package config

// Config is the root configuration for the application.
type Config struct {
	App     App
	Log     Log
	DB      DB
	Redis   Redis
	Grant   Grant
	Booking Booking
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

// DB is the Aurora writer: every booking read and write uses it (docs/05-booking/README.md §3 rule 5).
type DB struct {
	URL      string
	MaxConns int32
}

// Redis is the ElastiCache cluster (grant hashes, stock).
type Redis struct {
	Addr     string
	Password string
	Cluster  bool
}

// Grant configures pkg/grantauth.
type Grant struct {
	DevicePepper []byte // DEVICE_PEPPER, the same value queue's join uses
	JWTSecret    string // JWT_SECRET, the key user-service signs access tokens with (account grants, ADR-006 §8.17)
}

// Booking holds the purchase rules and the e-ticket link key.
type Booking struct {
	MaxTicketsPerTransaction int    // BR-01
	ETicketSigningKey        []byte // ETICKET_SIGNING_KEY, the same key notification and user-service use
}
