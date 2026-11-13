package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

func TestServingReadsStateAndBothCountersFromTheEventHash(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	rdb.HSet(ctx, ev.QueueHash(), FieldState, "FULL_BOOKED", FieldNowServing, 31600, rediskeys.FieldRatePerMin, 318, FieldIssued, 40000)
	got, found, err := s.Serving(ctx, ev)
	want := domain.Serving{State: domain.StateFullBooked, NowServing: 31600, RatePerMin: 318}
	if err != nil || !found || got != want {
		t.Fatalf("Serving() = %+v, %v, %v; want %+v", got, found, err, want)
	}
}

func TestServingReadsMissingCountersAsZero(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	rdb.HSet(ctx, ev.QueueHash(), FieldState, "SCHEDULED")
	got, found, err := s.Serving(ctx, ev)
	if err != nil || !found || got != (domain.Serving{State: domain.StateScheduled}) {
		t.Fatalf("Serving() = %+v, %v, %v", got, found, err)
	}
}

func TestServingReportsAnEventWithoutStateAsNotFound(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	if _, found, err := s.Serving(ctx, ev); err != nil || found {
		t.Fatalf("no hash: found %v, err %v", found, err)
	}
	rdb.HSet(ctx, ev.QueueHash(), FieldNowServing, 5) // counters without state: still not seeded
	if _, found, err := s.Serving(ctx, ev); err != nil || found {
		t.Fatalf("no state: found %v, err %v", found, err)
	}
}

func TestServingFailsOnACounterThatIsNotAnInteger(t *testing.T) {
	for _, field := range []string{FieldNowServing, rediskeys.FieldRatePerMin} {
		s, rdb, ev := newStore(t)
		ctx := context.Background()
		rdb.HSet(ctx, ev.QueueHash(), FieldState, "OPEN", field, "lots")
		if _, _, err := s.Serving(ctx, ev); err == nil {
			t.Errorf("%s = lots: Serving() returned no error", field)
		}
	}
}

func TestServingFailsOnARedisError(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	rdb.Set(ctx, ev.QueueHash(), "oops", 0) // WRONGTYPE on HMGET
	if _, _, err := s.Serving(ctx, ev); err == nil {
		t.Fatal("Serving() hid a WRONGTYPE error")
	}
}

// The same read against a real Redis (make verify sets REDIS_ADDR): the hash admission-ctl writes.
func TestServingOnRealRedis(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" || testing.Short() {
		t.Skip("set REDIS_ADDR (make verify does)")
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	ev, _ := rediskeys.NewEvent(fmt.Sprintf("evt_serving_%d", time.Now().UnixNano()))
	t.Cleanup(func() { rdb.Del(ctx, ev.QueueHash()) })
	s := NewStore(rdb)

	if _, found, err := s.Serving(ctx, ev); err != nil || found {
		t.Fatalf("unseeded: found %v, err %v", found, err)
	}
	rdb.HSet(ctx, ev.QueueHash(), FieldState, "OPEN", FieldNowServing, 120, rediskeys.FieldRatePerMin, 90)
	got, found, err := s.Serving(ctx, ev)
	if err != nil || !found || got != (domain.Serving{State: domain.StateOpen, NowServing: 120, RatePerMin: 90}) {
		t.Fatalf("Serving() = %+v, %v, %v", got, found, err)
	}
}

func TestServingFailsOnANegativeCounter(t *testing.T) {
	for _, field := range []string{FieldNowServing, rediskeys.FieldRatePerMin} {
		s, rdb, ev := newStore(t)
		rdb.HSet(context.Background(), ev.QueueHash(), FieldState, "OPEN", field, -5)
		if _, _, err := s.Serving(context.Background(), ev); err == nil {
			t.Errorf("%s = -5: Serving() returned no error", field)
		}
	}
}
