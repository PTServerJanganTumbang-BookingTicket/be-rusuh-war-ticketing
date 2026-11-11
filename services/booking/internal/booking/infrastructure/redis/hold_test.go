package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

func holdRig(t *testing.T) (*Holds, *miniredis.Miniredis, rediskeys.Event) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, err := rediskeys.NewEvent("evt_a")
	if err != nil {
		t.Fatal(err)
	}
	return NewHolds(rdb, 3, 6*time.Hour), mr, ev
}

func order(niks ...string) domain.HoldOrder {
	return domain.HoldOrder{Categories: []domain.CategoryQty{{CategoryID: "vip", Qty: int64(len(niks))}}, NIKs: niks}
}

func TestAHoldTakesTheStockAndLocksTheNIKsWithTheSaleWindowTTL(t *testing.T) {
	h, mr, ev := holdRig(t)
	_ = mr.Set(ev.Stock("vip"), "5")
	ans, err := h.Hold(context.Background(), "evt_a", order("h1", "h2"))
	if err != nil || !ans.Taken() || ans.Index != -1 {
		t.Fatalf("answer = %+v (%v), want HELD", ans, err)
	}
	if got, _ := mr.Get(ev.Stock("vip")); got != "3" {
		t.Fatalf("stock = %s, want 3", got)
	}
	if !mr.Exists(ev.NIKUsed("h1")) || !mr.Exists(ev.NIKUsed("h2")) || mr.TTL(ev.NIKUsed("h1")) != 6*time.Hour {
		t.Fatal("NIK locks missing or without the configured TTL")
	}
}

func TestEveryRefusalOfTheScriptIsPassedOnWithItsIndex(t *testing.T) {
	h, mr, ev := holdRig(t)
	ctx := context.Background()
	_ = mr.Set(ev.Stock("vip"), "0")
	if ans, err := h.Hold(ctx, "evt_a", order("h1")); err != nil || ans.Code != domain.HoldNoStock || ans.Index != 0 {
		t.Fatalf("no stock: %+v (%v)", ans, err)
	}
	_ = mr.Set(ev.Stock("vip"), "5")
	_ = mr.Set(ev.NIKUsed("h2"), "1")
	if ans, err := h.Hold(ctx, "evt_a", order("h1", "h2")); err != nil || ans.Code != domain.HoldNIKTaken || ans.Index != 1 {
		t.Fatalf("taken NIK: %+v (%v)", ans, err)
	}
	mr.Del(ev.Stock("vip"))
	if ans, err := h.Hold(ctx, "evt_a", order("h3")); err != nil || ans.Code != domain.HoldStockUninitialized || ans.Index != 0 {
		t.Fatalf("missing stock: %+v (%v)", ans, err)
	}
}

func TestAnOrderBeyondThePerOrderLimitIsAGuardAnswerNotAHold(t *testing.T) {
	h, mr, ev := holdRig(t) // limit 3
	_ = mr.Set(ev.Stock("vip"), "9")
	ans, err := h.Hold(context.Background(), "evt_a", order("a", "b", "c", "d"))
	if err != nil || ans.Taken() || ans.Code != "QTY_EXCEEDED" {
		t.Fatalf("answer = %+v (%v), want the QTY_EXCEEDED guard", ans, err)
	}
}

func TestReleaseGivesTheSeatsBackAndReportsAMissingStockKey(t *testing.T) {
	h, mr, ev := holdRig(t)
	ctx := context.Background()
	_ = mr.Set(ev.Stock("vip"), "5")
	_, _ = h.Hold(ctx, "evt_a", order("h1", "h2"))
	missing, err := h.Release(ctx, "evt_a", order("h1", "h2"))
	if err != nil || missing != 0 {
		t.Fatalf("missing = %d (%v)", missing, err)
	}
	if got, _ := mr.Get(ev.Stock("vip")); got != "5" || mr.Exists(ev.NIKUsed("h1")) {
		t.Fatal("stock or NIK locks not restored")
	}
	mr.Del(ev.Stock("vip"))
	if missing, err := h.Release(ctx, "evt_a", order("h1")); err != nil || missing != 1 || mr.Exists(ev.Stock("vip")) {
		t.Fatalf("missing = %d (%v), want 1 and the key not recreated", missing, err)
	}
}

func TestAnInvalidEventIdOrAnUnreachableRedisIsAnErrorNeverHeld(t *testing.T) {
	h, _, _ := holdRig(t)
	if _, err := h.Hold(context.Background(), "evt{bad}", order("h1")); err == nil {
		t.Error("an event id with braces must be refused")
	}
	if _, err := h.Release(context.Background(), "evt{bad}", order("h1")); err == nil {
		t.Error("an event id with braces must be refused")
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	defer func() { _ = rdb.Close() }()
	down := NewHolds(rdb, 3, time.Hour)
	if ans, err := down.Hold(context.Background(), "evt_a", order("h1")); err == nil || ans.Taken() {
		t.Fatalf("answer = %+v (%v), want an error", ans, err)
	}
	if _, err := down.Release(context.Background(), "evt_a", order("h1")); err == nil {
		t.Fatal("a failed release must be an error")
	}
}
