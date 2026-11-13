package config

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// Config is the root configuration for payment-api.
type Config struct {
	App      App
	Log      Log
	DB       DB
	Redis    Redis
	Midtrans Midtrans
	Payment  Payment
	Sweep    Sweep
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

	// WebhookIPAllowlist is the set of CIDR blocks allowed to call
	// POST /payments/webhook. Defense-in-depth alongside signature
	// verification: the signature proves authenticity, the allowlist limits
	// who can even attempt a guess.
	//
	// Comma-separated in the environment variable, supports both single IPs
	// (widened to /32) and CIDR ranges. Midtrans publishes their
	// IPs at https://docs.midtrans.com/docs/ip-address — sandbox and
	// production differ.
	//
	// An empty list is accepted for local development but REJECTED outside
	// local: failing closed means a misconfigured deployment blocks all
	// webhooks rather than accepting them from anywhere.
	WebhookIPAllowlist []netip.Prefix
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

// Sweep holds the backstop sweep parameters.
type Sweep struct {
	// GraceSeconds is the delay after payment_expires_at before a booking
	// becomes a sweep candidate. Non-zero to let in-flight webhooks land:
	// a booking swept one second after its deadline may race with a payment
	// that arrived on time. Defaults to 120s.
	GraceSeconds int

	// BatchSize caps how many candidates are processed per sweep invocation.
	// Each candidate costs a Midtrans Status call, so the batch bounds the
	// gateway load per tick. Defaults to 100.
	BatchSize int

	// StuckMinutes is the threshold for logging stuck AWAITING_PAYMENT
	// bookings — those that have been due longer than this but were skipped
	// because the gateway was unreachable. Defaults to 30.
	StuckMinutes int
}

// IsLocal reports whether this is a local development run.
func (a App) IsLocal() bool { return a.Env == "local" }

// ParseIPAllowlist parses a comma-separated list of IPs or CIDR blocks into
// a slice of netip.Prefix. Single IPs without a mask are treated as /32.
// Returns an error if any entry is invalid.
func ParseIPAllowlist(raw string) ([]netip.Prefix, error) {
	if raw == "" {
		return nil, nil
	}
	var result []netip.Prefix
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// Try parsing as CIDR first
		prefix, err := netip.ParsePrefix(entry)
		if err == nil {
			result = append(result, prefix)
			continue
		}
		// Fall back to single IP (add /32 for IPv4, /128 for IPv6)
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid IP or CIDR %q: %w", entry, err)
		}
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		prefix = netip.PrefixFrom(addr, bits)
		result = append(result, prefix)
	}
	return result, nil
}
