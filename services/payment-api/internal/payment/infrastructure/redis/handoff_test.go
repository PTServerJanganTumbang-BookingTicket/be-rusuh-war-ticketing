package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

// onEveryRedis runs body on miniredis and, with REDIS_ADDR set (make test-module), on the real Redis. Keys get a
// unique suffix so runs never share them.
func onEveryRedis(t *testing.T, body func(t *testing.T, rdb goredis.UniversalClient, id string)) {
	t.Helper()
	mr := miniredis.RunT(t)
	mini := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = mini.Close() })
	targets := map[string]goredis.UniversalClient{"miniredis": mini}
	if addr := os.Getenv("REDIS_ADDR"); addr != "" && !testing.Short() {
		// A plain client, as payment-api itself connects (redisutil.NewClient). Every command here touches one key
		// with no hash tag, so the same code also runs on a cluster.
		c := goredis.NewClient(&goredis.Options{Addr: addr})
		t.Cleanup(func() { _ = c.Close() })
		targets["real"] = c
	}
	for name, rdb := range targets {
		t.Run(name, func(t *testing.T) { body(t, rdb, fmt.Sprintf("%d", time.Now().UnixNano())) })
	}
}

func TestHandoffStore_ACodeIsReadOnceAndThenGone(t *testing.T) {
	onEveryRedis(t, func(t *testing.T, rdb goredis.UniversalClient, id string) {
		ctx, s := context.Background(), NewHandoffStore(rdb)
		code := "hc_" + id
		created, err := s.PutCode(ctx, code, "gt_"+id, time.Minute)
		require.NoError(t, err)
		require.True(t, created)

		ttl, err := rdb.TTL(ctx, rediskeys.PaymentHandoff(code)).Result()
		require.NoError(t, err)
		assert.InDelta(t, 60, ttl.Seconds(), 1)

		grant, found, err := s.TakeCode(ctx, code)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "gt_"+id, grant)

		_, found, err = s.TakeCode(ctx, code)
		require.NoError(t, err)
		assert.False(t, found, "a second read finds nothing")
	})
}

func TestHandoffStore_ACodeIsNeverOverwritten(t *testing.T) {
	onEveryRedis(t, func(t *testing.T, rdb goredis.UniversalClient, id string) {
		ctx, s := context.Background(), NewHandoffStore(rdb)
		code := "hc_" + id
		_, err := s.PutCode(ctx, code, "gt_first", time.Minute)
		require.NoError(t, err)
		created, err := s.PutCode(ctx, code, "gt_second", time.Minute)
		require.NoError(t, err)
		assert.False(t, created)
		grant, _, _ := s.TakeCode(ctx, code)
		assert.Equal(t, "gt_first", grant)
	})
}

func TestHandoffStore_GrantBookingReadsTheBookingAndTheRemainingTTL(t *testing.T) {
	onEveryRedis(t, func(t *testing.T, rdb goredis.UniversalClient, id string) {
		ctx, s := context.Background(), NewHandoffStore(rdb)
		key := rediskeys.Grant("gt_" + id)
		require.NoError(t, rdb.HSet(ctx, key, rediskeys.FieldBookingID, "RB-"+id).Err())
		require.NoError(t, rdb.Expire(ctx, key, 900*time.Second).Err())

		booking, ttl, found, err := s.GrantBooking(ctx, "gt_"+id)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "RB-"+id, booking)
		assert.InDelta(t, 900, ttl.Seconds(), 1)
	})
}

func TestHandoffStore_AMissingGrantIsNotFoundAndAGrantWithoutTTLHasNone(t *testing.T) {
	onEveryRedis(t, func(t *testing.T, rdb goredis.UniversalClient, id string) {
		ctx, s := context.Background(), NewHandoffStore(rdb)
		_, _, found, err := s.GrantBooking(ctx, "gt_missing_"+id)
		require.NoError(t, err)
		assert.False(t, found)

		key := rediskeys.Grant("gt_nottl_" + id)
		require.NoError(t, rdb.HSet(ctx, key, rediskeys.FieldBookingID, "RB-1").Err())
		t.Cleanup(func() { rdb.Del(context.Background(), key) })
		_, ttl, found, err := s.GrantBooking(ctx, "gt_nottl_"+id)
		require.NoError(t, err)
		assert.True(t, found)
		assert.LessOrEqual(t, ttl, time.Duration(0), "no TTL must read as <= 0 so the use case refuses it")
	})
}

func TestHandoffStore_ASessionGetsTheTTLItIsGiven(t *testing.T) {
	onEveryRedis(t, func(t *testing.T, rdb goredis.UniversalClient, id string) {
		ctx, s := context.Background(), NewHandoffStore(rdb)
		created, err := s.PutSession(ctx, "ps_"+id, "gt_"+id, 1500*time.Millisecond)
		require.NoError(t, err)
		require.True(t, created)
		pttl, err := rdb.PTTL(ctx, rediskeys.PaymentSession("ps_"+id)).Result()
		require.NoError(t, err)
		assert.InDelta(t, 1500, pttl.Milliseconds(), 100)
		v, err := rdb.Get(ctx, rediskeys.PaymentSession("ps_"+id)).Result()
		require.NoError(t, err)
		assert.Equal(t, "gt_"+id, v)
	})
}

func TestHandoffLimiter_TheEleventhCodeInAMinuteIsRefused(t *testing.T) {
	onEveryRedis(t, func(t *testing.T, rdb goredis.UniversalClient, id string) {
		ctx, l := context.Background(), NewHandoffLimiter(rdb)
		for i := 0; i < 10; i++ {
			ok, _, err := l.Allow(ctx, "gt_"+id)
			require.NoError(t, err)
			require.True(t, ok, "call %d", i+1)
		}
		ok, retry, err := l.Allow(ctx, "gt_"+id)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Greater(t, retry, time.Duration(0))
	})
}

func TestHandoffLimiter_AnErrorIsReportedNotSwallowed(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	mr.SetError("connection refused")
	_, _, err := NewHandoffLimiter(rdb).Allow(context.Background(), "gt_1")
	assert.Error(t, err, "the use case decides to fail open; the adapter must not hide the failure")
}

func TestHandoffStore_TwentyConcurrentExchangesOfOneCodeHaveExactlyOneWinner(t *testing.T) {
	onEveryRedis(t, func(t *testing.T, rdb goredis.UniversalClient, id string) {
		ctx, s := context.Background(), NewHandoffStore(rdb)
		code := "hc_race_" + id
		_, err := s.PutCode(ctx, code, "gt_"+id, time.Minute)
		require.NoError(t, err)

		wins := make(chan bool, 20)
		start := make(chan struct{})
		for i := 0; i < 20; i++ {
			go func() {
				<-start
				_, found, err := s.TakeCode(ctx, code)
				wins <- err == nil && found
			}()
		}
		close(start)
		won := 0
		for i := 0; i < 20; i++ {
			if <-wins {
				won++
			}
		}
		assert.Equal(t, 1, won)
	})
}
