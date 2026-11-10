package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

func newTestClient(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()}), mr
}

// ev builds the keys for the event these tests settle against. Built the same
// way the adapter builds them, so a test cannot assert a key the production
// code would never write - including the {hash tag} that keeps an event's keys
// on one cluster slot.
func ev(t *testing.T) rediskeys.Event {
	t.Helper()
	e, err := rediskeys.NewEvent("EV-1")
	require.NoError(t, err)
	return e
}

// ── CircuitBreaker ────────────────────────────────────────────────────────────

func TestCircuitBreaker_StartsClosed(t *testing.T) {
	client, _ := newTestClient(t)

	allowed, err := NewCircuitBreaker(client, 3, time.Minute).Allow(context.Background())
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestCircuitBreaker_OpensAtTheThreshold(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	b := NewCircuitBreaker(client, 3, time.Minute)

	for i := 0; i < 2; i++ {
		require.NoError(t, b.RecordFailure(ctx))
		allowed, err := b.Allow(ctx)
		require.NoError(t, err)
		assert.True(t, allowed, "still closed below the threshold")
	}

	require.NoError(t, b.RecordFailure(ctx))
	allowed, err := b.Allow(ctx)
	require.NoError(t, err)
	assert.False(t, allowed)
}

// The OPEN marker carries its own TTL, so recovery needs no scheduler: when
// the key expires the next call probes the gateway again. A breaker reset by a
// timer in a process that may not survive would stay open forever.
func TestCircuitBreaker_ReopensItselfWhenTheMarkerExpires(t *testing.T) {
	client, mr := newTestClient(t)
	ctx := context.Background()
	b := NewCircuitBreaker(client, 1, 30*time.Second)

	require.NoError(t, b.RecordFailure(ctx))
	allowed, err := b.Allow(ctx)
	require.NoError(t, err)
	require.False(t, allowed)

	mr.FastForward(31 * time.Second)

	allowed, err = b.Allow(ctx)
	require.NoError(t, err)
	assert.True(t, allowed)
}

// Both keys are cleared, not just the state: leaving the failure counter near
// its threshold would let one later failure trip the breaker immediately, so a
// recovered gateway would keep flapping.
func TestCircuitBreaker_SuccessClearsTheCounterToo(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	b := NewCircuitBreaker(client, 3, time.Minute)

	require.NoError(t, b.RecordFailure(ctx))
	require.NoError(t, b.RecordFailure(ctx))
	require.NoError(t, b.RecordSuccess(ctx))

	n, err := client.Exists(ctx, KeyBreakerFails).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	// One failure after recovery must not re-open a 3-failure breaker.
	require.NoError(t, b.RecordFailure(ctx))
	allowed, err := b.Allow(ctx)
	require.NoError(t, err)
	assert.True(t, allowed)
}

// The counting window is bounded, so failures spread over hours do not
// accumulate into an outage that never happened.
func TestCircuitBreaker_CountingWindowExpires(t *testing.T) {
	client, mr := newTestClient(t)
	ctx := context.Background()
	b := NewCircuitBreaker(client, 3, 30*time.Second)

	require.NoError(t, b.RecordFailure(ctx))
	require.NoError(t, b.RecordFailure(ctx))
	mr.FastForward(31 * time.Second)

	require.NoError(t, b.RecordFailure(ctx))
	allowed, err := b.Allow(ctx)
	require.NoError(t, err)
	assert.True(t, allowed, "the earlier failures should have aged out")
}

// A threshold below one would open the breaker before anything failed, so it
// is clamped rather than trusted.
func TestCircuitBreaker_ThresholdIsClamped(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	for _, threshold := range []int{0, -5} {
		b := NewCircuitBreaker(client, threshold, time.Minute)
		allowed, err := b.Allow(ctx)
		require.NoError(t, err)
		assert.True(t, allowed, "a fresh breaker is closed whatever the threshold")

		require.NoError(t, b.RecordFailure(ctx))
		allowed, err = b.Allow(ctx)
		require.NoError(t, err)
		assert.False(t, allowed, "one failure opens a clamped breaker")
		require.NoError(t, b.RecordSuccess(ctx))
	}
}

// A breaker that cannot be read must not refuse payments: Allow reports true
// alongside the error and the caller decides.
func TestCircuitBreaker_Unreachable_FailsOpen(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})

	allowed, err := NewCircuitBreaker(client, 3, time.Minute).Allow(context.Background())
	require.Error(t, err)
	assert.True(t, allowed)
}

// ── CredentialStore ───────────────────────────────────────────────────────────

func settleInput() portout.SettleInput {
	return portout.SettleInput{
		EventID:      "EV-1",
		BookingID:    "RB-00912",
		QueueToken:   "qt-1",
		AccountID:    "ACC-1",
		TombstoneTTL: time.Hour,
	}
}

