package config

import "time"

// Config is the root configuration for the application.
type Config struct {
	App   App
	Log   Log
	Redis Redis
	Email Email
}

// App holds general application settings.
type App struct {
	Env  string
	Port int

	// AppBase is the public frontend base URL used to build emailed links.
	// It points at the app, not the API: the verify endpoint reads its token
	// from a cookie (contract §1.3), so the app route sets the cookie and
	// then calls the endpoint.
	AppBase string

	// MagicTokenTTLMinutes is quoted in link emails. Mirrors
	// entity.MagicTokenTTL in services/user; if that changes, this must too.
	MagicTokenTTLMinutes int
}

// Log holds configuration for structured logging.
type Log struct {
	Level  string
	Format string
}

// Redis holds the dedupe-store connection settings.
type Redis struct {
	Addr     string
	Password string
	DB       int
}

// Email holds the provider settings.
type Email struct {
	FromAddress      string
	ConfigurationSet string
	SendTimeout      time.Duration
}
