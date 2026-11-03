package config

import "time"

// Config is the root configuration for the application.
type Config struct {
	App   App
	Log   Log
	DB    DB
	Redis Redis
	Queue Queue
}

// App holds general application settings.
type App struct {
	Env  string
	Port int

	// Interval is how often a pass runs in server mode.
	//
	// ADR-005 §9 step 4 says every second. That is reachable in server mode;
	// as a scheduled Lambda the floor is one minute, so the Lambda entrypoint
	// drains in a loop within its own budget instead. Both are acceptable
	// because publishing is the FALLBACK path — see the service README.
	Interval time.Duration

	// DrainBudget caps how long one Lambda invocation keeps draining.
	DrainBudget time.Duration

	// BatchSize is rows per pass (ADR-005 §9 step 4: 500).
	BatchSize int
}

// Log holds configuration for structured logging.
type Log struct {
	Level  string
	Format string
}

// DB holds the shared Aurora connection settings.
type DB struct {
	DSN          string
	MaxConns     int32
	ConnLifetime time.Duration
}

// Redis holds the lease-guard connection settings.
type Redis struct {
	Addr     string
	Password string
	DB       int
}

// Queue holds the broker settings.
type Queue struct {
	NotificationURL string
	LeaseTTL        time.Duration
	LeaseDoneTTL    time.Duration
}
