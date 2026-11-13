package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setRequired sets only the variables that have no default, so each test can
// remove exactly one and watch the loader refuse to start.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("MIDTRANS_SERVER_KEY", "SB-Mid-server-test")
	// A DIFFERENT value from the server key on purpose: the two are separate
	// credentials, and identical fixtures would hide a slot mix-up.
	t.Setenv("MIDTRANS_SIGNATURE_SECRET_KEY", "SB-Mid-signature-test")
	t.Setenv("DEVICE_PEPPER", "pepper")
	t.Setenv("JWT_SECRET", "jwt-secret")
}

// defaulted lists every variable Load reads with a default. TestLoad_Defaults clears them, so a value from the
// developer's shell or .env (REDIS_ADDR pointing at a local Redis, say) cannot leak into the defaults it checks.
var defaulted = []string{
	"APP_BASE_URL", "APP_ENV", "APP_PORT", "DATABASE_CONN_LIFETIME", "DATABASE_MAX_CONNS", "LOG_FORMAT", "LOG_LEVEL",
	"MAX_PAYMENT_ATTEMPTS", "MIDTRANS_BASE_URL", "MIDTRANS_TIMEOUT_MS", "MIN_RETRY_REMAINING_SECONDS",
	"PAYMENT_BREAKER_FAILURE_THRESHOLD", "PAYMENT_BREAKER_OPEN_SECONDS", "PAYMENT_ICON_BASE", "PAYMENT_WEB_PATH",
	"PAYMENT_SWITCH_CACHE_SECONDS", "PAYMENT_TTL_SECONDS", "REDIS_ADDR", "REDIS_DB", "SETTLED_TOMBSTONE_SECONDS",
}

func clearDefaulted(t *testing.T) {
	t.Helper()
	for _, k := range defaulted {
		t.Setenv(k, "") // restores the original value when the test ends
		require.NoError(t, os.Unsetenv(k))
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearDefaulted(t)
	setRequired(t)

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 8085, cfg.App.Port)
	assert.Equal(t, "local", cfg.App.Env)
	assert.Equal(t, int32(4), cfg.DB.MaxConns)
	assert.Equal(t, 30*time.Minute, cfg.DB.ConnLifetime)
	assert.Equal(t, "localhost:6379", cfg.Redis.Addr)

	// Sandbox by default. A deployment that forgets this should charge nobody
	// rather than charge everybody.
	assert.Equal(t, "https://api.sandbox.midtrans.com", cfg.Midtrans.BaseURL)
	assert.Equal(t, 10*time.Second, cfg.Midtrans.Timeout)
	assert.Equal(t, 5, cfg.Midtrans.BreakerThreshold)
	// Loaded into separate fields, not aliased onto one another.
	assert.Equal(t, "SB-Mid-server-test", cfg.Midtrans.ServerKey)
	assert.Equal(t, "SB-Mid-signature-test", cfg.Midtrans.SignatureSecretKey)
	assert.Equal(t, 30*time.Second, cfg.Midtrans.BreakerOpenFor)

	assert.Equal(t, 10*time.Minute, cfg.Payment.TTL)
	assert.Equal(t, 60*time.Second, cfg.Payment.MinRetryRemaining)
	assert.Equal(t, 5, cfg.Payment.MaxAttempts)
	assert.Equal(t, time.Hour, cfg.Payment.SettledTombstoneTTL)
	assert.NotEmpty(t, cfg.Payment.IconBase)

	assert.Equal(t, "http://localhost:3000/m/orders/{booking_id}/pay", cfg.App.PaymentPage())
}

