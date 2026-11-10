package config

import (
	"fmt"
	"os"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{
			Env:          envutil.Env("APP_ENV", "local"),
			Port:         envutil.EnvInt("APP_PORT", 8085),
			DevicePepper: os.Getenv("DEVICE_PEPPER"),
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
		Midtrans: Midtrans{
			// Sandbox by default. A deployment that forgot to set this should
			// charge nobody rather than charge everybody.
			BaseURL:          envutil.Env("MIDTRANS_BASE_URL", "https://api.sandbox.midtrans.com"),
			ServerKey:        os.Getenv("MIDTRANS_SERVER_KEY"),
			Timeout:          envutil.EnvDuration("MIDTRANS_TIMEOUT_MS", 10*time.Second),
			BreakerThreshold: envutil.EnvInt("PAYMENT_BREAKER_FAILURE_THRESHOLD", 5),
			BreakerOpenFor:   envutil.EnvDuration("PAYMENT_BREAKER_OPEN_SECONDS", 30*time.Second),
		},
		Payment: Payment{
			TTL:                 envutil.EnvDuration("PAYMENT_TTL_SECONDS", 600*time.Second),
			MinRetryRemaining:   envutil.EnvDuration("MIN_RETRY_REMAINING_SECONDS", 60*time.Second),
			MaxAttempts:         envutil.EnvInt("MAX_PAYMENT_ATTEMPTS", 5),
			SettledTombstoneTTL: envutil.EnvDuration("SETTLED_TOMBSTONE_SECONDS", 3600*time.Second),
			IconBase:            envutil.Env("PAYMENT_ICON_BASE", "https://cdn.rusuh.id/icons"),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate refuses to start on a configuration that would be unsafe rather
// than merely wrong. Every check here is a condition whose symptom would
// otherwise appear in production as a customer-visible payment failure.
func (c Config) validate() error {
	if c.DB.DSN == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.Midtrans.ServerKey == "" {
		// Signs charges and verifies every notification signature. Missing, it
		// would reject all valid callbacks and confirm no booking ever - while
		// the service still reported healthy.
		return fmt.Errorf("MIDTRANS_SERVER_KEY is required and has no default: without it every notification fails signature verification")
	}
	if c.App.DevicePepper == "" {
		return fmt.Errorf("DEVICE_PEPPER is required and has no default: without it every guest grant fails the device check")
	}
	if c.Payment.TTL <= 0 {
		return fmt.Errorf("PAYMENT_TTL_SECONDS must be positive")
	}
	if c.Payment.MaxAttempts < 1 {
		return fmt.Errorf("MAX_PAYMENT_ATTEMPTS must be at least 1")
	}
	if c.Payment.MinRetryRemaining >= c.Payment.TTL {
		// A floor at or above the whole window refuses every switch, which
		// looks like a broken endpoint rather than a policy.
		return fmt.Errorf("MIN_RETRY_REMAINING_SECONDS (%s) must be below PAYMENT_TTL_SECONDS (%s)",
			c.Payment.MinRetryRemaining, c.Payment.TTL)
	}
	return nil
}
