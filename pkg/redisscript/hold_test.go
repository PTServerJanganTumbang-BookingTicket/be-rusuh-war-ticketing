package redisscript_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
)

// Every test runs on miniredis (which runs the real Lua) and, when REDIS_ADDR is set (make verify), on the real
// cluster too: the same keys must land in one slot there.
type target struct {
	name string
	rdb  goredis.UniversalClient
}

func targets(t *testing.T) []target {
	t.Helper()
	mr := miniredis.RunT(t)
	mini := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = mini.Close() })
	out := []target{{"miniredis", mini}}
	if addr := os.Getenv("REDIS_ADDR"); addr != "" && !testing.Short() {
		c := goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{addr}})
		t.Cleanup(func() { _ = c.Close() })
		out = append(out, target{"cluster", c})
	}
	return out
}

// each runs body once per target with a fresh event, so runs never share keys.
func each(t *testing.T, body func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event)) {
	t.Helper()
	for _, tg := range targets(t) {
		t.Run(tg.name, func(t *testing.T) {
			ev, err := rediskeys.NewEvent(fmt.Sprintf("evt_hold_%d", time.Now().UnixNano()))
			if err != nil {
				t.Fatal(err)
			}
			body(t, tg.rdb, ev)
		})
	}
}

var ctx = context.Background()

func seedStock(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event, levels map[string]int64) {
	t.Helper()
	for cat, n := range levels {
		if err := rdb.Set(ctx, ev.Stock(cat), n, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func stock(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event, cat string) int64 {
	t.Helper()
	n, err := rdb.Get(ctx, ev.Stock(cat)).Int64()
	if err != nil {
		t.Fatalf("stock %s: %v", cat, err)
	}
	return n
}

func nikHeld(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event, hmac string) bool {
	t.Helper()
	n, err := rdb.Exists(ctx, ev.NIKUsed(hmac)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func request(ev rediskeys.Event, cats []redisscript.CategoryQty, niks ...string) redisscript.HoldRequest {
	return redisscript.HoldRequest{Event: ev, Categories: cats, NIKs: niks, MaxTotal: 3, NIKTTL: 6 * time.Hour}
}

func mustHold(t *testing.T, rdb goredis.UniversalClient, req redisscript.HoldRequest) redisscript.HoldResult {
	t.Helper()
	res, err := redisscript.Hold(ctx, rdb, req)
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	return res
}

func TestThreeSeatsAcrossThreeCategoriesAreHeldTogether(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5, "fest": 5, "trib": 5})
		res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}, {"fest", 1}, {"trib", 1}}, "h1", "h2", "h3"))
		if res.Code != redisscript.HoldHeld {
			t.Fatalf("result = %+v, want HELD", res)
		}
		for _, c := range []string{"vip", "fest", "trib"} {
			if got := stock(t, rdb, ev, c); got != 4 {
				t.Errorf("stock %s = %d, want 4", c, got)
			}
		}
		for _, h := range []string{"h1", "h2", "h3"} {
			if !nikHeld(t, rdb, ev, h) {
				t.Errorf("NIK %s is not locked", h)
			}
		}
	})
}

func TestSeveralSeatsOfOneCategoryTakeThatManyAtOnce(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		if res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 3}}, "h1", "h2", "h3")); res.Code != redisscript.HoldHeld {
			t.Fatalf("result = %+v", res)
		}
		if got := stock(t, rdb, ev, "vip"); got != 2 {
			t.Fatalf("stock = %d, want 2", got)
		}
	})
}

func TestTheNIKLocksExpireWithTheConfiguredTTL(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		req := request(ev, []redisscript.CategoryQty{{"vip", 1}}, "h1")
		req.NIKTTL = 90 * time.Second
		mustHold(t, rdb, req)
		ttl, err := rdb.TTL(ctx, ev.NIKUsed("h1")).Result()
		if err != nil || ttl <= 0 || ttl > 90*time.Second {
			t.Fatalf("ttl = %v (%v), want a positive TTL of at most 90 s", ttl, err)
		}
		if v, _ := rdb.Get(ctx, ev.NIKUsed("h1")).Result(); v != "1" {
			t.Fatalf("lock value = %q, want 1", v)
		}
	})
}

