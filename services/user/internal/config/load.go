package config

import (
	"fmt"
	"os"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
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
	env := envutil.Env("APP_ENV", "local")
	saleWindow := envutil.EnvDuration("SALE_WINDOW_SECONDS", defaultSaleWindow)
	cfg := Config{
		App: App{
			Env:     env,
			Port:    envutil.EnvInt("APP_PORT", 8081),
			BaseURL: envutil.Env("APP_BASE_URL", "http://localhost:8081"),
		},
		Log: Log{
			Level:  envutil.Env("LOG_LEVEL", "info"),
			Format: envutil.Env("LOG_FORMAT", "json"),
		},
		DB: DB{
			DSN:          os.Getenv("DATABASE_URL"),
			MaxConns:     int32(envutil.EnvInt("DATABASE_MAX_CONNS", 4)),
			ConnLifetime: envutil.EnvDuration("DATABASE_CONN_LIFETIME", 30*time.Minute),
		},
		Redis: Redis{
			Addr:     envutil.Env("REDIS_ADDR", "localhost:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       envutil.EnvInt("REDIS_DB", 0),
		},
		NikVerify: NikVerify{
			BaseURL: os.Getenv("NIK_VERIFY_BASE_URL"),
			Timeout: envutil.EnvDuration("NIK_VERIFY_TIMEOUT_MS", 5*time.Second),
		},
		Auth: Auth{
			JWTSecret:    os.Getenv("JWT_SECRET"),
			SaleWindow:   saleWindow,
			CookieDomain: os.Getenv("COOKIE_DOMAIN"),
			// Derived from the sale window by default, because the session has to
			// outlive the queue wait and not merely the journey (see config.go).
			AccessTokenTTL: envutil.EnvDuration("ACCESS_TOKEN_TTL_SECONDS", saleWindow+accessTokenSlack),
			// Secure cookies are the default everywhere except local, where a
			// plain-HTTP origin silently drops them.
			SecureCookies: envutil.EnvBool("SECURE_COOKIES", env != "local"),
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
