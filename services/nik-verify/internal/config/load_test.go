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

// ── parseExtraHeaders ────────────────────────────────────────────────────

func TestParseExtraHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr bool
	}{
		{name: "absent", raw: "", want: nil},
		{name: "whitespace only", raw: "   ", want: nil},
		{name: "empty object", raw: "{}", want: nil},
		{
			name: "one header",
			raw:  `{"apidogToken":"SzO1NLPx"}`,
			want: map[string]string{"apidogToken": "SzO1NLPx"},
		},
		{
			name: "several headers",
			raw:  `{"apidogToken":"t","X-Trace-Id":"abc"}`,
			want: map[string]string{"apidogToken": "t", "X-Trace-Id": "abc"},
		},
		{name: "not JSON", raw: "apidogToken=t", wantErr: true},
		{name: "not an object", raw: `["apidogToken"]`, wantErr: true},
		{name: "non-string value", raw: `{"apidogToken":1}`, wantErr: true},
		{name: "empty header name", raw: `{"":"t"}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseExtraHeaders(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
