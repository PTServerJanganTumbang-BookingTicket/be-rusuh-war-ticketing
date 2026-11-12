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

	// JWTSecret verifies the account session on a grant that carries an
	// account_id (ADR-006 §8.17). The SAME secret user-service signs with:
	// payment-api only verifies, it never issues.
	//
	// Required, because the guard fails closed without it: an account grant
	// would answer 503 rather than skip the check, and skipping it is the hole
	// §8.17 closes.
	JWTSecret string

	// AppBase is the public web frontend origin (APP_BASE_URL), the same
	// variable notification builds emailed links on. The WebView payment page
	// lives there (ADR-007): payment_url = AppBase + PaymentWebPath + #code=.
	// No trailing slash.
	AppBase string

	// PaymentWebPath is the web payment route, with BookingIDPlaceholder where
	// the booking id goes. A variable so a web route change needs neither an
	// app release nor a backend release.
	PaymentWebPath string
}

// BookingIDPlaceholder marks where PaymentWebPath takes the booking id.
const BookingIDPlaceholder = "{booking_id}"

// PaymentPage is the WebView payment URL template: AppBase + PaymentWebPath,
// still holding BookingIDPlaceholder.
func (a App) PaymentPage() string { return a.AppBase + a.PaymentWebPath }

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

	// ServerKey authenticates charges: HTTP Basic on POST /v2/charge. It does
	// NOT verify notification signatures - see SignatureSecretKey.
	// Required, no default.
	ServerKey string

	// SignatureSecretKey verifies the signature_key on notifications:
	//
	//	SHA512(order_id + status_code + gross_amount + SignatureSecretKey)
	//
	// A SEPARATE credential from ServerKey since Midtrans split the two on
	// 28 October 2026. For a merchant that integrated before that date the
	// Signature Secret Key STARTS OUT equal to the default Server Key, so one
	// value in both slots appears to work - until either is regenerated, and
	// Server Keys now carry their own expiry dates. When they diverge the
	// charge still succeeds and the customer still pays, while every
	// notification is rejected as a forgery: money taken, seats swept.
	//
	// Required, no default, and deliberately not defaulted to ServerKey: a
	// fallback would reinstate exactly that silent failure.
	SignatureSecretKey string

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
	// SwitchCacheTTL is how long the operator kill switch
	// (payment_method_switches, migration 0014) is cached in process.
	//
	// It is the delay between an operator inserting a row and customers
	// seeing it. Seconds, deliberately: the switch replaces a deploy, and
	// reading the table on every §5.1 would add a query to the hottest path
	// in the service for state that changes a handful of times a year. Zero
	// disables the cache, which is what a debugging session wants.
	SwitchCacheTTL time.Duration
}

// IsLocal reports whether this is a local development run.
func (a App) IsLocal() bool { return a.Env == "local" }
