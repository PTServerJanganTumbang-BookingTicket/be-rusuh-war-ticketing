package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

func holdsRig(t *testing.T) (*Holds, *miniredis.Miniredis, rediskeys.Event) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, err := rediskeys.NewEvent("E1")
	if err != nil {
		t.Fatal(err)
	}
	return NewHolds(rdb), mr, ev
}

func expiredHold() domain.EndedHold {
	return domain.EndedHold{
		Ended: domain.Ended{BookingID: "B1", EventID: "E1", GrantToken: "G1"},
		Seats: []domain.SeatCount{{CategoryID: "VIP", Qty: 2}, {CategoryID: "FEST", Qty: 1}},
		NIKs:  []string{"n1", "n2", "n3"},
	}
}

func TestReleaseGivesTheSeatsBackAndDeletesTheNIKLocksOfAnExpiredHold(t *testing.T) {
	h, mr, ev := holdsRig(t)
	_ = mr.Set(ev.Stock("VIP"), "3")
	_ = mr.Set(ev.Stock("FEST"), "4")
	for _, n := range []string{"n1", "n2", "n3", "someone-else"} {
		_ = mr.Set(ev.NIKUsed(n), "1")
		mr.SetTTL(ev.NIKUsed(n), time.Hour)
	}
	missing, err := h.Release(context.Background(), expiredHold())
	if err != nil || missing != 0 {
		t.Fatalf("missing = %d (%v)", missing, err)
	}
	if v, _ := mr.Get(ev.Stock("VIP")); v != "5" {
		t.Fatalf("VIP = %s, want 5", v)
	}
	if v, _ := mr.Get(ev.Stock("FEST")); v != "5" {
		t.Fatalf("FEST = %s, want 5", v)
	}
	for _, n := range []string{"n1", "n2", "n3"} {
		if mr.Exists(ev.NIKUsed(n)) {
			t.Fatalf("NIK lock %s still held", n)
		}
	}
	if !mr.Exists(ev.NIKUsed("someone-else")) {
		t.Fatal("release deleted a lock it did not own")
	}
}

// A lost stock key stays lost: releasing into it would make up a count and sell seats nobody counted.
func TestAStockKeyThatIsGoneIsNotRecreatedAndIsReported(t *testing.T) {
	h, mr, ev := holdsRig(t)
	_ = mr.Set(ev.Stock("FEST"), "4") // VIP is gone
	_ = mr.Set(ev.NIKUsed("n1"), "1")
	missing, err := h.Release(context.Background(), expiredHold())
	if err != nil || missing != 1 || mr.Exists(ev.Stock("VIP")) {
		t.Fatalf("missing = %d (%v) VIP exists = %v, want 1 reported and not recreated", missing, err, mr.Exists(ev.Stock("VIP")))
	}
	if v, _ := mr.Get(ev.Stock("FEST")); v != "5" || mr.Exists(ev.NIKUsed("n1")) {
		t.Fatal("the other category was not given back, or a lock stayed")
	}
}

func TestReleaseReportsAnInvalidEventIdAndARedisError(t *testing.T) {
	h, _, _ := holdsRig(t)
	bad := expiredHold()
	bad.EventID = "E{1}"
	if _, err := h.Release(context.Background(), bad); err == nil {
		t.Error("an event id with braces must be refused")
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	defer func() { _ = rdb.Close() }()
	if _, err := NewHolds(rdb).Release(context.Background(), expiredHold()); err == nil {
		t.Error("a Redis error must be reported (the sweep counts it as ReleaseFailed)")
	}
}
