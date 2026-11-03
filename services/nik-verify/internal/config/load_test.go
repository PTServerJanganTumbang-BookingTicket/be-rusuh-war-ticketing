package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setRequired sets the minimum env for a valid stub configuration.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("NIK_PEPPER", "test-pepper")
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("DUKCAPIL_MODE", "stub")
	t.Setenv("APP_ENV", "local")
}

func TestLoad_Defaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8082, cfg.App.Port)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, float64(40), cfg.Dukcapil.RateLimitRPS)
	assert.Equal(t, 3*time.Second, cfg.Dukcapil.Timeout)
	assert.True(t, cfg.Dukcapil.IsStub())
}

func TestLoad_RequiresPepper(t *testing.T) {
	// No default, ever. The pepper cannot be rotated, so a placeholder that
	// reaches a deployed environment is permanent.
	setRequired(t)
	t.Setenv("NIK_PEPPER", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NIK_PEPPER")
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	setRequired(t)
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	assert.Error(t, err)
}

func TestLoad_LiveModeRequiresUpstreamSettings(t *testing.T) {
	setRequired(t)
	t.Setenv("DUKCAPIL_MODE", "live")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DUKCAPIL_BASE_URL")

	t.Setenv("DUKCAPIL_BASE_URL", "https://dukcapil.example.gov.id")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DUKCAPIL_API_KEY")

	t.Setenv("DUKCAPIL_API_KEY", "key")
	_, err = Load()
	assert.NoError(t, err)
}

func TestLoad_StubIsRefusedInProduction(t *testing.T) {
	// Otherwise a forgotten env var means every NIK verifies.
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stub")
}

func TestLoad_RejectsOutOfRangePort(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_PORT", "70000")
	_, err := Load()
	assert.Error(t, err)
}

func TestLoad_RejectsNonPositiveRate(t *testing.T) {
	setRequired(t)
	t.Setenv("DUKCAPIL_RATE_LIMIT_RPS", "0")
	_, err := Load()
	assert.Error(t, err)
}

func TestGetEnvDuration_UnitSuffixes(t *testing.T) {
	t.Setenv("A_MS", "250")
	assert.Equal(t, 250*time.Millisecond, getEnvDuration("A_MS", time.Second))

	t.Setenv("B_SECONDS", "45")
	assert.Equal(t, 45*time.Second, getEnvDuration("B_SECONDS", time.Second))

	t.Setenv("C", "2m")
	assert.Equal(t, 2*time.Minute, getEnvDuration("C", time.Second))

	assert.Equal(t, time.Second, getEnvDuration("UNSET_KEY", time.Second))
}

func TestGetEnvHelpers(t *testing.T) {
	assert.Equal(t, "fallback", getEnv("UNSET_KEY", "fallback"))
	assert.Equal(t, 7, getEnvInt("UNSET_KEY", 7))
	assert.Equal(t, 1.5, getEnvFloat("UNSET_KEY", 1.5))

	t.Setenv("BAD_INT", "abc")
	assert.Equal(t, 7, getEnvInt("BAD_INT", 7))
	t.Setenv("BAD_FLOAT", "abc")
	assert.Equal(t, 1.5, getEnvFloat("BAD_FLOAT", 1.5))
}
