package config

import "testing"

func TestLoad_defaults(t *testing.T) {
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
