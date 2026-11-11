package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

func queueRig(t *testing.T) (*Queue, *miniredis.Miniredis, rediskeys.Event) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, _ := rediskeys.NewEvent("evt_a")
	return NewQueue(rdb), mr, ev
}

func TestTheSnapshotReadsTheStateAndWhenItFilled(t *testing.T) {
	q, mr, ev := queueRig(t)
	mr.HSet(ev.QueueHash(), "state", "FULL_BOOKED", "full_booked_at", "1794000000")
	s, err := q.Snapshot(context.Background(), "evt_a")
	if err != nil || s.State != "FULL_BOOKED" || !s.FullBookedAt.Equal(time.Unix(1794000000, 0)) {
		t.Fatalf("snapshot = %+v (%v)", s, err)
	}
}

func TestAnOpenQueueHasNoFullBookedTime(t *testing.T) {
	q, mr, ev := queueRig(t)
	mr.HSet(ev.QueueHash(), "state", "OPEN")
	s, err := q.Snapshot(context.Background(), "evt_a")
	if err != nil || s.State != "OPEN" || !s.FullBookedAt.IsZero() {
		t.Fatalf("snapshot = %+v (%v)", s, err)
	}
}

func TestAMissingHashOrAnUnreadableTimeGivesAnEmptySnapshotNotAnError(t *testing.T) {
	q, mr, ev := queueRig(t)
	if s, err := q.Snapshot(context.Background(), "evt_a"); err != nil || s.State != "" || !s.FullBookedAt.IsZero() {
		t.Fatalf("missing hash: %+v (%v)", s, err)
	}
	mr.HSet(ev.QueueHash(), "state", "CLOSED", "full_booked_at", "yesterday")
	if s, err := q.Snapshot(context.Background(), "evt_a"); err != nil || s.State != "CLOSED" || !s.FullBookedAt.IsZero() {
		t.Fatalf("bad time: %+v (%v), want the state with no time", s, err)
	}
}

func TestSnapshotReportsAnInvalidEventIdAndARedisError(t *testing.T) {
	q, _, _ := queueRig(t)
	if _, err := q.Snapshot(context.Background(), "evt{bad}"); err == nil {
		t.Error("an event id with braces must be refused")
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	defer func() { _ = rdb.Close() }()
	if _, err := NewQueue(rdb).Snapshot(context.Background(), "evt_a"); err == nil {
		t.Error("a Redis error must be reported (the caller then labels the queue FULL_BOOKED)")
	}
}
