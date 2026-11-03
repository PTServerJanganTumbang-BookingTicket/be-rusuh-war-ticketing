package dukcapil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubAdapter_ValidNIK(t *testing.T) {
	res, err := NewStubAdapter().VerifyNIK(context.Background(), "3171000000000022")
	require.NoError(t, err)
	assert.True(t, res.Valid)
	assert.Equal(t, "ACTIVE", res.Status)
	assert.NotEmpty(t, res.Ref)
}

func TestStubAdapter_RejectsNIKEndingZeros(t *testing.T) {
	// The stub must be able to produce a rejection, or the fail-closed path is
	// unreachable without a real Dukcapil endpoint.
	res, err := NewStubAdapter().VerifyNIK(context.Background(), "3171000000000000")
	require.NoError(t, err)
	assert.False(t, res.Valid)
	assert.Equal(t, "NOT_FOUND", res.Status)
}

func TestStubAdapter_SimulatesOutage(t *testing.T) {
	_, err := NewStubAdapter().VerifyNIK(context.Background(), "3171000000009999")
	assert.Error(t, err)
}
