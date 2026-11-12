// Package config loads sse-server configuration from the environment.
package config

import (
	"errors"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/sse-server/internal/stream"
)

// Config is the sse-server configuration.
type Config struct {
	Env      string // local | preprod | production
	Port     int
	LogLevel string
	LogFmt   string

	RedisAddr    string
	RedisPass    string
	RedisCluster bool // true in AWS (ElastiCache cluster mode)

	// CORSOrigin is the value of Access-Control-Allow-Origin. Empty = no CORS headers (same-origin only).
	// Set to the frontend origin e.g. https://rush-my-ticket.my.id in production.
	CORSOrigin string

	// ShutdownGrace is how long the process waits for the drain, above the stream's own
	// deadline (ECS stopTimeout is 30 s).
	ShutdownGrace time.Duration

	Stream stream.Config
}

// Load reads and validates the configuration. Call once at startup.
func Load() (Config, error) {
	sc := stream.DefaultConfig()
	sc.Keepalive = envutil.EnvDuration("SSE_KEEPALIVE_SECONDS", sc.Keepalive)
	sc.StaleAfter = envutil.EnvDuration("SSE_STALE_AFTER_SECONDS", sc.StaleAfter)
	sc.DrainDeadline = envutil.EnvDuration("SSE_DRAIN_DEADLINE_SECONDS", sc.DrainDeadline)
	sc.ConnBuffer = envutil.EnvInt("SSE_CONN_BUFFER", sc.ConnBuffer)
	sc.IPLimit.Capacity = envutil.EnvInt("SSE_IP_LIMIT_PER_SECOND", sc.IPLimit.Capacity)
	sc.IPLimit.Refill = sc.IPLimit.Capacity

	cfg := Config{
		Env:           envutil.Env("APP_ENV", "local"),
		Port:          envutil.EnvInt("APP_PORT", 8085),
		LogLevel:      envutil.Env("LOG_LEVEL", "info"),
		LogFmt:        envutil.Env("LOG_FORMAT", "json"),
		RedisAddr:     envutil.Env("REDIS_ADDR", "localhost:16379"),
		RedisPass:     envutil.Env("REDIS_PASSWORD", ""),
		RedisCluster:  envutil.EnvBool("REDIS_CLUSTER", true),
		ShutdownGrace: envutil.EnvDuration("SHUTDOWN_GRACE_SECONDS", 5*time.Second),
		CORSOrigin:    envutil.Env("CORS_ALLOW_ORIGIN", ""),
		Stream:        sc,
	}
	return cfg, cfg.Validate()
}

// Validate rejects values that would break the stream's promises.
func (c Config) Validate() error {
	var errs []error
	if c.RedisAddr == "" {
		errs = append(errs, errors.New("REDIS_ADDR is required"))
	}
	s := c.Stream
	if s.Keepalive <= 0 || s.Keepalive > 55*time.Second {
		errs = append(errs, errors.New("SSE_KEEPALIVE_SECONDS must be in (0, 55]: the ALB idle timeout would cut a quieter stream"))
	}
	if s.StaleAfter <= s.Keepalive {
		errs = append(errs, errors.New("SSE_STALE_AFTER_SECONDS must be longer than the keepalive"))
	}
	if s.DrainDeadline <= 0 {
		errs = append(errs, errors.New("SSE_DRAIN_DEADLINE_SECONDS must be positive"))
	}
	if s.ConnBuffer < 2 {
		errs = append(errs, errors.New("SSE_CONN_BUFFER must be at least 2 (snapshot + one update)"))
	}
	if s.IPLimit.Capacity < 1 {
		errs = append(errs, errors.New("SSE_IP_LIMIT_PER_SECOND must be at least 1"))
	}
	return errors.Join(errs...)
}
