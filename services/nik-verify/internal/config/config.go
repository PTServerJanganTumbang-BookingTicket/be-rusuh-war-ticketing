package config

import "time"

// Config is the root configuration for the application.
type Config struct {
	App      App
	Log      Log
	DB       DB
	Redis    Redis
	Dukcapil Dukcapil
}

// App holds general application settings.
type App struct {
	Env  string // "local", "preprod", "production"
	Port int

	// NIKPepper is the KMS-managed HMAC secret for hashing NIKs.
	//
	// This service is the only holder. It has NO default: a guessable pepper
	// makes every stored hash brute-forceable, and because rotation is
	// impossible (ADR-005 §8) a leak can never be remediated. Startup fails
	// without it, loudly, rather than running on a placeholder.
	NIKPepper string
}

// Log holds configuration for structured logging.
type Log struct {
	Level  string // "debug", "info", "warn", "error"
	Format string // "json", "text"
}

// DB holds the shared Aurora connection settings.
type DB struct {
	DSN          string
	MaxConns     int32
	ConnLifetime time.Duration
}

// Redis holds the shared-state connection settings.
type Redis struct {
	Addr     string
	Password string
	DB       int
}

// Dukcapil holds the upstream settings from ADR-005 §8.
type Dukcapil struct {
	// Mode is "live" or "stub". Defaults to "live" when the variable is absent
	// entirely, so a misconfigured deployment fails rather than silently
	// verifying every NIK it is given. The deployed value comes from the
	// DukcapilMode stack parameter, which the POC defaults to "stub".
	Mode    string
	BaseURL string
	APIKey  string
	// ExtraHeaders are sent on every Dukcapil request alongside X-Api-Key, and
	// may be nil. Read from DUKCAPIL_EXTRA_HEADERS as a JSON object. For
	// gateways in front of Dukcapil that authenticate the caller their own way
	// - an Apidog mock server wants its own apidogToken header, for instance.
	ExtraHeaders     map[string]string
	Timeout          time.Duration
	RateLimitRPS     float64
	BurstCapacity    float64
	BreakerThreshold int
	BreakerOpenFor   time.Duration
}

// IsStub reports whether the no-network Dukcapil adapter is selected.
func (d Dukcapil) IsStub() bool { return d.Mode == "stub" }
