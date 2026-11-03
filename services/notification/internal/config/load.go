package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{
			Env:                  getEnv("APP_ENV", "local"),
			Port:                 getEnvInt("APP_PORT", 8084),
			AppBase:              getEnv("APP_BASE_URL", "http://localhost:3000"),
			MagicTokenTTLMinutes: getEnvInt("MAGIC_TOKEN_TTL_MINUTES", 15),
		},
		Log: Log{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
		},
		Redis: Redis{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		Email: Email{
			FromAddress:      os.Getenv("EMAIL_FROM_ADDRESS"),
			ConfigurationSet: os.Getenv("SES_CONFIGURATION_SET"),
			SendTimeout:      getEnvDuration("EMAIL_SEND_TIMEOUT_MS", 10*time.Second),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.App.Port < 1 || c.App.Port > 65535 {
		return fmt.Errorf("APP_PORT out of range: %d", c.App.Port)
	}
	if c.Email.FromAddress == "" {
		return fmt.Errorf("EMAIL_FROM_ADDRESS is required")
	}
	if !strings.Contains(c.Email.FromAddress, "@") {
		return fmt.Errorf("EMAIL_FROM_ADDRESS is not an address: %q", c.Email.FromAddress)
	}
	if c.App.AppBase == "" {
		return fmt.Errorf("APP_BASE_URL is required: emailed links have nowhere to point")
	}
	// A link that resolves over plain HTTP would carry a single-use login
	// token in cleartext, so outside local it must be HTTPS.
	if c.App.Env != "local" && !strings.HasPrefix(c.App.AppBase, "https://") {
		return fmt.Errorf("APP_BASE_URL must be https outside local: emailed links carry login tokens")
	}
	if c.App.MagicTokenTTLMinutes < 1 {
		return fmt.Errorf("MAGIC_TOKEN_TTL_MINUTES must be positive")
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

// getEnvDuration reads a duration. Keys ending in _MS are read as
// milliseconds and _SECONDS as seconds; anything else accepts a Go duration
// string such as "30m".
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
