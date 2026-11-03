package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Intervals the session TTL is derived from. Spelled out as named constants
// because the derivation is the point — ADR-005 §7 forbids writing a token
// lifetime as a loose number, and a bare `+ 1140` in the expression below
// would be exactly that.
const (
	// journeyWithPauseGrace is granted_at → final cancellation, plus the one
	// BR-22 pause grace a customer can be handed mid-journey:
	//
	//	300s booking + 600s payment + 30s payment grace = 930s
	//	+ 60s BR-22 pause grace                         = 990s
	journeyWithPauseGrace = 990 * time.Second

	// accessTokenSlack is journeyWithPauseGrace plus the same 150s absolute
	// margin ADR-005 §7 already uses for GRANT_TOKEN_TTL_SECONDS.
	accessTokenSlack = journeyWithPauseGrace + 150*time.Second

	// defaultSaleWindow is a FALLBACK, not a decision. The real value belongs
	// to the queue and is still open — ADR-005 Bagian 17 #10.
	defaultSaleWindow = 21600 * time.Second
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	env := getEnv("APP_ENV", "local")
	saleWindow := getEnvDuration("SALE_WINDOW_SECONDS", defaultSaleWindow)
	cfg := Config{
		App: App{
			Env:     env,
			Port:    getEnvInt("APP_PORT", 8081),
			BaseURL: getEnv("APP_BASE_URL", "http://localhost:8081"),
		},
		Log: Log{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
		},
		DB: DB{
			DSN:          os.Getenv("DATABASE_URL"),
			MaxConns:     int32(getEnvInt("DATABASE_MAX_CONNS", 4)),
			ConnLifetime: getEnvDuration("DATABASE_CONN_LIFETIME", 30*time.Minute),
		},
		Redis: Redis{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		NikVerify: NikVerify{
			BaseURL: os.Getenv("NIK_VERIFY_BASE_URL"),
			Timeout: getEnvDuration("NIK_VERIFY_TIMEOUT_MS", 5*time.Second),
		},
		Auth: Auth{
			JWTSecret:    os.Getenv("JWT_SECRET"),
			SaleWindow:   saleWindow,
			CookieDomain: os.Getenv("COOKIE_DOMAIN"),
			// Derived from the sale window by default, because the session has to
			// outlive the queue wait and not merely the journey (see config.go).
			// An explicit override is allowed — an operator may want to shorten
			// the revocation window after a key scare without touching queue
			// behaviour — but validate() enforces a floor on it.
			AccessTokenTTL: getEnvDuration("ACCESS_TOKEN_TTL_SECONDS", saleWindow+accessTokenSlack),
			// Secure cookies are the default everywhere except local, where a
			// plain-HTTP origin silently drops them.
			SecureCookies: getEnvBool("SECURE_COOKIES", env != "local"),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate refuses to start on a configuration that would be unsafe rather
// than merely wrong. Each required secret has no default, because a default
// secret is worse than a missing one: it starts cleanly and is forgotten.
func (c Config) validate() error {
	if c.App.Port < 1 || c.App.Port > 65535 {
		return fmt.Errorf("APP_PORT out of range: %d", c.App.Port)
	}
	if c.DB.DSN == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.NikVerify.BaseURL == "" {
		return fmt.Errorf("NIK_VERIFY_BASE_URL is required: this service does not call Dukcapil directly")
	}
	if c.Auth.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required and has no default: a known signing key forges sessions")
	}
	if c.Auth.SaleWindow <= 0 {
		return fmt.Errorf("SALE_WINDOW_SECONDS must be positive")
	}
	if c.Auth.AccessTokenTTL <= 0 {
		return fmt.Errorf("ACCESS_TOKEN_TTL_SECONDS must be positive")
	}
	// A session shorter than the wait plus the journey dies mid-purchase, and it
	// does so silently: the saved-NIK list 401s on the Detail Ticket page and the
	// customer is pushed onto the fail-closed Dukcapil path (config.go). Refuse
	// at boot rather than discover it under a flash crowd.
	if floor := c.Auth.SaleWindow + journeyWithPauseGrace; c.Auth.AccessTokenTTL < floor {
		return fmt.Errorf(
			"ACCESS_TOKEN_TTL_SECONDS (%s) is shorter than SALE_WINDOW_SECONDS + journey (%s): "+
				"the session would expire while the customer is still in the queue",
			c.Auth.AccessTokenTTL, floor)
	}
	if !c.App.IsLocal() && !c.Auth.SecureCookies {
		return fmt.Errorf("SECURE_COOKIES cannot be disabled outside local")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// getEnvDuration reads a duration. Keys ending in _MS are read as
// milliseconds and _SECONDS as seconds; anything else accepts a Go duration
// string such as "30m".
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if n, err := strconv.Atoi(v); err == nil {
		switch {
		case len(key) > 3 && key[len(key)-3:] == "_MS":
			return time.Duration(n) * time.Millisecond
		case len(key) > 8 && key[len(key)-8:] == "_SECONDS":
			return time.Duration(n) * time.Second
		default:
			return time.Duration(n) * time.Second
		}
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return fallback
}
