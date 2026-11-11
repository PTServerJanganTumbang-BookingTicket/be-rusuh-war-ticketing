package config

import "testing"

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "APP_PORT", "DATABASE_URL", "DATABASE_READER_URL", "QUEUE_CAPACITY", "TURNSTILE_SITE_KEY", "AWS_LAMBDA_FUNCTION_NAME"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadDefaultsLocally(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x"})
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.URL != "postgres://x" || cfg.Events.QueueCapacity != 300000 || cfg.Events.CacheMaxAgeSeconds != 30 ||
		cfg.Events.DegradedMaxAge != 5 || cfg.App.Port != 8080 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadPrefersTheReaderURL(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://writer", "DATABASE_READER_URL": "postgres://reader"})
	cfg, err := Load()
	if err != nil || cfg.DB.URL != "postgres://reader" {
		t.Fatalf("cfg = %+v err = %v", cfg, err)
	}
}

func TestLoadRefusesBadSettings(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no database":               {},
		"port out of range":         {"DATABASE_URL": "x", "APP_PORT": "70000"},
		"zero queue capacity":       {"DATABASE_URL": "x", "QUEUE_CAPACITY": "0"},
		"no site key outside local": {"DATABASE_URL": "x", "APP_ENV": "production"},
		"local inside lambda":       {"DATABASE_URL": "x", "AWS_LAMBDA_FUNCTION_NAME": "f"},
	} {
		setEnv(t, env)
		if _, err := Load(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	setEnv(t, map[string]string{"DATABASE_URL": "x", "APP_ENV": "production", "TURNSTILE_SITE_KEY": "k"})
	if _, err := Load(); err != nil {
		t.Errorf("a complete production config must load: %v", err)
	}
}
