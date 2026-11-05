package envutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEnv_FallbackWhenUnset(t *testing.T) {
	assert.Equal(t, "fallback", Env("UNSET_ENVUTIL_KEY", "fallback"))
}

func TestEnv_ReadsValue(t *testing.T) {
	t.Setenv("ENVUTIL_STR", "hello")
	assert.Equal(t, "hello", Env("ENVUTIL_STR", "fallback"))
}

func TestEnvInt_FallbackWhenUnset(t *testing.T) {
	assert.Equal(t, 7, EnvInt("UNSET_ENVUTIL_INT", 7))
}

func TestEnvInt_FallbackOnBadValue(t *testing.T) {
	t.Setenv("ENVUTIL_BAD_INT", "abc")
	assert.Equal(t, 7, EnvInt("ENVUTIL_BAD_INT", 7))
}

func TestEnvInt_ReadsValue(t *testing.T) {
	t.Setenv("ENVUTIL_INT", "42")
	assert.Equal(t, 42, EnvInt("ENVUTIL_INT", 0))
}

func TestEnvBool_FallbackWhenUnset(t *testing.T) {
	assert.True(t, EnvBool("UNSET_ENVUTIL_BOOL", true))
}

func TestEnvBool_FallbackOnBadValue(t *testing.T) {
	t.Setenv("ENVUTIL_BAD_BOOL", "maybe")
	assert.True(t, EnvBool("ENVUTIL_BAD_BOOL", true))
}

func TestEnvBool_ReadsTrue(t *testing.T) {
	t.Setenv("ENVUTIL_BOOL_T", "true")
	assert.True(t, EnvBool("ENVUTIL_BOOL_T", false))
}

func TestEnvBool_ReadsFalse(t *testing.T) {
	t.Setenv("ENVUTIL_BOOL_F", "false")
	assert.False(t, EnvBool("ENVUTIL_BOOL_F", true))
}

func TestEnvFloat_FallbackWhenUnset(t *testing.T) {
	assert.Equal(t, 1.5, EnvFloat("UNSET_ENVUTIL_FLOAT", 1.5))
}

func TestEnvFloat_FallbackOnBadValue(t *testing.T) {
	t.Setenv("ENVUTIL_BAD_FLOAT", "abc")
	assert.Equal(t, 1.5, EnvFloat("ENVUTIL_BAD_FLOAT", 1.5))
}

func TestEnvFloat_ReadsValue(t *testing.T) {
	t.Setenv("ENVUTIL_FLOAT", "3.14")
	assert.InDelta(t, 3.14, EnvFloat("ENVUTIL_FLOAT", 0), 0.0001)
}

func TestEnvDuration_MS_Suffix(t *testing.T) {
	t.Setenv("ENVUTIL_A_MS", "250")
	assert.Equal(t, 250*time.Millisecond, EnvDuration("ENVUTIL_A_MS", time.Second))
}

func TestEnvDuration_SECONDS_Suffix(t *testing.T) {
	t.Setenv("ENVUTIL_B_SECONDS", "45")
	assert.Equal(t, 45*time.Second, EnvDuration("ENVUTIL_B_SECONDS", time.Second))
}

func TestEnvDuration_GoDurationString(t *testing.T) {
	t.Setenv("ENVUTIL_C", "2m")
	assert.Equal(t, 2*time.Minute, EnvDuration("ENVUTIL_C", time.Second))
}

func TestEnvDuration_FallbackWhenUnset(t *testing.T) {
	assert.Equal(t, time.Second, EnvDuration("UNSET_ENVUTIL_DUR", time.Second))
}

func TestEnvDuration_FallbackOnBadValue(t *testing.T) {
	t.Setenv("ENVUTIL_BAD_DUR", "notaduration")
	assert.Equal(t, time.Second, EnvDuration("ENVUTIL_BAD_DUR", time.Second))
}
