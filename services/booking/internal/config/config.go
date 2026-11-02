package config

// Config is the root configuration for the application.
type Config struct {
	App App
	Log Log
}

// App holds general application settings.
type App struct {
	Env  string // "local", "preprod", "production"
	Port int
}

// Log holds configuration for structured logging.
type Log struct {
	Level  string // "debug", "info", "warn", "error"
	Format string // "json", "text"
}
