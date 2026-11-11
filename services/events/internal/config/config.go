// Package config reads the events service's environment.
package config

// Config is the root configuration.
type Config struct {
	App    App
	Log    Log
	DB     DB
	Redis  Redis
	Events Events
}

// App holds general application settings.
type App struct {
	Env  string // "local", "preprod", "production"
	Port int
}

// Log holds structured logging settings.
type Log struct {
	Level  string
	Format string
}

// DB is the Aurora READER endpoint: this service only reads.
type DB struct {
	URL      string
	MaxConns int32
}

// Redis is the ElastiCache cluster (queue:{ev}, stock keys). Read only.
type Redis struct {
	Addr     string
	Password string
	Cluster  bool
}

// Events holds the page rules.
type Events struct {
	QueueCapacity      int64  // QUEUE_CAPACITY, the same value the queue service enforces
	CacheMaxAgeSeconds int    // EVENTS_CACHE_MAX_AGE_SECONDS
	DegradedMaxAge     int    // EVENTS_DEGRADED_MAX_AGE_SECONDS, used when Redis is down
	TurnstileSiteKey   string // TURNSTILE_SITE_KEY
}
