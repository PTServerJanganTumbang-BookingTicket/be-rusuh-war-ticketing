package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setRequired sets the minimum env for a valid local configuration.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("NIK_VERIFY_BASE_URL", "http://localhost:8082")
	t.Setenv("JWT_SECRET", "test-signing-secret")
}

func TestLoad_Defaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8081, cfg.App.Port)
	assert.Equal(t, "info", cfg.Log.Level)
	// Derived from the sale window, not from the journey: the session begins at
	// login, before the queue (ADR-005 §7). 21600 + 990 + 150 = 22740.
	assert.Equal(t, 21600*time.Second, cfg.Auth.SaleWindow)
	assert.Equal(t, 22740*time.Second, cfg.Auth.AccessTokenTTL,
		"the session must outlive the queue wait, not just the 930s journey")
	assert.False(t, cfg.Auth.SecureCookies, "local serves plain HTTP, which drops Secure cookies")
	assert.True(t, cfg.App.IsLocal())
}

func TestLoad_RequiresJWTSecret(t *testing.T) {
	// No default: a known signing key forges sessions for every account.
	setRequired(t)
	t.Setenv("JWT_SECRET", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	setRequired(t)
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	assert.Error(t, err)
}

func TestLoad_RequiresNikVerifyBaseURL(t *testing.T) {
	// This service does not call Dukcapil directly; without nik-verify it
	// cannot verify anything and must not start.
	setRequired(t)
	t.Setenv("NIK_VERIFY_BASE_URL", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NIK_VERIFY_BASE_URL")
}

func TestLoad_NoNikPepperIsRead(t *testing.T) {
	// The pepper lives in nik-verify. If this service ever starts reading one,
	// the blast radius ADR-005 §8 describes has silently widened again.
	setRequired(t)
	t.Setenv("NIK_HMAC_KEY", "should-be-ignored")
	t.Setenv("NIK_PEPPER", "should-be-ignored")
	cfg, err := Load()
	require.NoError(t, err)
	assert.NotContains(t, cfg.App.BaseURL, "should-be-ignored")
}

func TestLoad_SecureCookiesDefaultOutsideLocal(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.Auth.SecureCookies)
}

func TestLoad_SecureCookiesCannotBeDisabledOutsideLocal(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SECURE_COOKIES", "false")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SECURE_COOKIES")
}

func TestLoad_RejectsOutOfRangePort(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_PORT", "70000")
	_, err := Load()
	assert.Error(t, err)
}

func TestLoad_RejectsNonPositiveTokenTTL(t *testing.T) {
	setRequired(t)
	t.Setenv("ACCESS_TOKEN_TTL_SECONDS", "0")
	_, err := Load()
	assert.Error(t, err)
}
