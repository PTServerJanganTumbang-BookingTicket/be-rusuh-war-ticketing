package config

import (
	"errors"
	"fmt"
	"os"
	"time"

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
		Grant: Grant{
			DevicePepper: []byte(os.Getenv("DEVICE_PEPPER")),
			JWTSecret:    jwtSecret(envutil.Env("APP_ENV", "local")),
		},
		NIKVerify: NIKVerify{
			FunctionName: os.Getenv("NIK_VERIFY_FUNCTION_NAME"),
			Timeout:      time.Duration(envutil.EnvInt("NIK_VERIFY_TIMEOUT_MS", 3000)) * time.Millisecond,
		},
		Booking: Booking{
			MaxTicketsPerTransaction: envutil.EnvInt("MAX_TICKETS_PER_TRANSACTION", 3),
			NIKLockTTL:               envutil.EnvDuration("SALE_WINDOW_SECONDS", 6*time.Hour),
			ETicketSigningKey:        []byte(os.Getenv("ETICKET_SIGNING_KEY")),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// localJWTSecret is user-service's example.env value, so a token from the local login verifies on the local booking.
// Never used outside APP_ENV=local: a default signing key is a forged-token generator.
const localJWTSecret = "local-dev-jwt-secret-not-for-any-deployed-environment"

func jwtSecret(appEnv string) string {
	if v := os.Getenv("JWT_SECRET"); v != "" {
		return v
	}
	if appEnv == "local" {
		return localJWTSecret
	}
	return ""
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
	if c.Grant.JWTSecret == "" {
		errs = append(errs, errors.New("JWT_SECRET is required outside local: a known signing key forges sessions"))
	}
	if c.App.Env != "local" && len(c.Booking.ETicketSigningKey) == 0 {
		errs = append(errs, errors.New("ETICKET_SIGNING_KEY is required outside local: e-ticket links must verify in user-service"))
	}
	if c.App.Env != "local" && c.NIKVerify.FunctionName == "" {
		errs = append(errs, errors.New("NIK_VERIFY_FUNCTION_NAME is required outside local: the local fake must never check a NIK in a deployed environment"))
	}
	if c.NIKVerify.Timeout < time.Millisecond {
		errs = append(errs, fmt.Errorf("NIK_VERIFY_TIMEOUT_MS must be at least 1, got %d", c.NIKVerify.Timeout.Milliseconds()))
	}
	if c.DB.MaxConns < 1 {
		errs = append(errs, fmt.Errorf("DATABASE_MAX_CONNS must be at least 1, got %d", c.DB.MaxConns))
	}
	if c.Booking.NIKLockTTL < time.Second {
		errs = append(errs, errors.New("SALE_WINDOW_SECONDS must be at least 1: a NIK lock without an expiry would leak for ever"))
	}
	if c.Booking.MaxTicketsPerTransaction < 1 {
		errs = append(errs, fmt.Errorf("MAX_TICKETS_PER_TRANSACTION must be at least 1, got %d", c.Booking.MaxTicketsPerTransaction))
	}
	return errors.Join(errs...)
}
