// Package config loads queue-service configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
)

// CAPTCHA providers.
const (
	CaptchaTurnstile = "turnstile"
	CaptchaMock      = "mock" // tests and k6 only; refused in production
)

// Config is the queue-service configuration.
type Config struct {
	Env      string // local | preprod | production
	Port     int
	LogLevel string
	LogFmt   string

	DatabaseURL  string // Aurora WRITER endpoint (join reads only, but must see fresh grants)
	DBMaxConns   int32
	DBLifetime   time.Duration
	RedisAddr    string
	RedisPass    string
	RedisCluster bool // true in AWS (ElastiCache cluster mode)

	JWTSecret    string
	CookieDomain string
	SSEBaseURL   string

	Captcha         string
	TurnstileSecret string
	TurnstileURL    string
	TurnstileWait   time.Duration

	Join usecase.Config
}

// Load reads and validates the configuration. Call once at startup.
func Load() (Config, error) {
	join := usecase.DefaultConfig()
	join.Capacity = int64(envutil.EnvInt("QUEUE_CAPACITY", int(join.Capacity)))
	join.SaleWindow = envutil.EnvDuration("SALE_WINDOW_SECONDS", join.SaleWindow)
	join.GrantWait = envutil.EnvDuration("JOIN_GRANT_WAIT_MS", join.GrantWait)
	join.RateLimits = envutil.EnvBool("JOIN_RATE_LIMITS_ENABLED", true)
	join.DevicePepper = []byte(os.Getenv("DEVICE_PEPPER"))

	cfg := Config{
		Env:             envutil.Env("APP_ENV", "local"),
		Port:            envutil.EnvInt("APP_PORT", 8084),
		LogLevel:        envutil.Env("LOG_LEVEL", "info"),
		LogFmt:          envutil.Env("LOG_FORMAT", "json"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		DBMaxConns:      int32(envutil.EnvInt("DATABASE_MAX_CONNS", 2)),
		DBLifetime:      envutil.EnvDuration("DATABASE_CONN_LIFETIME", 30*time.Minute),
		RedisAddr:       envutil.Env("REDIS_ADDR", "localhost:16379"),
		RedisPass:       os.Getenv("REDIS_PASSWORD"),
		RedisCluster:    envutil.EnvBool("REDIS_CLUSTER", true),
		JWTSecret:       os.Getenv("JWT_SECRET"),
		CookieDomain:    os.Getenv("COOKIE_DOMAIN"),
		SSEBaseURL:      os.Getenv("SSE_BASE_URL"),
		Captcha:         envutil.Env("CAPTCHA_PROVIDER", CaptchaTurnstile),
		TurnstileSecret: os.Getenv("TURNSTILE_SECRET_KEY"),
		TurnstileURL:    os.Getenv("TURNSTILE_SITEVERIFY_URL"),
		TurnstileWait:   envutil.EnvDuration("TURNSTILE_TIMEOUT_MS", time.Second),
		Join:            join,
	}
	return cfg, cfg.Validate()
}

// Validate rejects configurations that would run insecurely.
func (c Config) Validate() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if c.JWTSecret == "" {
		errs = append(errs, errors.New("JWT_SECRET is required: a known signing key forges sessions"))
	}
	local := c.Env == "local"
	if !local && len(c.Join.DevicePepper) == 0 {
		errs = append(errs, errors.New("DEVICE_PEPPER is required outside local"))
	}
	switch c.Captcha {
	case CaptchaTurnstile:
		if !local && c.TurnstileSecret == "" {
			errs = append(errs, errors.New("TURNSTILE_SECRET_KEY is required outside local (Secrets Manager / SSM, never the repo)"))
		}
	case CaptchaMock:
		if c.Env == "production" {
			errs = append(errs, errors.New("CAPTCHA_PROVIDER=mock is not allowed in production"))
		}
	default:
		errs = append(errs, fmt.Errorf("CAPTCHA_PROVIDER %q: want %q or %q", c.Captcha, CaptchaTurnstile, CaptchaMock))
	}
	if c.Env == "production" && !c.Join.RateLimits {
		errs = append(errs, errors.New("JOIN_RATE_LIMITS_ENABLED=false is not allowed in production"))
	}
	if c.Join.Capacity <= 0 || c.Join.SaleWindow <= 0 {
		errs = append(errs, errors.New("QUEUE_CAPACITY and SALE_WINDOW_SECONDS must be positive"))
	}
	return errors.Join(errs...)
}
