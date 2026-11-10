package config

import (
	"strings"
	"testing"
)

func TestLoad_defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://local")
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	// no env set -> t.Setenv to empty forces the fallback branch deterministically
	t.Setenv("APP_ENV", "")
	t.Setenv("APP_PORT", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.App.Env != "local" {
		t.Errorf("App.Env = %q, want local", cfg.App.Env)
	}
	if cfg.App.Port != 8080 {
		t.Errorf("App.Port = %d, want 8080", cfg.App.Port)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want info", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want json", cfg.Log.Format)
	}
}

func TestLoad_envOverrides(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://prod")
	t.Setenv("DEVICE_PEPPER", "pepper")
	t.Setenv("JWT_SECRET", "jwt")
	t.Setenv("ETICKET_SIGNING_KEY", "key")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.App.Env != "production" {
		t.Errorf("App.Env = %q, want production", cfg.App.Env)
	}
	if cfg.App.Port != 9090 {
		t.Errorf("App.Port = %d, want 9090", cfg.App.Port)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format = %q, want text", cfg.Log.Format)
	}
}

func TestLoad_invalidPortFallsBack(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://local")
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("APP_PORT", "not-a-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.App.Port != 8080 {
		t.Errorf("App.Port = %d, want 8080 (non-numeric env falls back)", cfg.App.Port)
	}
}

func TestLoad_portOutOfRange(t *testing.T) {
	t.Setenv("APP_PORT", "70000")

	_, err := Load()
	if err == nil {
		t.Fatal("Load with APP_PORT=70000 returned nil error, want out-of-range error")
	}
}

func TestLoad_portZeroOutOfRange(t *testing.T) {
	t.Setenv("APP_PORT", "0")

	_, err := Load()
	if err == nil {
		t.Fatal("Load with APP_PORT=0 returned nil error, want out-of-range error")
	}
}

// clearB1 unsets every variable the booking config reads, so a test sees only what it sets.
func clearB1(t *testing.T) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "APP_PORT", "DATABASE_MAX_CONNS", "REDIS_ADDR", "REDIS_PASSWORD",
		"REDIS_CLUSTER", "DEVICE_PEPPER", "JWT_SECRET", "MAX_TICKETS_PER_TRANSACTION",
		"AWS_LAMBDA_FUNCTION_NAME", "ETICKET_SIGNING_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("DATABASE_URL", "postgres://local")
}

func TestLocalDefaultsMatchTheComposeStackAndThePurchaseRules(t *testing.T) {
	clearB1(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("local defaults must load: %v", err)
	}
	if cfg.Redis.Addr != "localhost:16379" || !cfg.Redis.Cluster {
		t.Errorf("redis = %+v, want the compose cluster at localhost:16379", cfg.Redis)
	}
	if cfg.Booking.MaxTicketsPerTransaction != 3 {
		t.Errorf("MaxTicketsPerTransaction = %d, want 3 (BR-01)", cfg.Booking.MaxTicketsPerTransaction)
	}
	if cfg.DB.MaxConns < 1 {
		t.Errorf("DB.MaxConns = %d, want at least 1", cfg.DB.MaxConns)
	}
}

func TestEnvironmentValuesAreRead(t *testing.T) {
	clearB1(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_CLUSTER", "false")
	t.Setenv("DEVICE_PEPPER", "pepper")
	t.Setenv("JWT_SECRET", "jwt")
	t.Setenv("MAX_TICKETS_PER_TRANSACTION", "2")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.URL != "postgres://x" || cfg.Redis.Addr != "redis:6379" || cfg.Redis.Cluster ||
		string(cfg.Grant.DevicePepper) != "pepper" || cfg.Grant.JWTSecret != "jwt" ||
		cfg.Booking.MaxTicketsPerTransaction != 2 {
		t.Fatalf("config = %+v", cfg)
	}
}

// ADR-006 §8.17: without the signing secret the guard cannot verify the
// session behind an account grant, so every logged-in call would be 503. A
// deployment learns that at boot rather than from its customers.
func TestAJWTSecretIsRequiredOutsideLocal(t *testing.T) {
	clearB1(t)
	t.Setenv("APP_ENV", "preprod")
	t.Setenv("DEVICE_PEPPER", "pepper")
	t.Setenv("ETICKET_SIGNING_KEY", "key")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("err = %v, want JWT_SECRET reported", err)
	}
}

func TestADevicePepperAndADatabaseAreRequiredOutsideLocal(t *testing.T) {
	clearB1(t)
	t.Setenv("APP_ENV", "preprod")
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DEVICE_PEPPER") || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want both DEVICE_PEPPER and DATABASE_URL reported", err)
	}
}

func TestADatabaseIsRequiredLocallyToo(t *testing.T) {
	clearB1(t)
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want DATABASE_URL reported", err)
	}
}

func TestTheLocalDefaultIsRefusedInsideLambda(t *testing.T) {
	clearB1(t)
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "rusuh-booking")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("err = %v, want APP_ENV reported", err)
	}
}

func TestAPerOrderLimitBelowOneIsRejected(t *testing.T) {
	clearB1(t)
	t.Setenv("MAX_TICKETS_PER_TRANSACTION", "0")
	if _, err := Load(); err == nil {
		t.Fatal("MAX_TICKETS_PER_TRANSACTION=0 must be rejected")
	}
}

func TestTheETicketKeyIsReadAndRequiredOutsideLocal(t *testing.T) {
	clearB1(t)
	t.Setenv("ETICKET_SIGNING_KEY", "k")
	if cfg, err := Load(); err != nil || string(cfg.Booking.ETicketSigningKey) != "k" {
		t.Fatalf("key = %q (%v)", cfg.Booking.ETicketSigningKey, err)
	}
	clearB1(t)
	t.Setenv("APP_ENV", "preprod")
	t.Setenv("DEVICE_PEPPER", "p")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ETICKET_SIGNING_KEY") {
		t.Fatalf("err = %v, want ETICKET_SIGNING_KEY reported outside local", err)
	}
}
