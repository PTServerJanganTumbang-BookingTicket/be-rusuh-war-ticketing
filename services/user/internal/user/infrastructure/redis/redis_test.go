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
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	return client, mr
}

// ── NewClient ─────────────────────────────────────────────────────────────────
// NewClient is tested in pkg/redisutil.

// ── RateLimiter ───────────────────────────────────────────────────────────────

func TestRateLimiter_Allow_UnderLimit(t *testing.T) {
	client, _ := newTestClient(t)
	limiter := NewRateLimiter(client)
	allowed, retryAfter, err := limiter.Allow(context.Background(), "test-key", 5, time.Second)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Zero(t, retryAfter)
}

func TestRateLimiter_Allow_AtLimit(t *testing.T) {
	client, _ := newTestClient(t)
	limiter := NewRateLimiter(client)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		allowed, _, err := limiter.Allow(ctx, "key", 3, time.Minute)
		require.NoError(t, err)
		assert.True(t, allowed)
	}
	allowed, retryAfter, err := limiter.Allow(ctx, "key", 3, time.Minute)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Positive(t, retryAfter)
}

func TestRateLimiter_Allow_WindowReset(t *testing.T) {
	client, mr := newTestClient(t)
	limiter := NewRateLimiter(client)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, _, _ = limiter.Allow(ctx, "key-reset", 2, 2*time.Second)
	}
	// exhaust
	allowed, _, _ := limiter.Allow(ctx, "key-reset", 2, 2*time.Second)
	assert.False(t, allowed)

	// fast-forward past the window
	mr.FastForward(3 * time.Second)

	allowed, _, err := limiter.Allow(ctx, "key-reset", 2, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, allowed)
}