// All or nothing: one short category holds nothing, not even the categories that had enough.
func TestOneShortCategoryHoldsNothing(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5, "fest": 0})
		res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}, {"fest", 1}}, "h1", "h2"))
		if res.Code != redisscript.HoldNoStock || res.Index != 1 {
			t.Fatalf("result = %+v, want NO_STOCK for category index 1", res)
		}
		if stock(t, rdb, ev, "vip") != 5 || stock(t, rdb, ev, "fest") != 0 {
			t.Fatal("stock changed on a refused hold")
		}
		if nikHeld(t, rdb, ev, "h1") || nikHeld(t, rdb, ev, "h2") {
			t.Fatal("a NIK was locked on a refused hold")
		}
	})
}

func TestNoStockNamesTheFirstShortCategory(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 0, "fest": 0, "trib": 5})
		res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}, {"fest", 1}, {"trib", 1}}, "h1", "h2", "h3"))
		if res.Code != redisscript.HoldNoStock || res.Index != 0 {
			t.Fatalf("result = %+v, want NO_STOCK for index 0", res)
		}
	})
}

// A key that is gone is "unknown", never "sold out" (ADR-005 §8), and it wins over a category that is merely short.
func TestAMissingStockKeyIsUninitializedNotSoldOutAndWinsOverAShortCategory(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 0}) // short; "fest" has no key
		res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}, {"fest", 1}}, "h1", "h2"))
		if res.Code != redisscript.HoldStockUninitialized || res.Index != 1 {
			t.Fatalf("result = %+v, want STOCK_UNINITIALIZED for index 1", res)
		}
		if n, _ := rdb.Exists(ctx, ev.Stock("fest")).Result(); n != 0 {
			t.Fatal("the script must not create a missing stock key")
		}
		if nikHeld(t, rdb, ev, "h1") {
			t.Fatal("a NIK was locked")
		}
	})
}

// The second NIK is already held by someone else: the first lock of this call is undone, theirs stays.
func TestATakenNIKUndoesTheLocksThisCallMadeAndLeavesTheOtherHoldersLock(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		if err := rdb.Set(ctx, ev.NIKUsed("h2"), "1", time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
		res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 3}}, "h1", "h2", "h3"))
		if res.Code != redisscript.HoldNIKTaken || res.Index != 1 {
			t.Fatalf("result = %+v, want NIK_TAKEN for seat index 1", res)
		}
		if nikHeld(t, rdb, ev, "h1") || nikHeld(t, rdb, ev, "h3") {
			t.Fatal("this call's locks were not undone")
		}
		if !nikHeld(t, rdb, ev, "h2") {
			t.Fatal("the other holder's lock was deleted")
		}
		if got := stock(t, rdb, ev, "vip"); got != 5 {
			t.Fatalf("stock = %d, want 5 (nothing taken)", got)
		}
	})
}

func TestATakenFirstNIKIsReportedAtIndexZero(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		_ = rdb.Set(ctx, ev.NIKUsed("h1"), "1", time.Hour).Err()
		res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}}, "h1"))
		if res.Code != redisscript.HoldNIKTaken || res.Index != 0 {
			t.Fatalf("result = %+v", res)
		}
	})
}

// Stock is checked before NIKs are locked, so a sold-out answer never leaves a NIK locked even briefly.
func TestStockIsCheckedBeforeAnyNIKIsLocked(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 0})
		_ = rdb.Set(ctx, ev.NIKUsed("h1"), "1", time.Hour).Err() // would be NIK_TAKEN if reached
		res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}}, "h1"))
		if res.Code != redisscript.HoldNoStock {
			t.Fatalf("result = %+v, want NO_STOCK before NIK_TAKEN", res)
		}
	})
}

