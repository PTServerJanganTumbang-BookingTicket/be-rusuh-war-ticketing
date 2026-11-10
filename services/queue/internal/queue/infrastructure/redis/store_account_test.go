package redis

import (
	"context"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

func TestAccountTokenReadsThePointerAndReportsAMissingOneAsNotFound(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	if _, found, err := s.AccountToken(ctx, ev, "acc-1"); err != nil || found {
		t.Fatalf("missing: found=%v err=%v", found, err)
	}
	rdb.Set(ctx, ev.Acct("acc-1"), "qt_A", 0)
	if tok, found, err := s.AccountToken(ctx, ev, "acc-1"); err != nil || !found || tok != "qt_A" {
		t.Fatalf("got %q found=%v err=%v", tok, found, err)
	}
	rdb.HSet(ctx, ev.Acct("acc-2"), "f", "v") // wrong type
	if _, _, err := s.AccountToken(ctx, ev, "acc-2"); err == nil {
		t.Fatal("a WRONGTYPE error was hidden")
	}
}

func TestReleaseAccountDeletesOnlyThePointerThatStillHoldsMyToken(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	rdb.Set(ctx, ev.Acct("acc-1"), "qt_A", 0)

	if err := s.ReleaseAccount(ctx, ev, "acc-1", "qt_OTHER"); err != nil {
		t.Fatal(err)
	}
	if v, _ := rdb.Get(ctx, ev.Acct("acc-1")).Result(); v != "qt_A" {
		t.Fatalf("another token's release removed the pointer (now %q)", v)
	}
	if err := s.ReleaseAccount(ctx, ev, "acc-1", "qt_A"); err != nil {
		t.Fatal(err)
	}
	if n, _ := rdb.Exists(ctx, ev.Acct("acc-1")).Result(); n != 0 {
		t.Fatal("the owner's release left the pointer")
	}
	if err := s.ReleaseAccount(ctx, ev, "acc-1", "qt_A"); err != nil {
		t.Fatalf("releasing a missing pointer = %v, want nil", err)
	}
	rdb.HSet(ctx, ev.Acct("acc-2"), "f", "v")
	if err := s.ReleaseAccount(ctx, ev, "acc-2", "qt_A"); err == nil {
		t.Fatal("a WRONGTYPE error was hidden")
	}
}

func TestSaveEngagementWritesTheTokenHashWithATTLAndLoadsItBack(t *testing.T) {
	s, rdb, _ := newStore(t)
	ctx := context.Background()
	joined := time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)
	e := domain.Engagement{QueueToken: "qt_A", EventID: "E1", TicketNo: 7, AccountID: "acc-1", DeviceHash: "dh", JoinedAt: joined}
	if err := s.SaveEngagement(ctx, e, time.Hour); err != nil {
		t.Fatal(err)
	}
	if ttl, _ := rdb.TTL(ctx, rediskeys.Token("qt_A")).Result(); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl = %v", ttl)
	}
	got, found, err := s.Engagement(ctx, "qt_A")
	if err != nil || !found || got.TicketNo != 7 || got.AccountID != "acc-1" || got.DeviceHash != "dh" || !got.JoinedAt.Equal(joined) {
		t.Fatalf("got %+v found=%v err=%v", got, found, err)
	}

	guest := domain.Engagement{QueueToken: "qt_G", EventID: "E1", TicketNo: 8, JoinedAt: joined}
	if err := s.SaveEngagement(ctx, guest, time.Hour); err != nil {
		t.Fatal(err)
	}
	if rdb.HExists(ctx, rediskeys.Token("qt_G"), FieldAccountID).Val() || rdb.HExists(ctx, rediskeys.Token("qt_G"), FieldDeviceHash).Val() {
		t.Fatal("empty account/device fields were stored")
	}
}

func TestAddToLineKeepsAnExistingScore(t *testing.T) {
	s, rdb, ev := newStore(t)
	ctx := context.Background()
	if err := s.AddToLine(ctx, ev, "qt_A", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToLine(ctx, ev, "qt_A", 9); err != nil {
		t.Fatal(err)
	}
	if sc, _ := rdb.ZScore(ctx, ev.ByTicket(), "qt_A").Result(); sc != 5 {
		t.Fatalf("score = %v, want the first one kept", sc)
	}
}
