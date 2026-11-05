package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()}), mr
}

// ── NewClient ─────────────────────────────────────────────────────────────────
// NewClient is tested in pkg/redisutil.

// ── LeaseGuard ────────────────────────────────────────────────────────────────

func TestLeaseGuard_Claim_New(t *testing.T) {
	client, _ := newTestClient(t)
	guard := NewLeaseGuard(client, time.Second, time.Minute)
	claimed, err := guard.Claim(context.Background(), "row-1")
	require.NoError(t, err)
	assert.True(t, claimed)
}

func TestLeaseGuard_Claim_AlreadyHeld(t *testing.T) {
	client, _ := newTestClient(t)
	guard := NewLeaseGuard(client, time.Second, time.Minute)
	ctx := context.Background()
	first, _ := guard.Claim(ctx, "row-held")
	assert.True(t, first)
	second, err := guard.Claim(ctx, "row-held")
	require.NoError(t, err)
	assert.False(t, second)
}

func TestLeaseGuard_Complete_PreventsReclaim(t *testing.T) {
	client, _ := newTestClient(t)
	guard := NewLeaseGuard(client, time.Second, time.Minute)
	ctx := context.Background()
	_, _ = guard.Claim(ctx, "row-done")
	require.NoError(t, guard.Complete(ctx, "row-done"))
	// After Complete the key is "done", not worker-id, so SET NX returns false
	claimed, err := guard.Claim(ctx, "row-done")
	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestLeaseGuard_Release_AllowsReclaim(t *testing.T) {
	client, _ := newTestClient(t)
	guard := NewLeaseGuard(client, time.Second, time.Minute)
	ctx := context.Background()
	_, _ = guard.Claim(ctx, "row-release")
	require.NoError(t, guard.Release(ctx, "row-release"))
	claimed, err := guard.Claim(ctx, "row-release")
	require.NoError(t, err)
	assert.True(t, claimed)
}

func TestLeaseGuard_Claim_ExpiresAfterTTL(t *testing.T) {
	client, mr := newTestClient(t)
	guard := NewLeaseGuard(client, 2*time.Second, time.Minute)
	ctx := context.Background()
	first, _ := guard.Claim(ctx, "row-expire")
	assert.True(t, first)
	mr.FastForward(3 * time.Second)
	reclaim, err := guard.Claim(ctx, "row-expire")
	require.NoError(t, err)
	assert.True(t, reclaim)
}

// ── NewLeaseGuard default TTL guards ─────────────────────────────────────────

func TestNewLeaseGuard_DefaultTTL(t *testing.T) {
	client, _ := newTestClient(t)
	// ttl=0 and doneTTL=0 → should both fall back to entity defaults
	guard := NewLeaseGuard(client, 0, 0)
	claimed, err := guard.Claim(context.Background(), "row-default")
	require.NoError(t, err)
	assert.True(t, claimed)
}
