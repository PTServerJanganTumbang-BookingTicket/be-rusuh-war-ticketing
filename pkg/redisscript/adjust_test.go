package redisscript_test

import (
	"sync"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
)

func adjust(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event, observed, delta int64) redisscript.AdjustResult {
	t.Helper()
	res, err := redisscript.AdjustIfUnchanged(ctx, rdb, ev, "vip", observed, delta)
	if err != nil {
		t.Fatalf("AdjustIfUnchanged: %v", err)
	}
	return res
}

func TestAnUnchangedCounterIsAdjustedByTheDelta(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		if res := adjust(t, rdb, ev, 5, 3); res.Code != redisscript.AdjustAdjusted || res.Value != 8 || stock(t, rdb, ev, "vip") != 8 {
			t.Fatalf("result = %+v stock = %d, want ADJUSTED to 8", res, stock(t, rdb, ev, "vip"))
		}
		if res := adjust(t, rdb, ev, 8, -2); res.Code != redisscript.AdjustAdjusted || res.Value != 6 {
			t.Fatalf("a negative delta: %+v, want 6", res)
		}
	})
}

// The point of the script: a counter that moved since it was read is left alone, so a double run never corrects twice.
func TestACounterThatMovedSinceItWasReadIsLeftAlone(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 7})
		res := adjust(t, rdb, ev, 5, 3) // read as 5, but it is 7 now
		if res.Code != redisscript.AdjustChanged || res.Value != 7 || stock(t, rdb, ev, "vip") != 7 {
			t.Fatalf("result = %+v stock = %d, want CHANGED with the current 7 and nothing written", res, stock(t, rdb, ev, "vip"))
		}
	})
}

func TestTwoRunsWithTheSameObservedValueApplyOnce(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		first, second := adjust(t, rdb, ev, 5, 2), adjust(t, rdb, ev, 5, 2)
		if first.Code != redisscript.AdjustAdjusted || second.Code != redisscript.AdjustChanged || stock(t, rdb, ev, "vip") != 7 {
			t.Fatalf("first %+v second %+v stock %d, want one correction (7), not two", first, second, stock(t, rdb, ev, "vip"))
		}
	})
}

func TestARacingPairOfReconcilersAppliesTheCorrectionOnce(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		var wg sync.WaitGroup
		var mu sync.Mutex
		applied := 0
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := redisscript.AdjustIfUnchanged(ctx, rdb, ev, "vip", 5, 4)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					t.Error(err)
				} else if res.Code == redisscript.AdjustAdjusted {
					applied++
				}
			}()
		}
		wg.Wait()
		if applied != 1 || stock(t, rdb, ev, "vip") != 9 {
			t.Fatalf("applied %d times, stock %d, want once and 9", applied, stock(t, rdb, ev, "vip"))
		}
	})
}

func TestAMissingOrUnreadableKeyIsReportedNeverCreated(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		if res := adjust(t, rdb, ev, 5, 1); res.Code != redisscript.AdjustMissing || res.Value != -1 {
			t.Fatalf("missing key: %+v", res)
		}
		if n, _ := rdb.Exists(ctx, ev.Stock("vip")).Result(); n != 0 {
			t.Fatal("the script must not create a missing key")
		}
		if err := rdb.Set(ctx, ev.Stock("vip"), "5.0", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if res := adjust(t, rdb, ev, 5, 1); res.Code != redisscript.AdjustMissing {
			t.Fatalf("a value that is not an integer is unreadable, like a missing key: %+v", res)
		}
	})
}

func TestACorrectionBelowZeroIsRefusedAndWritesNothing(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 3})
		res := adjust(t, rdb, ev, 3, -4)
		if res.Code != redisscript.AdjustBelowZero || res.Value != 3 || stock(t, rdb, ev, "vip") != 3 {
			t.Fatalf("result = %+v stock = %d, want BELOW_ZERO and 3 untouched", res, stock(t, rdb, ev, "vip"))
		}
		if res := adjust(t, rdb, ev, 3, -3); res.Code != redisscript.AdjustAdjusted || res.Value != 0 {
			t.Fatalf("down to exactly zero is allowed: %+v", res)
		}
	})
}

func TestTheAdjustScriptWritesOnlyItsOwnKey(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5, "fest": 5})
		_ = rdb.HSet(ctx, ev.QueueHash(), "state", "OPEN").Err()
		adjust(t, rdb, ev, 5, 2)
		if stock(t, rdb, ev, "fest") != 5 {
			t.Fatal("another category changed")
		}
		if got, _ := rdb.HGet(ctx, ev.QueueHash(), "state").Result(); got != "OPEN" {
			t.Fatal("the queue hash changed")
		}
	})
}
