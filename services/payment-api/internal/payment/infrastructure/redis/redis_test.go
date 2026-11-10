package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

func newTestClient(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()}), mr
}

// ── GrantStore ────────────────────────────────────────────────────────────────

// The hash fields are fixed by ADR-006 §8.13: event_id, booking_id,
// queue_token, plus exactly one of account_id or device_hash.
func TestGrantStore_ReturnsTheHashFields(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, client.HSet(ctx, "grant:tok-1",
		"event_id", "EV-1",
		"booking_id", "RB-00912",
		"queue_token", "qt-1",
		"account_id", "ACC-1",
	).Err())

	fields, err := NewGrantStore(client).Grant(ctx, "tok-1")
	require.NoError(t, err)

	assert.Equal(t, "EV-1", fields["event_id"])
	assert.Equal(t, "RB-00912", fields["booking_id"])
	assert.Equal(t, "qt-1", fields["queue_token"])
	assert.Equal(t, "ACC-1", fields["account_id"])
}

// HGETALL on a missing key returns an EMPTY MAP rather than an error, which is
// why the length is checked explicitly: without that an expired grant would
// look like a grant with no fields and sail past the credential check.
func TestGrantStore_MissingGrant_IsNotFound(t *testing.T) {
	client, _ := newTestClient(t)

	_, err := NewGrantStore(client).Grant(context.Background(), "nope")
	assert.ErrorIs(t, err, grantauth.ErrNotFound)
}

// The key's own TTL is the expiry mechanism, so there is no timestamp to
// compare: once Redis drops the hash the grant is gone.
func TestGrantStore_ExpiredGrant_IsNotFound(t *testing.T) {
	client, mr := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, client.HSet(ctx, "grant:tok-1", "booking_id", "RB-00912").Err())
	require.NoError(t, client.Expire(ctx, "grant:tok-1", 30*time.Second).Err())

	mr.FastForward(31 * time.Second)

	_, err := NewGrantStore(client).Grant(ctx, "tok-1")
	assert.ErrorIs(t, err, grantauth.ErrNotFound)
}

// Redis being unreachable is an infrastructure failure, and it must not be
// reported as a missing grant: the guard turns ErrNotFound into a 401, and
// telling every paying customer their session expired over a Redis blip is
// the worst available answer.
func TestGrantStore_Unreachable_IsNotMistakenForMissing(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})

	_, err := NewGrantStore(client).Grant(context.Background(), "tok-1")
	require.Error(t, err)
	assert.NotErrorIs(t, err, grantauth.ErrNotFound)
}

func TestGrantStore_KeyIsPrefixed(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	// Written WITHOUT the prefix, so a store that forgot it would find this.
	require.NoError(t, client.HSet(ctx, "tok-1", "booking_id", "RB-00912").Err())

	_, err := NewGrantStore(client).Grant(ctx, "tok-1")
	assert.ErrorIs(t, err, grantauth.ErrNotFound)
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
	require.NoError(t, client.Set(ctx, KeyAcctPrefix+"EV-1:ACC-1", "qt-1", 0).Err())
	require.NoError(t, client.Set(ctx, KeyTokenPrefix+"qt-1", "engaged", 0).Err())

	require.NoError(t, NewCredentialStore(client).SettleEngagement(ctx, settleInput()))

	// BR-07 allows one ACTIVE engagement, and a confirmed purchase is not
	// active: a customer who wants a second pair of tickets must not be locked
	// out.
	n, err := client.Exists(ctx, KeyAcctPrefix+"EV-1:ACC-1").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	n, err = client.Exists(ctx, KeyTokenPrefix+"qt-1").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	// The tombstone is what stops a guest who refreshes between settlement and
	// the e-ticket email from being shown an expiry screen (§3.3).
	got, err := client.Get(ctx, KeySettledPrefix+"qt-1").Result()
	require.NoError(t, err)
	assert.Equal(t, "RB-00912", got)
}

// A tombstone deliberately cannot recover anything: no ticket number, no
// account id, no device hash. It holds the booking id and nothing else.
func TestSettleEngagement_TombstoneHoldsOnlyTheBookingID(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, NewCredentialStore(client).SettleEngagement(ctx, settleInput()))

	got, err := client.Get(ctx, KeySettledPrefix+"qt-1").Result()
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

	n, err := client.Exists(ctx, KeySettledPrefix+"qt-1").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

// A guest booking has no account_id, so there is no BR-07 hold to release -
// and no key named acct:EV-1: must be touched.
func TestSettleEngagement_GuestBooking_TouchesNoAccountKey(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, client.Set(ctx, KeyAcctPrefix+"EV-1:", "someone-else", 0).Err())

	in := settleInput()
	in.AccountID = ""
	require.NoError(t, NewCredentialStore(client).SettleEngagement(ctx, in))

	n, err := client.Exists(ctx, KeyAcctPrefix+"EV-1:").Result()
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

	n, err := client.Exists(ctx, KeySettledPrefix).Result()
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

	got, err := client.Get(ctx, KeySettledPrefix+"qt-1").Result()
	require.NoError(t, err)
	assert.Equal(t, "RB-00912", got)
}

func TestSettleEngagement_Unreachable_IsReported(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})

	err := NewCredentialStore(client).SettleEngagement(context.Background(), settleInput())
	assert.Error(t, err)
}

// Harmless if missed - the sweep re-reads the booking and finds it CONFIRMED -
// but leaving it costs a wasted row read every tick for the rest of the sale.
func TestUnscheduleExpiry_RemovesTheBookingFromTheSweep(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	require.NoError(t, client.ZAdd(ctx, KeyExpiriesPrefix+"EV-1",
		goredis.Z{Score: 1, Member: "RB-00912"},
		goredis.Z{Score: 2, Member: "RB-00913"},
	).Err())

	require.NoError(t, NewCredentialStore(client).UnscheduleExpiry(ctx, "EV-1", "RB-00912"))

	n, err := client.ZCard(ctx, KeyExpiriesPrefix+"EV-1").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only this booking is removed")
	_, err = client.ZScore(ctx, KeyExpiriesPrefix+"EV-1", "RB-00913").Result()
	assert.NoError(t, err)
}

func TestUnscheduleExpiry_AlreadyGone_IsNotAnError(t *testing.T) {
	client, _ := newTestClient(t)

	assert.NoError(t, NewCredentialStore(client).UnscheduleExpiry(context.Background(), "EV-1", "RB-00912"))
}

func TestUnscheduleExpiry_Unreachable_IsReported(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})

	err := NewCredentialStore(client).UnscheduleExpiry(context.Background(), "EV-1", "RB-00912")
	assert.Error(t, err)
}

// ── keys ──────────────────────────────────────────────────────────────────────

// The breaker state is read by other services, so renaming it here would
// silently decouple them. The payment breaker is also deliberately distinct
// from the Dukcapil one: one upstream being down says nothing about the other.
func TestKeyNamesAreStable(t *testing.T) {
	assert.Equal(t, "grant:", KeyGrantPrefix)
	assert.Equal(t, "payment:breaker:state", KeyBreakerState)
	assert.Equal(t, "payment:breaker:failures", KeyBreakerFails)
	assert.Equal(t, "token:", KeyTokenPrefix)
	assert.Equal(t, "acct:", KeyAcctPrefix)
	assert.Equal(t, "settled:", KeySettledPrefix)
	assert.Equal(t, "expiries:", KeyExpiriesPrefix)
	assert.NotEqual(t, KeyBreakerState, "dukcapil:breaker:state")
}
