package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
)

// Load reads configuration from environment variables and returns a validated Config.
// It should be called once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{
			Env:  envutil.Env("APP_ENV", "local"),
			Port: envutil.EnvInt("APP_PORT", 8080),
		},
		Log: Log{
			Level:  envutil.Env("LOG_LEVEL", "info"),
			Format: envutil.Env("LOG_FORMAT", "json"),
		},
		DB: DB{
			URL:      os.Getenv("DATABASE_URL"),
			MaxConns: int32(envutil.EnvInt("DATABASE_MAX_CONNS", 2)),
		},
		Redis: Redis{
			Addr:     envutil.Env("REDIS_ADDR", "localhost:16379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			Cluster:  envutil.EnvBool("REDIS_CLUSTER", true),
		},
		Grant: Grant{DevicePepper: []byte(os.Getenv("DEVICE_PEPPER"))},
		Booking: Booking{
			MaxTicketsPerTransaction: envutil.EnvInt("MAX_TICKETS_PER_TRANSACTION", 3),
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
		// Required everywhere: an empty DSN makes pgx fall back to libpq defaults (localhost:5432, OS user).
		errs = append(errs, errors.New("DATABASE_URL is required (local: see example.env)"))
	}
	if c.App.Env == "local" && os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		// APP_ENV defaults to local; inside Lambda that default would silently skip the checks below.
		errs = append(errs, errors.New("APP_ENV must be set inside Lambda (local is for development only)"))
	}
	if c.App.Env != "local" && len(c.Grant.DevicePepper) == 0 {
		errs = append(errs, errors.New("DEVICE_PEPPER is required outside local: without queue's pepper every guest gets DEVICE_MISMATCH"))
	}
	if c.DB.MaxConns < 1 {
		errs = append(errs, fmt.Errorf("DATABASE_MAX_CONNS must be at least 1, got %d", c.DB.MaxConns))
	}
	if c.Booking.MaxTicketsPerTransaction < 1 {
		errs = append(errs, fmt.Errorf("MAX_TICKETS_PER_TRANSACTION must be at least 1, got %d", c.Booking.MaxTicketsPerTransaction))
	}
	return errors.Join(errs...)
}
