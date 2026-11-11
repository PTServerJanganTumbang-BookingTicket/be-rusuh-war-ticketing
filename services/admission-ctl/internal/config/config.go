// Package config reads the admission-ctl settings from the environment.
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
)

// Config is everything the worker needs.
type Config struct {
	DatabaseURL  string
	RedisAddr    string
	RedisPass    string
	RedisCluster bool
	LogLevel     string
	LogFmt       string
	DBMaxConns   int32
	// EventRefresh is how often the worker scans Redis for sales (queue:{ev} hashes) and their stock categories.
	EventRefresh time.Duration
	Tick         usecase.Config
	// Reconcile tunes the repair of Redis stock and NIK locks from Aurora (docs/05-booking/04-stock-reconciliation.md).
	Reconcile usecase.ReconcileConfig
}

// Load reads and validates the environment.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:  envutil.Env("DATABASE_URL", ""),
		RedisAddr:    envutil.Env("REDIS_ADDR", ""),
		RedisPass:    envutil.Env("REDIS_PASSWORD", ""),
		RedisCluster: envutil.EnvBool("REDIS_CLUSTER", true),
		LogLevel:     envutil.Env("LOG_LEVEL", "info"),
		LogFmt:       envutil.Env("LOG_FORMAT", "json"),
		DBMaxConns:   int32(envutil.EnvInt("DB_MAX_CONNS", 4)),
		EventRefresh: time.Duration(envutil.EnvInt("EVENT_REFRESH_SECONDS", 30)) * time.Second,
	}
	t := usecase.DefaultConfig()
	t.Interval = time.Duration(envutil.EnvInt("TICK_INTERVAL_MS", 500)) * time.Millisecond
	t.MaxSlots = int64(envutil.EnvInt("MAX_CONCURRENT_SLOTS", int(t.MaxSlots)))
	t.BookingTTL = time.Duration(envutil.EnvInt("BOOKING_TTL_SECONDS", 300)) * time.Second
	t.GrantTTL = time.Duration(envutil.EnvInt("GRANT_TOKEN_TTL_SECONDS", 1080)) * time.Second
	t.Heartbeat = time.Duration(envutil.EnvInt("HEARTBEAT_SECONDS", 10)) * time.Second
	t.CloseAfter = time.Duration(envutil.EnvInt("FULL_BOOKED_CLOSE_SECONDS", 930)) * time.Second
	t.EtaSeedMinutes = int64(envutil.EnvInt("ETA_SEED_SLOT_MINUTES", int(t.EtaSeedMinutes)))
	c.Tick = t
	rc := usecase.DefaultReconcileConfig()
	rc.Every = time.Duration(envutil.EnvInt("STOCK_RECONCILE_SECONDS", int(rc.Every/time.Second))) * time.Second
	rc.Threshold = int64(envutil.EnvInt("STOCK_DRIFT_THRESHOLD", int(rc.Threshold)))
	rc.MaxCorrection = int64(envutil.EnvInt("STOCK_MAX_CORRECTION", int(rc.MaxCorrection)))
	rc.LockTTL = time.Duration(envutil.EnvInt("SALE_WINDOW_SECONDS", int(rc.LockTTL/time.Second))) * time.Second
	c.Reconcile = rc
	return c, c.Validate()
}

// Validate rejects settings that would make the tick unsafe.
func (c Config) Validate() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if c.RedisAddr == "" {
		errs = append(errs, errors.New("REDIS_ADDR is required"))
	}
	if c.EventRefresh < time.Second {
		errs = append(errs, fmt.Errorf("EVENT_REFRESH_SECONDS must be at least 1, got %s", c.EventRefresh))
	}
	t := c.Tick
	switch {
	case t.Interval < 50*time.Millisecond:
		errs = append(errs, fmt.Errorf("TICK_INTERVAL_MS must be at least 50, got %s", t.Interval))
	case t.MaxSlots < 1:
		errs = append(errs, errors.New("MAX_CONCURRENT_SLOTS must be at least 1"))
	case t.BookingTTL < time.Second || t.GrantTTL < t.BookingTTL:
		errs = append(errs, errors.New("GRANT_TOKEN_TTL_SECONDS must be at least BOOKING_TTL_SECONDS"))
	case t.EtaSeedMinutes < 1:
		errs = append(errs, errors.New("ETA_SEED_SLOT_MINUTES must be at least 1"))
	}
	if rc := c.Reconcile; rc.Every < 0 {
		errs = append(errs, errors.New("STOCK_RECONCILE_SECONDS must not be negative (0 turns reconciliation off)"))
	} else if rc.Every > 0 {
		if rc.Threshold < 1 {
			errs = append(errs, errors.New("STOCK_DRIFT_THRESHOLD must be at least 1"))
		}
		if rc.MaxCorrection < rc.Threshold {
			errs = append(errs, errors.New("STOCK_MAX_CORRECTION must be at least STOCK_DRIFT_THRESHOLD"))
		}
		if rc.LockTTL < time.Second {
			errs = append(errs, errors.New("SALE_WINDOW_SECONDS must be at least 1: a restored NIK lock without an expiry would leak for ever"))
		}
	}
	return errors.Join(errs...)
}
