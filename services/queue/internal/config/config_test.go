package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad_localDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("CAPTCHA_PROVIDER", "")
	t.Setenv("JOIN_GRANT_WAIT_MS", "750")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Captcha != CaptchaTurnstile || cfg.Join.Capacity != 300000 || cfg.Join.GrantWait != 750*time.Millisecond {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func valid() Config {
	c := Config{Env: "production", DatabaseURL: "postgres://x", JWTSecret: "s", Captcha: CaptchaTurnstile, TurnstileSecret: "t"}
	c.Join.Capacity, c.Join.SaleWindow, c.Join.RateLimits, c.Join.DevicePepper = 1, time.Hour, true, []byte("p")
	return c
}

func TestValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(*Config){
		"DATABASE_URL":             func(c *Config) { c.DatabaseURL = "" },
		"JWT_SECRET":               func(c *Config) { c.JWTSecret = "" },
		"DEVICE_PEPPER":            func(c *Config) { c.Join.DevicePepper = nil },
		"TURNSTILE_SECRET_KEY":     func(c *Config) { c.TurnstileSecret = "" },
		"CAPTCHA_PROVIDER=mock":    func(c *Config) { c.Captcha = CaptchaMock },
		"CAPTCHA_PROVIDER \"x\"":   func(c *Config) { c.Captcha = "x" },
		"JOIN_RATE_LIMITS_ENABLED": func(c *Config) { c.Join.RateLimits = false },
		"QUEUE_CAPACITY":           func(c *Config) { c.Join.Capacity = 0 },
	}
	for want, mutate := range cases {
		c := valid()
		mutate(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Validate() = %v", want, err)
		}
	}
}

func TestValidate_mockAllowedOutsideProduction(t *testing.T) {
	c := valid()
	c.Env, c.Captcha, c.TurnstileSecret = "preprod", CaptchaMock, ""
	if err := c.Validate(); err != nil {
		t.Fatalf("mock in preprod rejected: %v", err)
	}
}