func TestTheGuardsRefuseAMalformedCallWithoutChangingAnything(t *testing.T) {
	cases := []struct {
		name string
		cats []redisscript.CategoryQty
		niks []string
		max  int64
		want redisscript.HoldCode
	}{
		{"more seats than the order limit", []redisscript.CategoryQty{{"vip", 4}}, []string{"a", "b", "c", "d"}, 3, redisscript.HoldQtyExceeded},
		{"a zero quantity", []redisscript.CategoryQty{{"vip", 0}}, nil, 3, redisscript.HoldQtyInvalid},
		{"a negative quantity", []redisscript.CategoryQty{{"vip", -1}}, nil, 3, redisscript.HoldQtyInvalid},
		{"fewer NIKs than seats", []redisscript.CategoryQty{{"vip", 2}}, []string{"a"}, 3, redisscript.HoldNIKCountMismatch},
		{"more NIKs than seats", []redisscript.CategoryQty{{"vip", 1}}, []string{"a", "b"}, 3, redisscript.HoldNIKCountMismatch},
	}
	for _, c := range cases {
		each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
			seedStock(t, rdb, ev, map[string]int64{"vip": 9})
			req := request(ev, c.cats, c.niks...)
			req.MaxTotal = c.max
			res := mustHold(t, rdb, req)
			if res.Code != c.want {
				t.Errorf("%s: result = %+v, want %s", c.name, res, c.want)
			}
			if stock(t, rdb, ev, "vip") != 9 {
				t.Errorf("%s: stock changed", c.name)
			}
			for _, h := range c.niks {
				if nikHeld(t, rdb, ev, h) {
					t.Errorf("%s: NIK %s locked", c.name, h)
				}
			}
		})
	}
}

// The last guard: the same category listed twice passes the per-category check twice but would drive the stock below
// zero. Everything this call did is undone.
func TestADuplicatedCategoryThatWouldGoNegativeUndoesEverything(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 3})
		req := request(ev, []redisscript.CategoryQty{{"vip", 2}, {"vip", 2}}, "h1", "h2", "h3", "h4")
		req.MaxTotal = 4
		res := mustHold(t, rdb, req)
		if res.Code != redisscript.HoldStockNegative {
			t.Fatalf("result = %+v, want STOCK_NEGATIVE", res)
		}
		if got := stock(t, rdb, ev, "vip"); got != 3 {
			t.Fatalf("stock = %d, want 3 restored", got)
		}
		for _, h := range []string{"h1", "h2", "h3", "h4"} {
			if nikHeld(t, rdb, ev, h) {
				t.Fatalf("NIK %s still locked", h)
			}
		}
	})
}

func TestHoldTouchesNeitherTheQueueHashNorOtherEventKeys(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		if err := rdb.HSet(ctx, ev.QueueHash(), "state", "OPEN", "issued", "7", "now_serving", "3").Err(); err != nil {
			t.Fatal(err)
		}
		mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}}, "h1"))
		got, _ := rdb.HGetAll(ctx, ev.QueueHash()).Result()
		if len(got) != 3 || got["state"] != "OPEN" || got["issued"] != "7" || got["now_serving"] != "3" {
			t.Fatalf("queue hash = %v, want it untouched", got)
		}
	})
}

func TestReleaseGivesBackExactlyWhatTheHoldTook(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5, "fest": 5})
		_ = rdb.Set(ctx, ev.NIKUsed("other"), "1", time.Hour).Err()
		cats := []redisscript.CategoryQty{{"vip", 2}, {"fest", 1}}
		mustHold(t, rdb, request(ev, cats, "h1", "h2", "h3"))
		res, err := redisscript.Release(ctx, rdb, ev, cats, []string{"h1", "h2", "h3"})
		if err != nil || res.NIKsDeleted != 3 || res.StockMissing != 0 {
			t.Fatalf("release = %+v (%v), want 3 locks deleted and no missing stock key", res, err)
		}
		if stock(t, rdb, ev, "vip") != 5 || stock(t, rdb, ev, "fest") != 5 {
			t.Fatal("stock not restored")
		}
		for _, h := range []string{"h1", "h2", "h3"} {
			if nikHeld(t, rdb, ev, h) {
				t.Errorf("NIK %s still locked", h)
			}
		}
		if !nikHeld(t, rdb, ev, "other") {
			t.Fatal("release deleted a lock it did not own")
		}
	})
}

