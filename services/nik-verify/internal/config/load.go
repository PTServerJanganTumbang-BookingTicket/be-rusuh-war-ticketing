package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{
			Env:       getEnv("APP_ENV", "local"),
			Port:      getEnvInt("APP_PORT", 8082),
			NIKPepper: os.Getenv("NIK_PEPPER"),
		},
		Log: Log{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
		},
		DB: DB{
			DSN:          os.Getenv("DATABASE_URL"),
			MaxConns:     int32(getEnvInt("DATABASE_MAX_CONNS", 4)),
			ConnLifetime: getEnvDuration("DATABASE_CONN_LIFETIME", 30*time.Minute),
		},
		Redis: Redis{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		Dukcapil: Dukcapil{
			Mode:             getEnv("DUKCAPIL_MODE", "live"),
			BaseURL:          os.Getenv("DUKCAPIL_BASE_URL"),
			APIKey:           os.Getenv("DUKCAPIL_API_KEY"),
			Timeout:          getEnvDuration("DUKCAPIL_TIMEOUT_MS", 3*time.Second),
			RateLimitRPS:     getEnvFloat("DUKCAPIL_RATE_LIMIT_RPS", 40),
			BurstCapacity:    getEnvFloat("DUKCAPIL_BURST_CAPACITY", 80),
			BreakerThreshold: getEnvInt("DUKCAPIL_BREAKER_FAILURE_THRESHOLD", 5),
			BreakerOpenFor:   getEnvDuration("DUKCAPIL_BREAKER_OPEN_SECONDS", 30*time.Second),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate refuses to start on a configuration that would be unsafe rather than
// merely wrong. Every check here guards an invariant that is invisible at
// runtime: a placeholder pepper still produces plausible hashes, and a stub
// Dukcapil still returns confident answers.
func (c Config) validate() error {
	if c.App.Port < 1 || c.App.Port > 65535 {
		return fmt.Errorf("APP_PORT out of range: %d", c.App.Port)
	}
	if c.App.NIKPepper == "" {
		return fmt.Errorf("NIK_PEPPER is required and has no default: it can never be rotated, so a placeholder is permanent")
	}
	if c.DB.DSN == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if !c.Dukcapil.IsStub() {
		if c.Dukcapil.BaseURL == "" {
			return fmt.Errorf("DUKCAPIL_BASE_URL is required unless DUKCAPIL_MODE=stub")
		}
		if c.Dukcapil.APIKey == "" {
			return fmt.Errorf("DUKCAPIL_API_KEY is required unless DUKCAPIL_MODE=stub")
		}
	}
	if c.Dukcapil.IsStub() && c.App.Env == "production" {
		return fmt.Errorf("DUKCAPIL_MODE=stub is not permitted in production: every NIK would verify")
	}
	if c.Dukcapil.RateLimitRPS <= 0 {
		return fmt.Errorf("DUKCAPIL_RATE_LIMIT_RPS must be positive")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// getEnvDuration reads a duration. Keys ending in _MS are read as
// milliseconds and _SECONDS as seconds, matching the names ADR-005 §8 uses;
// anything else accepts a Go duration string.
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if n, err := strconv.Atoi(v); err == nil {
		switch {
		case len(key) > 3 && key[len(key)-3:] == "_MS":
			return time.Duration(n) * time.Millisecond
		case len(key) > 8 && key[len(key)-8:] == "_SECONDS":
			return time.Duration(n) * time.Second
		default:
			return time.Duration(n) * time.Second
		}
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return fallback
}
