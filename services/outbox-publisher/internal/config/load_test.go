package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("NOTIFICATION_QUEUE_URL", "https://sqs.ap-southeast-1.amazonaws.com/1/notify")
}

func TestLoad_Defaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8083, cfg.App.Port)
	assert.Equal(t, time.Second, cfg.App.Interval, "ADR-005 §9 step 4: one pass per second")
	assert.Equal(t, 500, cfg.App.BatchSize, "ADR-005 §9 step 4: 500 rows per pass")
	assert.Equal(t, 30*time.Second, cfg.Queue.LeaseTTL)
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	setRequired(t)
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	assert.Error(t, err)
}

func TestLoad_RequiresQueueURL(t *testing.T) {
	// Starting without a destination would drain rows into nothing.
	setRequired(t)
	t.Setenv("NOTIFICATION_QUEUE_URL", "")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NOTIFICATION_QUEUE_URL")
}

func TestLoad_RejectsLeaseShorterThanInterval(t *testing.T) {
	// A lease shorter than a pass lets a second worker claim a row the first
	// is still publishing, which is the duplicate the guard exists to prevent.
	setRequired(t)
	t.Setenv("PUBLISH_INTERVAL_MS", "60000")
	t.Setenv("OUTBOX_LEASE_SECONDS", "30")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OUTBOX_LEASE_SECONDS")
}

func TestLoad_RejectsNonPositiveBatchAndInterval(t *testing.T) {
	setRequired(t)
	t.Setenv("PUBLISH_BATCH_SIZE", "0")
	_, err := Load()
	assert.Error(t, err)

	t.Setenv("PUBLISH_BATCH_SIZE", "500")
	t.Setenv("PUBLISH_INTERVAL_MS", "0")
	_, err = Load()
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
