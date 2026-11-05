package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/entity"
)

func newTestClient(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()}), mr
}

// ── NewClient ─────────────────────────────────────────────────────────────────
// NewClient is tested in pkg/redisutil.

// ── CircuitBreaker ────────────────────────────────────────────────────────────

func TestCircuitBreaker_InitiallyClosed(t *testing.T) {
	client, _ := newTestClient(t)
	breaker := NewCircuitBreaker(client, 3, time.Minute)
	state, err := breaker.State(context.Background())
	require.NoError(t, err)
	assert.Equal(t, entity.BreakerClosed, state)
}

func TestCircuitBreaker_OpensAtThreshold(t *testing.T) {
	client, _ := newTestClient(t)
	breaker := NewCircuitBreaker(client, 3, time.Minute)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		require.NoError(t, breaker.RecordFailure(ctx))
	}
	state, err := breaker.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, entity.BreakerOpen, state)
}

func TestCircuitBreaker_RecordSuccess_Resets(t *testing.T) {
	client, _ := newTestClient(t)
	breaker := NewCircuitBreaker(client, 2, time.Minute)
	ctx := context.Background()
	require.NoError(t, breaker.RecordFailure(ctx))
	require.NoError(t, breaker.RecordFailure(ctx))
	require.NoError(t, breaker.RecordSuccess(ctx))
	state, err := breaker.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, entity.BreakerClosed, state)
}

func TestCircuitBreaker_OpenExpires(t *testing.T) {
	client, mr := newTestClient(t)
	breaker := NewCircuitBreaker(client, 1, 2*time.Second)
	ctx := context.Background()
	require.NoError(t, breaker.RecordFailure(ctx))
	state, _ := breaker.State(ctx)
	assert.Equal(t, entity.BreakerOpen, state)

	mr.FastForward(3 * time.Second)

	state, err := breaker.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, entity.BreakerClosed, state)
}

// ── VerificationCache ─────────────────────────────────────────────────────────

func TestVerificationCache_NotVerifiedInitially(t *testing.T) {
	client, _ := newTestClient(t)
	cache := NewVerificationCache(client)
	ok, err := cache.IsVerified(context.Background(), "hmac-1")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestVerificationCache_MarkAndCheck(t *testing.T) {
	client, _ := newTestClient(t)
	cache := NewVerificationCache(client)
	ctx := context.Background()
	require.NoError(t, cache.MarkVerified(ctx, "hmac-1", time.Hour))
	ok, err := cache.IsVerified(ctx, "hmac-1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestVerificationCache_Expires(t *testing.T) {
	client, mr := newTestClient(t)
	cache := NewVerificationCache(client)
	ctx := context.Background()
	require.NoError(t, cache.MarkVerified(ctx, "hmac-exp", 2*time.Second))
	mr.FastForward(3 * time.Second)
	ok, err := cache.IsVerified(ctx, "hmac-exp")
	require.NoError(t, err)
	assert.False(t, ok)
}

// ── RateLimiter (token bucket) ────────────────────────────────────────────────

func TestRateLimiter_Allow_First(t *testing.T) {
	client, _ := newTestClient(t)
	limiter := NewRateLimiter(client, 10.0, 10.0)
	allowed, err := limiter.Allow(context.Background())
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestRateLimiter_Allow_BucketExhausted(t *testing.T) {
	client, _ := newTestClient(t)
	// capacity=2, rps=0.01 (refill very slowly)
	limiter := NewRateLimiter(client, 0.01, 2.0)
	ctx := context.Background()
	// Exhaust the bucket
	for i := 0; i < 2; i++ {
		_, _ = limiter.Allow(ctx)
	}
	allowed, err := limiter.Allow(ctx)
	require.NoError(t, err)
	assert.False(t, allowed)
}

// ── Constructor guard clauses ─────────────────────────────────────────────────

func TestNewCircuitBreaker_DefaultsThreshold(t *testing.T) {
	client, _ := newTestClient(t)
	// failureThreshold < 1 → should default to 1
	breaker := NewCircuitBreaker(client, 0, time.Minute)
	ctx := context.Background()
	// Single failure should open the breaker (threshold defaulted to 1)
	require.NoError(t, breaker.RecordFailure(ctx))
	state, err := breaker.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, entity.BreakerOpen, state)
}

func TestNewRateLimiter_DefaultRPS(t *testing.T) {
	client, _ := newTestClient(t)
	// rps <= 0 and burstCapacity < 1 → both default
	limiter := NewRateLimiter(client, 0, 0)
	allowed, err := limiter.Allow(context.Background())
	require.NoError(t, err)
	assert.True(t, allowed) // first call always allowed (bucket starts full)
}
