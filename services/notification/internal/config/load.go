package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{
			Env:                  envutil.Env("APP_ENV", "local"),
			Port:                 envutil.EnvInt("APP_PORT", 8084),
			AppBase:              envutil.Env("APP_BASE_URL", "http://localhost:3000"),
			MagicTokenTTLMinutes: envutil.EnvInt("MAGIC_TOKEN_TTL_MINUTES", 15),
		},
		Log: Log{
			Level:  envutil.Env("LOG_LEVEL", "info"),
			Format: envutil.Env("LOG_FORMAT", "json"),
		},
		Redis: Redis{
			Addr:     envutil.Env("REDIS_ADDR", "localhost:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       envutil.EnvInt("REDIS_DB", 0),
		},
		Email: Email{
			FromAddress:      os.Getenv("EMAIL_FROM_ADDRESS"),
			ConfigurationSet: os.Getenv("SES_CONFIGURATION_SET"),
			SendTimeout:      envutil.EnvDuration("EMAIL_SEND_TIMEOUT_MS", 10*time.Second),
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