// Three things, in one pipeline: the account's BR-07 hold is released, the
// engagement token is deleted, and the settled tombstone is written in its
// place.
func TestSettleEngagement_ReleasesAndTombstones(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, client.Set(ctx, ev(t).Acct("ACC-1"), "qt-1", 0).Err())
	require.NoError(t, client.Set(ctx, rediskeys.Token("qt-1"), "engaged", 0).Err())

	require.NoError(t, NewCredentialStore(client).SettleEngagement(ctx, settleInput()))

	// BR-07 allows one ACTIVE engagement, and a confirmed purchase is not
	// active: a customer who wants a second pair of tickets must not be locked
	// out.
	n, err := client.Exists(ctx, ev(t).Acct("ACC-1")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	n, err = client.Exists(ctx, rediskeys.Token("qt-1")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	// The tombstone is what stops a guest who refreshes between settlement and
	// the e-ticket email from being shown an expiry screen (§3.3).
	got, err := client.Get(ctx, rediskeys.Settled("qt-1")).Result()
	require.NoError(t, err)
	assert.Equal(t, "RB-00912", got)
}

// A tombstone deliberately cannot recover anything: no ticket number, no
// account id, no device hash. It holds the booking id and nothing else.
func TestSettleEngagement_TombstoneHoldsOnlyTheBookingID(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, NewCredentialStore(client).SettleEngagement(ctx, settleInput()))

	got, err := client.Get(ctx, rediskeys.Settled("qt-1")).Result()
	require.NoError(t, err)
	assert.Equal(t, "RB-00912", got)
	assert.NotContains(t, got, "ACC-1")
	assert.NotContains(t, got, "qt-1")
}

// The tombstone's window is bounded, so the recovery answer does not outlive
// the sale.
func TestSettleEngagement_TombstoneCarriesItsTTL(t *testing.T) {
	client, mr := newTestClient(t)
	ctx := context.Background()
	in := settleInput()
	in.TombstoneTTL = 30 * time.Second

	require.NoError(t, NewCredentialStore(client).SettleEngagement(ctx, in))
	mr.FastForward(31 * time.Second)

	n, err := client.Exists(ctx, rediskeys.Settled("qt-1")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

// A guest booking has no account_id, so there is no BR-07 hold to release -
// and no key named acct:EV-1: must be touched.
func TestSettleEngagement_GuestBooking_TouchesNoAccountKey(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, client.Set(ctx, ev(t).Acct(""), "someone-else", 0).Err())

	in := settleInput()
	in.AccountID = ""
	require.NoError(t, NewCredentialStore(client).SettleEngagement(ctx, in))

	n, err := client.Exists(ctx, ev(t).Acct("")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "a blank account id must not delete another key")
}

// Nothing to delete and nothing to write when there is no queue token, which
// happens for a booking whose engagement already ended.
func TestSettleEngagement_NoQueueToken_WritesNoTombstone(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	in := settleInput()
	in.QueueToken = ""
	require.NoError(t, NewCredentialStore(client).SettleEngagement(ctx, in))

	n, err := client.Exists(ctx, rediskeys.Settled("")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

// Everything here is idempotent, which is why there is no lease around it: the
// lease ADR-005 specified existed to protect DECR slots:open, and ADR-006 §3.2
// removed that counter entirely.
func TestSettleEngagement_IsIdempotent(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	store := NewCredentialStore(client)

	require.NoError(t, store.SettleEngagement(ctx, settleInput()))
	require.NoError(t, store.SettleEngagement(ctx, settleInput()))

	got, err := client.Get(ctx, rediskeys.Settled("qt-1")).Result()
	require.NoError(t, err)
	assert.Equal(t, "RB-00912", got)
}

func TestSettleEngagement_Unreachable_IsReported(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})

	err := NewCredentialStore(client).SettleEngagement(context.Background(), settleInput())
	assert.Error(t, err)
}

// ── keys ──────────────────────────────────────────────────────────────────────

// The breaker state is read by other services, so renaming it here would
// silently decouple them. The payment breaker is also deliberately distinct
// from the Dukcapil one: one upstream being down says nothing about the other.
func TestBreakerKeyNamesAreStable(t *testing.T) {
	assert.Equal(t, "payment:breaker:state", KeyBreakerState)
	assert.Equal(t, "payment:breaker:failures", KeyBreakerFails)
	assert.NotEqual(t, KeyBreakerState, "dukcapil:breaker:state")
}

// Every key more than one service touches is pkg/rediskeys' to name. A local
// copy of a prefix is exactly how two readers drift apart, and this service
// used to carry four of them.
func TestSharedKeysComeFromRediskeys(t *testing.T) {
	assert.Equal(t, "grant:tok-1", rediskeys.Grant("tok-1"))
	assert.Equal(t, "token:qt-1", rediskeys.Token("qt-1"))
	assert.Equal(t, "settled:qt-1", rediskeys.Settled("qt-1"))
	assert.Equal(t, "acct:{EV-1}:ACC-1", ev(t).Acct("ACC-1"))
}
