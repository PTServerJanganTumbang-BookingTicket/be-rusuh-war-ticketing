package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
)

// Load reads configuration from environment variables and validates it. Call it once, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{Env: envutil.Env("APP_ENV", "local"), Port: envutil.EnvInt("APP_PORT", 8080)},
		Log: Log{Level: envutil.Env("LOG_LEVEL", "info"), Format: envutil.Env("LOG_FORMAT", "json")},
		DB: DB{
			// DATABASE_READER_URL is the Aurora reader; locally one database serves both, so it falls back to DATABASE_URL.
			URL:      envutil.Env("DATABASE_READER_URL", os.Getenv("DATABASE_URL")),
			MaxConns: int32(envutil.EnvInt("DATABASE_MAX_CONNS", 2)),
		},
		Redis: Redis{
			Addr:     envutil.Env("REDIS_ADDR", "localhost:16379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			Cluster:  envutil.EnvBool("REDIS_CLUSTER", true),
		},
		Events: Events{
			QueueCapacity:      int64(envutil.EnvInt("QUEUE_CAPACITY", 300000)),
			CacheMaxAgeSeconds: envutil.EnvInt("EVENTS_CACHE_MAX_AGE_SECONDS", 30),
			DegradedMaxAge:     envutil.EnvInt("EVENTS_DEGRADED_MAX_AGE_SECONDS", 5),
			TurnstileSiteKey:   os.Getenv("TURNSTILE_SITE_KEY"),
		},
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []error
	if c.App.Port < 1 || c.App.Port > 65535 {
		errs = append(errs, fmt.Errorf("APP_PORT out of range: %d", c.App.Port))
	}
	if c.DB.URL == "" {
		errs = append(errs, errors.New("DATABASE_READER_URL (or DATABASE_URL) is required"))
	}
	if c.App.Env == "local" && os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		errs = append(errs, errors.New("APP_ENV must be set inside Lambda (local is for development only)"))
	}
	if c.App.Env != "local" && c.Events.TurnstileSiteKey == "" {
		errs = append(errs, errors.New("TURNSTILE_SITE_KEY is required outside local: the client needs it to fetch a CAPTCHA token before the sale opens"))
	}
	if c.DB.MaxConns < 1 {
		errs = append(errs, fmt.Errorf("DATABASE_MAX_CONNS must be at least 1, got %d", c.DB.MaxConns))
	}
	if c.Events.QueueCapacity < 1 {
		errs = append(errs, fmt.Errorf("QUEUE_CAPACITY must be at least 1, got %d", c.Events.QueueCapacity))
	}
	if c.Events.CacheMaxAgeSeconds < 0 || c.Events.DegradedMaxAge < 0 {
		errs = append(errs, errors.New("cache max-age values must not be negative"))
	}
	return errors.Join(errs...)
}