// Without the server key no charge can be authenticated, so no customer ever
// receives an instrument to pay.
func TestLoad_MidtransServerKeyRequired(t *testing.T) {
	setRequired(t)
	t.Setenv("MIDTRANS_SERVER_KEY", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIDTRANS_SERVER_KEY")
}

// Without the signature secret key the service starts, reports healthy, issues
// virtual accounts and takes the money - then rejects every callback as a
// forgery, confirming no booking and letting the sweep resell the seats. The
// symptom is indistinguishable from Midtrans never calling at all, so refusing
// to boot is the only safe behaviour.
func TestLoad_MidtransSignatureSecretKeyRequired(t *testing.T) {
	setRequired(t)
	t.Setenv("MIDTRANS_SIGNATURE_SECRET_KEY", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIDTRANS_SIGNATURE_SECRET_KEY")
}

// The server key must NOT stand in for a missing signature secret key. Midtrans
// split the two on 28 October 2026, and for an older merchant both dashboard
// values start out identical - so a fallback would look correct in every test
// and in sandbox, then fail silently in production the day either key is
// regenerated. Server keys now carry expiry dates, so that day comes.
func TestLoad_SignatureSecretKeyDoesNotFallBackToServerKey(t *testing.T) {
	setRequired(t)
	t.Setenv("MIDTRANS_SIGNATURE_SECRET_KEY", "")

	_, err := Load()
	require.Error(t, err, "an empty signature secret key must not be filled in from the server key")
}

// Without the pepper every guest grant fails its device check, while the
// service still looks healthy.
func TestLoad_DevicePepperRequired(t *testing.T) {
	setRequired(t)
	t.Setenv("DEVICE_PEPPER", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEVICE_PEPPER")
}

// ADR-006 §8.17: the guard verifies the account session behind an account
// grant, and it cannot do that without the signing key. A default would be a
// forged-session generator, and an empty one makes every logged-in payment
// 503 at runtime instead of failing at boot.
func TestLoad_JWTSecretRequired(t *testing.T) {
	setRequired(t)
	t.Setenv("JWT_SECRET", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")
}

func TestLoad_DatabaseURLRequired(t *testing.T) {
	setRequired(t)
	t.Setenv("DATABASE_URL", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL")
}

// A retry floor at or above the whole window refuses every method switch,
// which reads as a broken endpoint rather than a policy. Caught at startup
// rather than discovered by the first customer who tries.
func TestLoad_RetryFloorMustBeBelowTheWindow(t *testing.T) {
	cases := map[string]string{
		"equal":  "600",
		"above":  "900",
		"absurd": "86400",
	}
	for name, floor := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("PAYMENT_TTL_SECONDS", "600")
			t.Setenv("MIN_RETRY_REMAINING_SECONDS", floor)

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "MIN_RETRY_REMAINING_SECONDS")
		})
	}
}

// Any window the template allows must load, whole minutes or not. custom_expiry
// is sent with unit "second", so there is no reason to restrict the value -
// and an earlier version of this file did restrict it, on the mistaken belief
// that expiry_duration could only be expressed in minutes. Midtrans accepts
// second, minute, hour or day.
func TestLoad_SubMinuteWindowsAreAccepted(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want time.Duration
	}{
		{"61", 61 * time.Second},
		{"90", 90 * time.Second},
		{"450", 450 * time.Second},
		{"599", 599 * time.Second},
		{"3599", 3599 * time.Second},
	} {
		t.Run(tc.env, func(t *testing.T) {
			setRequired(t)
			t.Setenv("PAYMENT_TTL_SECONDS", tc.env)
			t.Setenv("MIN_RETRY_REMAINING_SECONDS", "30")

			cfg, err := Load()
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Payment.TTL)
		})
	}
}

func TestLoad_PaymentTTLMustBePositive(t *testing.T) {
	setRequired(t)
	t.Setenv("PAYMENT_TTL_SECONDS", "0")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PAYMENT_TTL_SECONDS")
}

func TestLoad_MaxAttemptsMustAllowOneCharge(t *testing.T) {
	setRequired(t)
	t.Setenv("MAX_PAYMENT_ATTEMPTS", "0")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAX_PAYMENT_ATTEMPTS")
}

// _SECONDS and _MS keys are read as their named unit, so the deployed values
// stay human-readable in the SAM template.
func TestLoad_SecondAndMillisecondSuffixesAreHonoured(t *testing.T) {
	setRequired(t)
	t.Setenv("PAYMENT_TTL_SECONDS", "300")
	t.Setenv("MIN_RETRY_REMAINING_SECONDS", "30")
	t.Setenv("SETTLED_TOMBSTONE_SECONDS", "120")
	t.Setenv("PAYMENT_BREAKER_OPEN_SECONDS", "45")
	t.Setenv("MIDTRANS_TIMEOUT_MS", "2500")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 5*time.Minute, cfg.Payment.TTL)
	assert.Equal(t, 30*time.Second, cfg.Payment.MinRetryRemaining)
	assert.Equal(t, 2*time.Minute, cfg.Payment.SettledTombstoneTTL)
	assert.Equal(t, 45*time.Second, cfg.Midtrans.BreakerOpenFor)
	assert.Equal(t, 2500*time.Millisecond, cfg.Midtrans.Timeout)
}

