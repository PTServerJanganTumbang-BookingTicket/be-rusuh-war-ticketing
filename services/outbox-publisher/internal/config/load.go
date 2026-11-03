package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/domain/entity"
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{
			Env:         getEnv("APP_ENV", "local"),
			Port:        getEnvInt("APP_PORT", 8083),
			Interval:    getEnvDuration("PUBLISH_INTERVAL_MS", time.Second),
			DrainBudget: getEnvDuration("DRAIN_BUDGET_SECONDS", 45*time.Second),
			BatchSize:   getEnvInt("PUBLISH_BATCH_SIZE", entity.BatchSize),
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
		Queue: Queue{
			NotificationURL: os.Getenv("NOTIFICATION_QUEUE_URL"),
			LeaseTTL:        getEnvDuration("OUTBOX_LEASE_SECONDS", entity.LeaseTTL),
			LeaseDoneTTL:    getEnvDuration("OUTBOX_LEASE_DONE_SECONDS", entity.LeaseDoneTTL),
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
	if c.DB.DSN == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.Queue.NotificationURL == "" {
		return fmt.Errorf("NOTIFICATION_QUEUE_URL is required: there is nowhere to publish without it")
	}
	if c.App.BatchSize < 1 {
		return fmt.Errorf("PUBLISH_BATCH_SIZE must be positive")
	}
	if c.App.Interval <= 0 {
		return fmt.Errorf("PUBLISH_INTERVAL_MS must be positive")
	}
	// A lease shorter than a pass lets a second worker claim a row the first
	// is still publishing, which is the duplicate this guard exists to prevent.
	if c.Queue.LeaseTTL < c.App.Interval {
		return fmt.Errorf("OUTBOX_LEASE_SECONDS (%s) must not be shorter than PUBLISH_INTERVAL_MS (%s)",
			c.Queue.LeaseTTL, c.App.Interval)
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
