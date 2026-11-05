package config

import (
	"fmt"
	"os"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/domain/entity"
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{
			Env:         envutil.Env("APP_ENV", "local"),
			Port:        envutil.EnvInt("APP_PORT", 8083),
			Interval:    envutil.EnvDuration("PUBLISH_INTERVAL_MS", time.Second),
			DrainBudget: envutil.EnvDuration("DRAIN_BUDGET_SECONDS", 45*time.Second),
			BatchSize:   envutil.EnvInt("PUBLISH_BATCH_SIZE", entity.BatchSize),
		},
		Log: Log{
			Level:  envutil.Env("LOG_LEVEL", "info"),
			Format: envutil.Env("LOG_FORMAT", "json"),
		},
		DB: DB{
			DSN:          os.Getenv("DATABASE_URL"),
			MaxConns:     int32(envutil.EnvInt("DATABASE_MAX_CONNS", 4)),
			ConnLifetime: envutil.EnvDuration("DATABASE_CONN_LIFETIME", 30*time.Minute),
		},
		Redis: Redis{
			Addr:     envutil.Env("REDIS_ADDR", "localhost:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       envutil.EnvInt("REDIS_DB", 0),
		},
		Queue: Queue{
			NotificationURL: os.Getenv("NOTIFICATION_QUEUE_URL"),
			LeaseTTL:        envutil.EnvDuration("OUTBOX_LEASE_SECONDS", entity.LeaseTTL),
			LeaseDoneTTL:    envutil.EnvDuration("OUTBOX_LEASE_DONE_SECONDS", entity.LeaseDoneTTL),
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