func TestLoad_OverridesAreRead(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_PORT", "9999")
	t.Setenv("APP_ENV", "production")
	t.Setenv("MIDTRANS_BASE_URL", "https://api.midtrans.com")
	t.Setenv("REDIS_ADDR", "redis.internal:6379")
	t.Setenv("REDIS_DB", "3")
	t.Setenv("DATABASE_MAX_CONNS", "8")
	t.Setenv("PAYMENT_ICON_BASE", "https://cdn.example/i")
	t.Setenv("APP_BASE_URL", "https://rush-my-ticket.my.id")
	// RFC 5737 documentation IPs — do NOT use in production
	t.Setenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST", "192.0.2.1,198.51.100.0/24")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 9999, cfg.App.Port)
	assert.Equal(t, "production", cfg.App.Env)
	assert.Equal(t, "https://api.midtrans.com", cfg.Midtrans.BaseURL)
	assert.Equal(t, "redis.internal:6379", cfg.Redis.Addr)
	assert.Equal(t, 3, cfg.Redis.DB)
	assert.Equal(t, int32(8), cfg.DB.MaxConns)
	assert.Equal(t, "https://cdn.example/i", cfg.Payment.IconBase)
	assert.Equal(t, "https://rush-my-ticket.my.id", cfg.App.AppBase)
}

func TestLoad_PaymentPageIsTheAppBaseURLPlusTheConfiguredPath(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_BASE_URL", "https://rush-my-ticket.my.id/")
	t.Setenv("PAYMENT_WEB_PATH", "/pay/{booking_id}")
	// RFC 5737 documentation IP — do NOT use in production
	t.Setenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST", "192.0.2.1")

	cfg, err := Load()
	require.NoError(t, err)
	// One trailing slash is dropped, so the URL has no "//".
	assert.Equal(t, "https://rush-my-ticket.my.id/pay/{booking_id}", cfg.App.PaymentPage())
}

// payment_url carries a handoff code; outside local it must not travel over plain HTTP.
func TestLoad_AppBaseURLMustBeHTTPSOutsideLocal(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_BASE_URL", "http://rush-my-ticket.my.id")
	// RFC 5737 documentation IP — do NOT use in production
	t.Setenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST", "192.0.2.1")
	_, err := Load()
	require.ErrorContains(t, err, "APP_BASE_URL must be https")
}

// Unset in production falls back to the localhost default, which is http and so refused: a forgotten variable
// fails the deploy instead of sending every app to localhost.
func TestLoad_AnUnsetAppBaseURLIsRefusedOutsideLocal(t *testing.T) {
	clearDefaulted(t)
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	// RFC 5737 documentation IP — do NOT use in production
	t.Setenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST", "192.0.2.1")
	_, err := Load()
	require.ErrorContains(t, err, "APP_BASE_URL must be https")
}

func TestLoad_AppBaseURLMustBeAnAbsoluteURLWithoutQueryOrFragment(t *testing.T) {
	for _, v := range []string{"rush-my-ticket.my.id", "ftp://rush-my-ticket.my.id", "https://x.id?a=1", "https://x.id#f"} {
		t.Run(v, func(t *testing.T) {
			setRequired(t)
			t.Setenv("APP_BASE_URL", v)
			_, err := Load()
			require.ErrorContains(t, err, "APP_BASE_URL")
		})
	}
}

func TestLoad_PaymentWebPathMustHoldTheBookingIDOnceAndNothingAfterThePath(t *testing.T) {
	for _, v := range []string{
		"m/orders/{booking_id}/pay",               // no leading slash
		"/m/orders/pay",                           // no placeholder
		"/m/{booking_id}/orders/{booking_id}/pay", // twice
		"/m/orders/{booking_id}/pay?x=1",          // query
		"/m/orders/{booking_id}/pay#x",            // fragment: the code goes there
	} {
		t.Run(v, func(t *testing.T) {
			setRequired(t)
			t.Setenv("PAYMENT_WEB_PATH", v)
			_, err := Load()
			require.ErrorContains(t, err, "PAYMENT_WEB_PATH")
		})
	}
}

// ── Webhook IP allowlist tests ────────────────────────────────────────────────
// Test IPs below are from RFC 5737 documentation ranges (192.0.2.0/24,
// 198.51.100.0/24, 203.0.113.0/24). Do NOT use these in production allowlists;
// they are reserved for documentation examples.

func TestParseIPAllowlist_Empty(t *testing.T) {
	result, err := ParseIPAllowlist("")
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestParseIPAllowlist_SingleIP(t *testing.T) {
	result, err := ParseIPAllowlist("192.0.2.1")
	require.NoError(t, err)
	require.Len(t, result, 1)
	// Single IP becomes /32
	assert.Equal(t, "192.0.2.1/32", result[0].String())
}

func TestParseIPAllowlist_SingleCIDR(t *testing.T) {
	result, err := ParseIPAllowlist("198.51.100.0/24")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "198.51.100.0/24", result[0].String())
}

