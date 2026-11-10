package config

import "time"

// Config is the root configuration for payment-api.
type Config struct {
	App      App
	Log      Log
	DB       DB
	Redis    Redis
	Midtrans Midtrans
	Payment  Payment
}

// App holds general application settings.
type App struct {
	Env  string // "local", "preprod", "production"
	Port int

	// DevicePepper keys the guest device binding: HMAC(pepper, X-Device-Id)
	// must equal the device_hash stored on the grant (ADR-006 §8.9).
	//
	// Required, no default. Without it every guest grant fails the device
	// check, so a placeholder would not fail safe - it would fail every guest
	// checkout while looking configured.
	//
	// NOT the NIK pepper and NOT JWT_SECRET. It may be rotated between sales
	// but never during one: rotation invalidates every live binding at once.
	DevicePepper string
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

// Midtrans holds the Core API settings.
type Midtrans struct {
	// BaseURL is the sandbox or production Core API host. Sandbox by default:
	// a misconfigured deployment should charge nobody, not charge everybody.
	BaseURL string

	// ServerKey authenticates charges AND verifies notification signatures.
	// Required, no default - without it every notification fails verification
	// and no payment is ever confirmed.
	ServerKey string

	Timeout time.Duration

	// BreakerThreshold consecutive failures opens the breaker; BreakerOpenFor
	// is how long it stays open, carried as the key's own TTL so recovery
	// needs no scheduler.
	BreakerThreshold int
	BreakerOpenFor   time.Duration
}

// Payment holds the window and attempt rules.
type Payment struct {
	// TTL is the payment window, sent to Midtrans as custom_expiry so the
	// gateway's deadline and ours end on the same second (BR-05). Without
	// custom_expiry a VA defaults to 24 HOURS and stays payable long after
	// the seats were resold.
	TTL time.Duration

	// MinRetryRemaining is the floor below which a method switch is refused.
	// Handing out a new instrument with seconds left only moves the failure
	// somewhere more painful (api-contracts §5.3).
	MinRetryRemaining time.Duration

	// MaxAttempts caps instruments per booking. Each attempt mints a new VA
	// or QR at Midtrans.
	MaxAttempts int

	// SettledTombstoneTTL is how long settled:{uuid} answers §3.3 after the
	// queue credential is deleted.
	SettledTombstoneTTL time.Duration

	// IconBase prefixes the method and bank icon URLs in §5.1.
	IconBase string
}

// IsLocal reports whether this is a local development run.
func (a App) IsLocal() bool { return a.Env == "local" }
