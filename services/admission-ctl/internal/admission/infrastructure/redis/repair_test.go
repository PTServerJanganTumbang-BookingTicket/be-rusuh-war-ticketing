package redis

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// Every test runs on miniredis and, when REDIS_ADDR is set (make verify), on the real cluster too.
type repairTarget struct {
	name string
	rdb  goredis.UniversalClient
}

func repairTargets(t *testing.T) []repairTarget {
	t.Helper()
	mr := miniredis.RunT(t)
	mini := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = mini.Close() })
	out := []repairTarget{{"miniredis", mini}}
	if addr := os.Getenv("REDIS_ADDR"); addr != "" && !testing.Short() {
		c := goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{addr}})
		t.Cleanup(func() { _ = c.Close() })
		out = append(out, repairTarget{"cluster", c})
	}
	return out
}

func eachRepair(t *testing.T, body func(t *testing.T, r *Repair, rdb goredis.UniversalClient, ev rediskeys.Event)) {
	t.Helper()
	for _, tg := range repairTargets(t) {
		t.Run(tg.name, func(t *testing.T) {
			id := fmt.Sprintf("evt_rep_%d", time.Now().UnixNano())
			ev, err := rediskeys.NewEvent(id)
			if err != nil {
				t.Fatal(err)
			}
			body(t, NewRepair(tg.rdb), tg.rdb, ev)
		})
	}
}

func TestLevelsReadsEveryCategoryAndReportsGoneAndUnreadableKeys(t *testing.T) {
	eachRepair(t, func(t *testing.T, r *Repair, rdb goredis.UniversalClient, ev rediskeys.Event) {
		_ = rdb.Set(ctx, ev.Stock("VIP"), "12", 0).Err()
		_ = rdb.Set(ctx, ev.Stock("TRIB"), "-1", 0).Err()
		_ = rdb.Set(ctx, ev.Stock("BAD"), "5.0", 0).Err() // not a plain integer: unreadable, like a missing key
		levels, missing, err := r.Levels(ctx, ev.ID(), []string{"VIP", "FEST", "TRIB", "BAD"})
		sort.Strings(missing)
		if err != nil || levels["VIP"] != 12 || levels["TRIB"] != -1 || len(levels) != 2 {
			t.Fatalf("levels %v (%v)", levels, err)
		}
		if len(missing) != 2 || missing[0] != "BAD" || missing[1] != "FEST" {
			t.Fatalf("missing %v, want BAD and FEST", missing)
		}
	})
}

func TestRestoreSetsAGoneKeyOnceAndNeverOverwritesOneThatCameBack(t *testing.T) {
	eachRepair(t, func(t *testing.T, r *Repair, rdb goredis.UniversalClient, ev rediskeys.Event) {
		if ok, err := r.Restore(ctx, ev.ID(), "VIP", 5); err != nil || !ok {
			t.Fatalf("restore: %v %v", ok, err)
		}
		_ = rdb.Set(ctx, ev.Stock("VIP"), "3", 0).Err() // a hold lowered it since
		if ok, err := r.Restore(ctx, ev.ID(), "VIP", 5); err != nil || ok {
			t.Fatalf("second restore: %v %v, want it to leave the key alone", ok, err)
		}
		if v, _ := rdb.Get(ctx, ev.Stock("VIP")).Int(); v != 3 {
			t.Fatalf("stock = %d, want 3 untouched", v)
		}
	})
}

func TestAdjustDelegatesToTheCompareAndAdjustScript(t *testing.T) {
	eachRepair(t, func(t *testing.T, r *Repair, rdb goredis.UniversalClient, ev rediskeys.Event) {
		_ = rdb.Set(ctx, ev.Stock("VIP"), "5", 0).Err()
		if res, err := r.Adjust(ctx, ev.ID(), "VIP", 5, 2); err != nil || res.Code != domain.AdjustAdjusted || res.Value != 7 {
			t.Fatalf("adjust: %+v (%v)", res, err)
		}
		if res, err := r.Adjust(ctx, ev.ID(), "VIP", 5, 2); err != nil || res.Code != domain.AdjustChanged || res.Value != 7 {
			t.Fatalf("a stale observation: %+v (%v)", res, err)
		}
		if res, err := r.Adjust(ctx, ev.ID(), "GONE", 5, 2); err != nil || res.Code != domain.AdjustMissing {
			t.Fatalf("a missing key: %+v (%v)", res, err)
		}
	})
}

func TestMissingLocksReturnsTheHashesWithoutAKey(t *testing.T) {
	eachRepair(t, func(t *testing.T, r *Repair, rdb goredis.UniversalClient, ev rediskeys.Event) {
		_ = rdb.Set(ctx, ev.NIKUsed("n1"), "1", time.Hour).Err()
		got, err := r.MissingLocks(ctx, ev.ID(), []string{"n1", "n2", "n3"})
		sort.Strings(got)
		if err != nil || len(got) != 2 || got[0] != "n2" || got[1] != "n3" {
			t.Fatalf("missing %v (%v), want n2 and n3", got, err)
		}
		if none, err := r.MissingLocks(ctx, ev.ID(), nil); err != nil || len(none) != 0 {
			t.Fatalf("no hashes: %v (%v)", none, err)
		}
	})
}