func TestAFreedNIKCanBeHeldAgain(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		cats := []redisscript.CategoryQty{{"vip", 1}}
		mustHold(t, rdb, request(ev, cats, "h1"))
		_, _ = redisscript.Release(ctx, rdb, ev, cats, []string{"h1"})
		if res := mustHold(t, rdb, request(ev, cats, "h1")); res.Code != redisscript.HoldHeld {
			t.Fatalf("result = %+v, want HELD again after a release", res)
		}
	})
}

func TestHoldAndReleaseRejectACallWithoutCategories(t *testing.T) {
	ev, _ := rediskeys.NewEvent("evt_x")
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	defer func() { _ = rdb.Close() }()
	if _, err := redisscript.Hold(ctx, rdb, redisscript.HoldRequest{Event: ev, MaxTotal: 3, NIKTTL: time.Hour}); err == nil {
		t.Error("Hold accepted a call without categories")
	}
	if _, err := redisscript.Release(ctx, rdb, ev, nil, nil); err == nil {
		t.Error("Release accepted a call without categories")
	}
	if _, err := redisscript.Release(ctx, rdb, ev, []redisscript.CategoryQty{{"v", 0}}, nil); err == nil {
		t.Error("Release accepted a quantity below one")
	}
	if _, err := redisscript.Hold(ctx, rdb, redisscript.HoldRequest{Event: ev, Categories: []redisscript.CategoryQty{{"v", 1}}, NIKs: []string{"a"}, NIKTTL: time.Hour}); err == nil {
		t.Error("Hold accepted a zero per-order limit (every order would be QTY_EXCEEDED)")
	}
	if _, err := redisscript.Hold(ctx, rdb, redisscript.HoldRequest{Event: ev, Categories: []redisscript.CategoryQty{{"v", 1}}, NIKs: []string{"a"}, MaxTotal: 3}); err == nil {
		t.Error("Hold accepted a NIK lock without a TTL (a leaked lock would live for ever)")
	}
}

func TestAnUnreachableRedisIsAnErrorNeverAHeldAnswer(t *testing.T) {
	ev, _ := rediskeys.NewEvent("evt_x")
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	defer func() { _ = rdb.Close() }()
	res, err := redisscript.Hold(ctx, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}}, "h1"))
	if err == nil || res.Code == redisscript.HoldHeld {
		t.Fatalf("result = %+v err = %v, want an error", res, err)
	}
	if _, err := redisscript.Release(ctx, rdb, ev, []redisscript.CategoryQty{{"vip", 1}}, []string{"h1"}); err == nil {
		t.Fatal("Release hid a Redis error")
	}
}

// Twenty bookings race for five seats, each with its own NIK: exactly five win, the stock ends at 0, never below.
func TestTwentyBookingsRacingForFiveSeatsLetExactlyFiveWin(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			held int
			none int
		)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := redisscript.Hold(ctx, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}}, fmt.Sprintf("racer-%d", i)))
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err != nil:
					t.Errorf("racer %d: %v", i, err)
				case res.Code == redisscript.HoldHeld:
					held++
				case res.Code == redisscript.HoldNoStock:
					none++
				default:
					t.Errorf("racer %d: %+v", i, res)
				}
			}()
		}
		wg.Wait()
		if held != 5 || none != 15 || stock(t, rdb, ev, "vip") != 0 {
			t.Fatalf("held=%d refused=%d stock=%d, want 5 / 15 / 0", held, none, stock(t, rdb, ev, "vip"))
		}
	})
}

