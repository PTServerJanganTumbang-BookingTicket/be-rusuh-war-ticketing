package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	extraHeaders, err := parseExtraHeaders(os.Getenv("DUKCAPIL_EXTRA_HEADERS"))
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		App: App{
			Env:       envutil.Env("APP_ENV", "local"),
			Port:      envutil.EnvInt("APP_PORT", 8082),
			NIKPepper: os.Getenv("NIK_PEPPER"),
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
		Dukcapil: Dukcapil{
			Mode:             envutil.Env("DUKCAPIL_MODE", "live"),
			BaseURL:          os.Getenv("DUKCAPIL_BASE_URL"),
			APIKey:           os.Getenv("DUKCAPIL_API_KEY"),
			Timeout:          envutil.EnvDuration("DUKCAPIL_TIMEOUT_MS", 3*time.Second),
			RateLimitRPS:     envutil.EnvFloat("DUKCAPIL_RATE_LIMIT_RPS", 40),
			BurstCapacity:    envutil.EnvFloat("DUKCAPIL_BURST_CAPACITY", 80),
			BreakerThreshold: envutil.EnvInt("DUKCAPIL_BREAKER_FAILURE_THRESHOLD", 5),
			BreakerOpenFor:   envutil.EnvDuration("DUKCAPIL_BREAKER_OPEN_SECONDS", 30*time.Second),
			ExtraHeaders:     extraHeaders,
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

// parseExtraHeaders reads a JSON object of header name to value. An empty or
// absent value yields nil, which sends no extra headers.
//
// A malformed value is an error rather than a silent nil: the headers it
// carries are usually what authenticates the caller to whatever fronts
// Dukcapil, so dropping them quietly turns a typo into every verification
// failing with 401 for no visible reason.
func parseExtraHeaders(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return nil, fmt.Errorf(`DUKCAPIL_EXTRA_HEADERS must be a JSON object of string to string, e.g. {"apidogToken":"..."}: %w`, err)
	}
	for name := range headers {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("DUKCAPIL_EXTRA_HEADERS contains an empty header name")
		}
	}
	if len(headers) == 0 {
		return nil, nil
	}
	return headers, nil
}