func TestRestoreLockSetsAnExpiringKeyOnceAndLeavesALiveOneAlone(t *testing.T) {
	eachRepair(t, func(t *testing.T, r *Repair, rdb goredis.UniversalClient, ev rediskeys.Event) {
		if ok, err := r.RestoreLock(ctx, ev.ID(), "n1", 90*time.Second); err != nil || !ok {
			t.Fatalf("restore: %v %v", ok, err)
		}
		if ttl, _ := rdb.TTL(ctx, ev.NIKUsed("n1")).Result(); ttl <= 0 || ttl > 90*time.Second {
			t.Fatalf("ttl %v, want a positive TTL of at most 90 s (a lock without an expiry would leak for ever)", ttl)
		}
		if ok, err := r.RestoreLock(ctx, ev.ID(), "n1", time.Hour); err != nil || ok {
			t.Fatalf("a live lock must be left alone: %v %v", ok, err)
		}
		if ttl, _ := rdb.TTL(ctx, ev.NIKUsed("n1")).Result(); ttl > 90*time.Second {
			t.Fatalf("ttl %v: the live lock's TTL was changed", ttl)
		}
	})
}

func TestCountOrphansCountsLocksWithoutAClaimOfThisEventOnlyAndDeletesNothing(t *testing.T) {
	eachRepair(t, func(t *testing.T, r *Repair, rdb goredis.UniversalClient, ev rediskeys.Event) {
		other, _ := rediskeys.NewEvent(ev.ID() + "_other")
		for _, h := range []string{"n1", "n2", "orphan1", "orphan2", "orphan3"} {
			_ = rdb.Set(ctx, ev.NIKUsed(h), "1", time.Hour).Err()
		}
		_ = rdb.Set(ctx, other.NIKUsed("elsewhere"), "1", time.Hour).Err() // another event: not ours
		n, err := r.CountOrphans(ctx, ev.ID(), map[string]struct{}{"n1": {}, "n2": {}})
		if err != nil || n != 3 {
			t.Fatalf("orphans = %d (%v), want 3", n, err)
		}
		for _, h := range []string{"orphan1", "orphan2", "orphan3"} {
			if ok, _ := rdb.Exists(ctx, ev.NIKUsed(h)).Result(); ok != 1 {
				t.Fatalf("orphan %s was deleted: it may belong to a hold that has not committed yet", h)
			}
		}
		if n, err := r.CountOrphans(ctx, ev.ID(), map[string]struct{}{"n1": {}, "n2": {}, "orphan1": {}, "orphan2": {}, "orphan3": {}}); err != nil || n != 0 {
			t.Fatalf("every lock claimed: %d (%v)", n, err)
		}
	})
}

func TestAnInvalidEventIdAndARedisErrorAreReported(t *testing.T) {
	r := NewRepair(goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond}))
	for name, call := range map[string]func() error{
		"levels":      func() error { _, _, err := r.Levels(ctx, "E1", []string{"VIP"}); return err },
		"restore":     func() error { _, err := r.Restore(ctx, "E1", "VIP", 1); return err },
		"adjust":      func() error { _, err := r.Adjust(ctx, "E1", "VIP", 1, 1); return err },
		"locks":       func() error { _, err := r.MissingLocks(ctx, "E1", []string{"n"}); return err },
		"restoreLock": func() error { _, err := r.RestoreLock(ctx, "E1", "n", time.Hour); return err },
		"orphans":     func() error { _, err := r.CountOrphans(ctx, "E1", nil); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: a Redis error must be reported", name)
		}
	}
	for name, call := range map[string]func() error{
		"levels":  func() error { _, _, err := r.Levels(ctx, "E{1}", []string{"VIP"}); return err },
		"restore": func() error { _, err := r.Restore(ctx, "E{1}", "VIP", 1); return err },
		"adjust":  func() error { _, err := r.Adjust(ctx, "E{1}", "VIP", 1, 1); return err },
		"locks":   func() error { _, err := r.MissingLocks(ctx, "E{1}", []string{"n"}); return err },
		"orphans": func() error { _, err := r.CountOrphans(ctx, "E{1}", nil); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: an event id with braces must be refused", name)
		}
	}
}

// A value that is not a plain integer is "missing" to the hold script (STOCK_UNINITIALIZED), but the key exists, so SET NX
// would do nothing and the category would stay dead without an alarm: Restore overwrites an unreadable value.
func TestRestoreOverwritesAValueThatIsNotAPlainIntegerButNeverAReadableOne(t *testing.T) {
	eachRepair(t, func(t *testing.T, r *Repair, rdb goredis.UniversalClient, ev rediskeys.Event) {
		for _, bad := range []string{"5.0", " 5", "abc", "0x5", ""} {
			_ = rdb.Set(ctx, ev.Stock("VIP"), bad, 0).Err()
			if ok, err := r.Restore(ctx, ev.ID(), "VIP", 5); err != nil || !ok {
				t.Fatalf("%q: restore = %v %v, want the unreadable value overwritten", bad, ok, err)
			}
			if v, _ := rdb.Get(ctx, ev.Stock("VIP")).Int(); v != 5 {
				t.Fatalf("%q: stock = %d, want 5", bad, v)
			}
		}
		_ = rdb.Set(ctx, ev.Stock("VIP"), "3", 0).Err() // a readable value, even a low one, is never overwritten
		if ok, _ := r.Restore(ctx, ev.ID(), "VIP", 5); ok {
			t.Fatal("a readable stock value was overwritten")
		}
	})
}