// Ten bookings with the same NIK: exactly one wins, and the others take no stock.
func TestTenBookingsWithTheSameNIKLetExactlyOneWin(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 20})
		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			held int
		)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := redisscript.Hold(ctx, rdb, request(ev, []redisscript.CategoryQty{{"vip", 1}}, "same-nik"))
				mu.Lock()
				defer mu.Unlock()
				if err != nil || (res.Code != redisscript.HoldHeld && res.Code != redisscript.HoldNIKTaken) {
					t.Errorf("racer %d: %+v %v", i, res, err)
				}
				if err == nil && res.Code == redisscript.HoldHeld {
					held++
				}
			}()
		}
		wg.Wait()
		if held != 1 || stock(t, rdb, ev, "vip") != 19 {
			t.Fatalf("held=%d stock=%d, want 1 winner and 19 left", held, stock(t, rdb, ev, "vip"))
		}
	})
}

// A stock value that is not a plain integer (a float, text, hex) is unreadable, not "enough": DECRBY would refuse it
// after the NIKs were locked, so it must be caught before anything is written.
func TestAStockValueThatIsNotAnIntegerIsUninitializedAndNothingIsWritten(t *testing.T) {
	for _, bad := range []string{"5.0", " 5", "0x5", "1e1", "abc", ""} {
		each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
			seedStock(t, rdb, ev, map[string]int64{"fest": 5})
			if err := rdb.Set(ctx, ev.Stock("vip"), bad, 0).Err(); err != nil {
				t.Fatal(err)
			}
			res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"fest", 1}, {"vip", 1}}, "h1", "h2"))
			if res.Code != redisscript.HoldStockUninitialized || res.Index != 1 {
				t.Fatalf("stock %q: result = %+v, want STOCK_UNINITIALIZED for index 1", bad, res)
			}
			if stock(t, rdb, ev, "fest") != 5 || nikHeld(t, rdb, ev, "h1") || nikHeld(t, rdb, ev, "h2") {
				t.Fatalf("stock %q: something was written", bad)
			}
		})
	}
}

func TestTheSameNIKTwiceInOneOrderIsTakenAtTheSecondSeatAndTheFirstLockIsUndone(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"vip": 5})
		res := mustHold(t, rdb, request(ev, []redisscript.CategoryQty{{"vip", 2}}, "same", "same"))
		if res.Code != redisscript.HoldNIKTaken || res.Index != 1 {
			t.Fatalf("result = %+v, want NIK_TAKEN at seat 1", res)
		}
		if nikHeld(t, rdb, ev, "same") || stock(t, rdb, ev, "vip") != 5 {
			t.Fatal("the first lock or the stock was not undone")
		}
	})
}

// A lost stock key must stay lost: releasing into it would make up a count and sell seats nobody counted (B5 repairs it).
func TestReleaseDoesNotRecreateAMissingStockKeyButStillFreesTheNIKs(t *testing.T) {
	each(t, func(t *testing.T, rdb goredis.UniversalClient, ev rediskeys.Event) {
		seedStock(t, rdb, ev, map[string]int64{"fest": 5})
		_ = rdb.Set(ctx, ev.NIKUsed("h1"), "1", time.Hour).Err()
		cats := []redisscript.CategoryQty{{"vip", 2}, {"fest", 1}} // "vip" has no key
		res, err := redisscript.Release(ctx, rdb, ev, cats, []string{"h1"})
		if err != nil || res.StockMissing != 1 || res.NIKsDeleted != 1 {
			t.Fatalf("release = %+v (%v), want 1 missing stock key and 1 lock deleted", res, err)
		}
		if n, _ := rdb.Exists(ctx, ev.Stock("vip")).Result(); n != 0 {
			t.Fatal("release recreated the missing stock key")
		}
		if stock(t, rdb, ev, "fest") != 6 || nikHeld(t, rdb, ev, "h1") {
			t.Fatal("the other category was not given back, or the lock stayed")
		}
	})
}
