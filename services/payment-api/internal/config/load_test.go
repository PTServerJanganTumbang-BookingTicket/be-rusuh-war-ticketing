package config

import (
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

func TestLoad_Defaults(t *testing.T) {
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

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 9999, cfg.App.Port)
	assert.Equal(t, "production", cfg.App.Env)
	assert.Equal(t, "https://api.midtrans.com", cfg.Midtrans.BaseURL)
	assert.Equal(t, "redis.internal:6379", cfg.Redis.Addr)
	assert.Equal(t, 3, cfg.Redis.DB)
	assert.Equal(t, int32(8), cfg.DB.MaxConns)
	assert.Equal(t, "https://cdn.example/i", cfg.Payment.IconBase)
}
