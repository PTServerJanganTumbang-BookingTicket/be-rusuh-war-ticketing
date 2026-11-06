package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

func newStore(t *testing.T) (*Store, *goredis.Client, rediskeys.Event) {
	t.Helper()
	rdb := goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, _ := rediskeys.NewEvent("E1")
	return NewStore(rdb), rdb, ev
}

// A served ticket is never put back: admission-ctl would grant it again.
func TestRepairLine_neverReaddsAServedTicket(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	rdb.HSet(ctx, ev.QueueHash(), FieldNowServing, 100)
	for _, n := range []int64{99, 100} {
		added, err := s.RepairLine(ctx, ev, "qt_SERVED", n)
		if err != nil || added {
			t.Fatalf("RepairLine(ticket %d, ns 100) = %v, %v; want not added", n, added, err)
		}
	}
	if c, _ := rdb.ZCard(ctx, ev.ByTicket()).Result(); c != 0 {
		t.Fatalf("by_ticket has %d members", c)
	}
	added, err := s.RepairLine(ctx, ev, "qt_WAITING", 101)
	if err != nil || !added {
		t.Fatalf("RepairLine(101) = %v, %v; want added", added, err)
	}
	if added, _ := s.RepairLine(ctx, ev, "qt_WAITING", 101); added {
		t.Fatal("second RepairLine reported added")
	}
}

// A real per-command error (not a missing key) fails the snapshot instead of
// reading as 0, which would open the capacity gate.
func TestSnapshot_failsClosedOnWrongType(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	rdb.Set(ctx, ev.QueueState(), "OPEN", 0)
	rdb.HSet(ctx, ev.QueueIssued(), "oops", 1) // issued is a hash: WRONGTYPE on GET
	if _, err := s.Snapshot(ctx, ev); err == nil {
		t.Fatal("Snapshot hid a WRONGTYPE error")
	}
}

func TestClaimAccount_reportsTheWinner(t *testing.T) {
	s, _, ev := newStore(t)
	ctx := context.Background()
	if ok, cur, err := s.ClaimAccount(ctx, ev, "acc-1", "qt_A", "", time.Hour); !ok || cur != "qt_A" || err != nil {
		t.Fatalf("first claim = %v %q %v", ok, cur, err)
	}
	ok, cur, err := s.ClaimAccount(ctx, ev, "acc-1", "qt_B", "", time.Hour)
	if ok || cur != "qt_A" || err != nil {
		t.Fatalf("second claim = %v %q %v; want lost to qt_A", ok, cur, err)
	}
	// Replacing a stale pointer works only if it still holds the stale value.
	if ok, cur, _ := s.ClaimAccount(ctx, ev, "acc-1", "qt_C", "qt_STALE", time.Hour); ok || cur != "qt_A" {
		t.Fatalf("replace with wrong stale value = %v %q", ok, cur)
	}
	if ok, cur, _ := s.ClaimAccount(ctx, ev, "acc-1", "qt_C", "qt_A", time.Hour); !ok || cur != "qt_C" {
		t.Fatalf("replace = %v %q", ok, cur)
	}
}
