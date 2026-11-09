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
	rdb.Set(ctx, ev.QueueHash(), "oops", 0) // the event hash is a string: WRONGTYPE on HMGET
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

func TestEngagement_aGrantOnlyHashIsNotAPlace(t *testing.T) {
	s, rdb, _ := newStore(t)
	ctx := context.Background()
	rdb.HSet(ctx, rediskeys.Token("qt_B"), FieldGrantToken, "gt_1", FieldBookingExpiresAt, "2026-11-10T19:05:00Z")
	if _, found, err := s.Engagement(ctx, "qt_B"); found || err != nil {
		t.Fatalf("Engagement = found %v, err %v; want not found, no error", found, err)
	}
}

// ADR-006 §8.12: state, issued and full_booked_at are fields of queue:{ev}.
func TestSnapshot_readsTheMergedHash(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	rdb.HSet(ctx, ev.QueueHash(), FieldState, "FULL_BOOKED", FieldIssued, 120, FieldNowServing, 100,
		FieldFullBookedAt, 1800000000, FieldName, "Konser", FieldSaleStartsAt, "2026-11-10T02:00:00Z")
	snap, err := s.Snapshot(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != "FULL_BOOKED" || snap.Issued != 120 || snap.NowServing != 100 || snap.Name != "Konser" ||
		snap.FullBookedAt != "2027-01-15T08:00:00Z" {
		t.Fatalf("Snapshot = %+v", snap)
	}
}

func TestSnapshot_anUnseededEventHasNoState(t *testing.T) {
	s, _, ev := newStore(t)
	snap, err := s.Snapshot(context.Background(), ev)
	if err != nil || snap.State != "" || snap.Issued != 0 {
		t.Fatalf("Snapshot = %+v, %v; want an empty snapshot", snap, err)
	}
}

func TestIssueTicket_isHINCRBYOnTheHash(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	for want := int64(1); want <= 3; want++ {
		got, err := s.IssueTicket(ctx, ev)
		if err != nil || got != want {
			t.Fatalf("IssueTicket = %d, %v; want %d", got, err, want)
		}
	}
	if v, _ := rdb.HGet(ctx, ev.QueueHash(), FieldIssued).Int(); v != 3 {
		t.Fatalf("issued field = %d, want 3", v)
	}
	if keys, _ := rdb.Keys(ctx, "*").Result(); len(keys) != 1 || keys[0] != ev.QueueHash() {
		t.Fatalf("keys after issuing = %v, want only %s", keys, ev.QueueHash())
	}
}

func TestNowServing_readsTheHashFieldAndTreatsMissingAsZero(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	if n, err := s.NowServing(ctx, ev); err != nil || n != 0 {
		t.Fatalf("empty hash: %d %v", n, err)
	}
	rdb.HSet(ctx, ev.QueueHash(), "now_serving", 31600)
	if n, err := s.NowServing(ctx, ev); err != nil || n != 31600 {
		t.Fatalf("now_serving %d %v", n, err)
	}
}

func TestInLine_isTrueOnlyWhileTheTokenIsInByTicket(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	if in, err := s.InLine(ctx, ev, "qt1"); err != nil || in {
		t.Fatalf("not added yet: %v %v", in, err)
	}
	rdb.ZAdd(ctx, ev.ByTicket(), goredis.Z{Score: 5, Member: "qt1"})
	if in, err := s.InLine(ctx, ev, "qt1"); err != nil || !in {
		t.Fatalf("added: %v %v", in, err)
	}
}

func TestSettled_readsTheTombstoneBookingID(t *testing.T) {
	s, rdb, _ := newStore(t)
	ctx := context.Background()
	if _, found, err := s.Settled(ctx, "qt1"); err != nil || found {
		t.Fatalf("no tombstone: found=%v err=%v", found, err)
	}
	rdb.Set(ctx, rediskeys.Settled("qt1"), "RB-9", 0)
	if id, found, err := s.Settled(ctx, "qt1"); err != nil || !found || id != "RB-9" {
		t.Fatalf("tombstone: %q %v %v", id, found, err)
	}
}

func TestEngagement_carriesTheGrantTokenOnceGranted(t *testing.T) {
	s, rdb, _ := newStore(t)
	ctx := context.Background()
	rdb.HSet(ctx, rediskeys.Token("qt1"), "ticket_no", 7, "event_id", "E1")
	if e, _, _ := s.Engagement(ctx, "qt1"); e.GrantToken != "" {
		t.Fatalf("grant token %q before the grant", e.GrantToken)
	}
	rdb.HSet(ctx, rediskeys.Token("qt1"), "grant_token", "gt_1")
	if e, _, _ := s.Engagement(ctx, "qt1"); e.GrantToken != "gt_1" {
		t.Fatalf("grant token %q after the grant", e.GrantToken)
	}
}