func TestParseIPAllowlist_MultipleEntries(t *testing.T) {
	result, err := ParseIPAllowlist("192.0.2.1,198.51.100.0/24,203.0.113.5")
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.Equal(t, "192.0.2.1/32", result[0].String())
	assert.Equal(t, "198.51.100.0/24", result[1].String())
	assert.Equal(t, "203.0.113.5/32", result[2].String())
}

func TestParseIPAllowlist_WhitespaceHandling(t *testing.T) {
	result, err := ParseIPAllowlist("  192.0.2.1 , 198.51.100.0/24  ,203.0.113.5  ")
	require.NoError(t, err)
	require.Len(t, result, 3)
}

// F8: strings.TrimSpace handles \n, \r, \v, \f — not just space and tab.
// A trailing newline from a console paste or file must not break startup.
func TestParseIPAllowlist_TrimsNewlinesAndCarriageReturns(t *testing.T) {
	// Trailing newline
	result, err := ParseIPAllowlist("192.0.2.1\n")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "192.0.2.1/32", result[0].String())

	// Trailing carriage return
	result, err = ParseIPAllowlist("192.0.2.1\r")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "192.0.2.1/32", result[0].String())

	// CRLF
	result, err = ParseIPAllowlist("192.0.2.1\r\n")
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Between entries
	result, err = ParseIPAllowlist("192.0.2.1\n,\n198.51.100.0/24\n")
	require.NoError(t, err)
	require.Len(t, result, 2)
}

// F8: A value that is only whitespace must behave as empty, not parse error.
func TestParseIPAllowlist_WhitespaceOnlyTreatedAsEmpty(t *testing.T) {
	for _, ws := range []string{" ", "\t", "\n", "\r\n", "  \t  \n"} {
		result, err := ParseIPAllowlist(ws)
		require.NoError(t, err, "whitespace-only %q should not error", ws)
		assert.Empty(t, result, "whitespace-only %q should produce empty result", ws)
	}
}

func TestParseIPAllowlist_InvalidIPReturnsError(t *testing.T) {
	_, err := ParseIPAllowlist("not-an-ip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid IP or CIDR")
}

func TestParseIPAllowlist_InvalidCIDRReturnsError(t *testing.T) {
	_, err := ParseIPAllowlist("192.168.1.0/33") // Invalid CIDR mask
	require.Error(t, err)
}

func TestParseIPAllowlist_IPv6Supported(t *testing.T) {
	result, err := ParseIPAllowlist("::1,2001:db8::/32")
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "::1/128", result[0].String())
	assert.Equal(t, "2001:db8::/32", result[1].String())
}

// Outside local environment, an empty allowlist must be rejected.
// This ensures fail-closed behavior in production.
func TestLoad_WebhookIPAllowlistRequiredOutsideLocal(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_BASE_URL", "https://rush-my-ticket.my.id")
	t.Setenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIDTRANS_WEBHOOK_IP_ALLOWLIST")
}

// In local environment, an empty allowlist is allowed (for dev convenience).
func TestLoad_WebhookIPAllowlistOptionalInLocal(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "local")
	t.Setenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST", "")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.Midtrans.WebhookIPAllowlist)
}

// Invalid IP in the allowlist should fail at startup, not at runtime.
func TestLoad_InvalidWebhookIPAllowlistFailsAtStartup(t *testing.T) {
	setRequired(t)
	t.Setenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST", "valid.ip.1.1,not-an-ip,8.8.8.8")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MIDTRANS_WEBHOOK_IP_ALLOWLIST")
}

// Test the full Midtrans sandbox IP list from the task description.
func TestLoad_IPAllowlistMultipleEntries(t *testing.T) {
	setRequired(t)
	// Test fixtures using RFC 5737 documentation IPs. Do NOT use in production.
	ips := "192.0.2.1,198.51.100.5,203.0.113.10"
	t.Setenv("MIDTRANS_WEBHOOK_IP_ALLOWLIST", ips)

	cfg, err := Load()
	require.NoError(t, err)
	require.Len(t, cfg.Midtrans.WebhookIPAllowlist, 3)

	// All should be /32 (single IPs)
	for _, p := range cfg.Midtrans.WebhookIPAllowlist {
		assert.Equal(t, 32, p.Bits())
	}
}
