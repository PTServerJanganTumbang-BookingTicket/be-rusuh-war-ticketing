package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
)

// Load reads configuration from environment variables and returns a validated
// Config. Call once at startup, after dotenv.LoadEnv().
func Load() (Config, error) {
	cfg := Config{
		App: App{
			Env:          envutil.Env("APP_ENV", "local"),
			Port:         envutil.EnvInt("APP_PORT", 8085),
			DevicePepper: os.Getenv("DEVICE_PEPPER"),
			JWTSecret:    os.Getenv("JWT_SECRET"),
			// One trailing slash dropped, so "https://x/" + "/m/..." is not "//m/...".
			AppBase:        strings.TrimSuffix(envutil.Env("APP_BASE_URL", "http://localhost:3000"), "/"),
			PaymentWebPath: envutil.Env("PAYMENT_WEB_PATH", "/m/orders/"+BookingIDPlaceholder+"/pay"),
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
		Midtrans: Midtrans{
			// Sandbox by default. A deployment that forgot to set this should
			// charge nobody rather than charge everybody.
			BaseURL:            envutil.Env("MIDTRANS_BASE_URL", "https://api.sandbox.midtrans.com"),
			ServerKey:          os.Getenv("MIDTRANS_SERVER_KEY"),
			SignatureSecretKey: os.Getenv("MIDTRANS_SIGNATURE_SECRET_KEY"),
			Timeout:            envutil.EnvDuration("MIDTRANS_TIMEOUT_MS", 10*time.Second),
			BreakerThreshold:   envutil.EnvInt("PAYMENT_BREAKER_FAILURE_THRESHOLD", 5),
			BreakerOpenFor:     envutil.EnvDuration("PAYMENT_BREAKER_OPEN_SECONDS", 30*time.Second),
			WebhookIPAllowlist: parseIPAllowlist(os.Getenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST")),
		},
		Payment: Payment{
			TTL:                 envutil.EnvDuration("PAYMENT_TTL_SECONDS", 600*time.Second),
			MinRetryRemaining:   envutil.EnvDuration("MIN_RETRY_REMAINING_SECONDS", 60*time.Second),
			MaxAttempts:         envutil.EnvInt("MAX_PAYMENT_ATTEMPTS", 5),
			SettledTombstoneTTL: envutil.EnvDuration("SETTLED_TOMBSTONE_SECONDS", 3600*time.Second),
			IconBase:            envutil.Env("PAYMENT_ICON_BASE", "https://cdn.rusuh.id/icons"),
			SwitchCacheTTL: time.Duration(
				envutil.EnvInt("PAYMENT_SWITCH_CACHE_SECONDS", 10)) * time.Second,
		},
		Sweep: Sweep{
			GraceSeconds: envutil.EnvInt("SWEEP_GRACE_SECONDS", 120),
			BatchSize:    envutil.EnvInt("SWEEP_BATCH_SIZE", 100),
			StuckMinutes: envutil.EnvInt("SWEEP_STUCK_MINUTES", 30),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate refuses to start on a configuration that would be unsafe rather
// than merely wrong. Every check here is a condition whose symptom would
// otherwise appear in production as a customer-visible payment failure.
func (c Config) validate() error {
	if c.DB.DSN == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.Midtrans.ServerKey == "" {
		// Authenticates the charge. Missing, no instrument is ever issued.
		return fmt.Errorf("MIDTRANS_SERVER_KEY is required and has no default: without it no charge can be authenticated")
	}
	if c.Midtrans.SignatureSecretKey == "" {
		// Verifies every notification. Missing, the service starts, reports
		// healthy, issues virtual accounts, takes the customer's money - and
		// rejects every callback as a forgery, confirming no booking ever.
		// The symptom is indistinguishable from Midtrans never calling.
		return fmt.Errorf("MIDTRANS_SIGNATURE_SECRET_KEY is required and has no default: without it every notification fails signature verification")
	}
	if c.App.JWTSecret == "" {
		// Without it every logged-in customer gets 503 at the payment step,
		// because the guard refuses to skip the §8.17 session check.
		return fmt.Errorf("JWT_SECRET is required and has no default: without it no account grant can be verified")
	}
	if c.App.DevicePepper == "" {
		return fmt.Errorf("DEVICE_PEPPER is required and has no default: without it every guest grant fails the device check")
	}
	if err := c.App.validatePaymentPage(); err != nil {
		return err
	}
	if c.Payment.TTL <= 0 {
		return fmt.Errorf("PAYMENT_TTL_SECONDS must be positive")
	}
	if c.Payment.MaxAttempts < 1 {
		return fmt.Errorf("MAX_PAYMENT_ATTEMPTS must be at least 1")
	}
	// No whole-minute restriction on the window: custom_expiry is sent with
	// unit "second", so any value the template allows is expressed to the
	// gateway exactly.
	if c.Payment.MinRetryRemaining >= c.Payment.TTL {
		// A floor at or above the whole window refuses every switch, which
		// looks like a broken endpoint rather than a policy.
		return fmt.Errorf("MIN_RETRY_REMAINING_SECONDS (%s) must be below PAYMENT_TTL_SECONDS (%s)",
			c.Payment.MinRetryRemaining, c.Payment.TTL)
	}
	// Sweep config. §4.1 of the sweep plan: a grace of zero silently ignores
	// the race between a payment landing and the sweep running.
	if c.Sweep.GraceSeconds < 1 {
		return fmt.Errorf("SWEEP_GRACE_SECONDS must be at least 1 to avoid racing with in-flight webhooks")
	}
	if c.Sweep.BatchSize < 1 {
		return fmt.Errorf("SWEEP_BATCH_SIZE must be at least 1")
	}
	// F1: SettledTombstoneTTL must be positive. A zero here recreates the bug
	// where settled:{queue_token} keys become permanent in Redis.
	if c.Payment.SettledTombstoneTTL <= 0 {
		return fmt.Errorf("SETTLED_TOMBSTONE_SECONDS must be positive: a zero TTL makes Redis keys permanent")
	}
	return nil
}

// validatePaymentPage checks the two halves of payment_url (§5.6). The app
// refuses any URL outside its allowed prefix, so a wrong value here shows as
// "the pay button does nothing" on mobile, not as an error anywhere.
func (a App) validatePaymentPage() error {
	u, err := url.Parse(a.AppBase)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("APP_BASE_URL must be an absolute http(s) URL, got %q", a.AppBase)
	}
	// The URL carries a single-use payment code in its fragment.
	if !a.IsLocal() && u.Scheme != "https" {
		return fmt.Errorf("APP_BASE_URL must be https outside local: payment_url carries a handoff code")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("APP_BASE_URL must not carry a query or fragment, got %q", a.AppBase)
	}
	p := a.PaymentWebPath
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "?#") ||
		strings.Count(p, BookingIDPlaceholder) != 1 {
		return fmt.Errorf("PAYMENT_WEB_PATH must start with /, contain %s once and no ? or #, got %q",
			BookingIDPlaceholder, p)
	}
	return nil
}

// parseIPAllowlist splits a comma-separated list of IPs into a slice, trimming
// whitespace from each entry and dropping empty strings.
func parseIPAllowlist(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
