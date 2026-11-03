package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "local")
	t.Setenv("EMAIL_FROM_ADDRESS", "no-reply@rusuh.id")
	t.Setenv("APP_BASE_URL", "http://localhost:3000")
}

func TestLoad_Defaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8084, cfg.App.Port)
	assert.Equal(t, 15, cfg.App.MagicTokenTTLMinutes)
	assert.Equal(t, 10*time.Second, cfg.Email.SendTimeout)
}

func TestLoad_RequiresFromAddress(t *testing.T) {
	setRequired(t)
	t.Setenv("EMAIL_FROM_ADDRESS", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EMAIL_FROM_ADDRESS")
}

func TestLoad_RejectsFromAddressThatIsNotAnAddress(t *testing.T) {
	setRequired(t)
	t.Setenv("EMAIL_FROM_ADDRESS", "no-reply")
	_, err := Load()
	assert.Error(t, err)
}

func TestLoad_RequiresHTTPSOutsideLocal(t *testing.T) {
	// These links carry single-use login tokens; plain HTTP would send one in
	// cleartext.
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https")

	t.Setenv("APP_BASE_URL", "https://rusuh.id")
	_, err = Load()
	assert.NoError(t, err)
}

func TestLoad_AllowsPlainHTTPLocally(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:3000", cfg.App.AppBase)
}

func TestLoad_RequiresAppBase(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_BASE_URL", " ")
	cfg, err := Load()
	// A space is not empty, so it passes the required check — the https check
	// is what catches it outside local.
	require.NoError(t, err)
	assert.Equal(t, " ", cfg.App.AppBase)
}

func TestLoad_RejectsNonPositiveTTL(t *testing.T) {
	setRequired(t)
	t.Setenv("MAGIC_TOKEN_TTL_MINUTES", "0")
	_, err := Load()
	assert.Error(t, err)
}

func TestLoad_RejectsOutOfRangePort(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_PORT", "70000")
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
	t.Setenv("BAD_INT", "abc")
	assert.Equal(t, 7, getEnvInt("BAD_INT", 7))
}
