package redisscript_test

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/specsuite"
)

func TestFeatures(t *testing.T) { specsuite.Run(t, initScenario) }

type world struct {
	ctx    context.Context
	rdb    *goredis.Client
	script *redisscript.Script
	ev     rediskeys.Event
	cats   []string
	now    int64
	noCats bool // "the script runs with no stock keys"

	res     redisscript.Result
	err     error
	before  map[string]string
	seen    int64
	ctrl    map[string]redisscript.Result
	sha     [2]string
	loadErr error
}

func initScenario(sc *godog.ScenarioContext) {
	w := &world{ctx: context.Background()}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return ctx, nil
	})

	sc.Step(`^a clean Redis cluster$`, w.cleanRedis)
	sc.Step(`^event "[^"]*" has stock categories "([^"]*)" and "([^"]*)"$`, func(a, b string) error { w.cats = []string{a, b}; return nil })
	sc.Step(`^the clock reads epoch (\d+)$`, func(n int64) error { w.now = n; return nil })

	// event state
	sc.Step(`^event "[^"]*" is "([A-Z_]+)" with now_serving (\d+) and issued (\d+)$`, func(st string, ns, is int64) error {
		return w.seed(st, &ns, &is, 0)
	})
	sc.Step(`^event "[^"]*" has state (.+) with now_serving (\d+) and issued (\d+)$`, func(st string, ns, is int64) error {
		if st == "missing" {
			return w.seed("", &ns, &is, 0)
		}
		return w.seed(strings.Trim(st, `"`), &ns, &is, 0)
	})
	sc.Step(`^event "[^"]*" is "OPEN" with now_serving (\d+) and no issued key$`, func(ns int64) error { return w.seed("OPEN", &ns, nil, 0) })
	sc.Step(`^event "[^"]*" is "OPEN" with no now_serving field and issued (\d+)$`, func(is int64) error { return w.seed("OPEN", nil, &is, 0) })
	sc.Step(`^event "[^"]*" is "FULL_BOOKED" since (\d+) with now_serving (\d+) and issued (\d+)$`, func(since, ns, is int64) error {
		return w.seed("FULL_BOOKED", &ns, &is, since)
	})
	sc.Step(`^queue:\{E1\} has free_slots (\d+) and free_slots_at (\d+)$`, func(f, at int64) error {
		return w.rdb.HSet(w.ctx, w.ev.QueueHash(), rediskeys.FieldFreeSlots, f, rediskeys.FieldFreeSlotsAt, at).Err()
	})

	// by_ticket and stock
	sc.Step(`^customers hold ticket numbers (.+) in by_ticket$`, func(spec string) error {
		nums, err := parseNumbers(spec)
		if err != nil {
			return err
		}
		pipe := w.rdb.Pipeline()
		for _, n := range nums {
			pipe.ZAdd(w.ctx, w.ev.ByTicket(), goredis.Z{Score: float64(n), Member: member(n)})
		}
		_, err = pipe.Exec(w.ctx)
		return err
	})
	sc.Step(`^tickets (.+) are pending grants$`, func(string) error { return nil }) // score <= now_serving already says so
	sc.Step(`^total stock is (\d+) split as VIP (\d+) and CAT1 (\d+)$`, func(total, vip, cat1 int64) error {
		if vip+cat1 != total {
			return fmt.Errorf("stock split %d + %d is not %d", vip, cat1, total)
		}
		return w.setStock(vip, cat1)
	})
	sc.Step(`^total stock is (\d+)$`, func(n int64) error { return w.setStock(n, 0) })
	sc.Step(`^stock VIP is (\d+) and stock CAT1 key is missing$`, func(vip int64) error {
		return w.rdb.Set(w.ctx, w.ev.Stock("VIP"), vip, 0).Err()
	})
	sc.Step(`^a new customer joins and receives ticket number (\d+)$`, w.newCustomer)

	// running
	sc.Step(`^the script runs with MAX (\d+), open (\d+), browsing (\d+), seen now_serving (\d+)$`, func(max, open, browsing, seen int64) error {
		w.run(max, open, browsing, seen)
		return nil
	})
	sc.Step(`^the script runs with no stock keys$`, func() error {
		w.noCats = true
		w.run(1000, 0, 0, 0)
		return nil
	})
	sc.Step(`^the script runs with raw arguments (.+)$`, w.runRaw)
	sc.Step(`^controllers A and B both read now_serving (\d+)$`, func(n int64) error { w.seen = n; w.ctrl = map[string]redisscript.Result{}; return nil })
	sc.Step(`^controller ([AB]) runs the script with MAX (\d+), open (\d+), browsing (\d+)$`, func(c string, max, open, browsing int64) error {
		w.run(max, open, browsing, w.seen)
		w.ctrl[c] = w.res
		return w.err
	})
	sc.Step(`^([AB]) gets "([A-Z_]+)" with tickets (.+)$`, func(c, code, spec string) error {
		if err := eq("result of "+c, w.ctrl[c].Code, code); err != nil {
			return err
		}
		return tokensAre(w.ctrl[c].Tokens, spec)
	})
	sc.Step(`^([AB]) gets "([A-Z_]+)"$`, func(c, code string) error { return eq("result of "+c, w.ctrl[c].Code, code) })

	// results
	sc.Step(`^the result is "([A-Z_]+)"$`, func(code string) error {
		if w.err != nil {
			return w.err
		}
		return eq("result", w.res.Code, code)
	})
	sc.Step(`^now_serving is (\d+)$`, func(n int64) error { return w.field("now_serving", n) })
	sc.Step(`^free_slots is (\d+) and free_slots_at is (\d+)$`, func(f, at int64) error {
		if err := w.field(rediskeys.FieldFreeSlots, f); err != nil {
			return err
		}
		return w.field(rediskeys.FieldFreeSlotsAt, at)
	})
	sc.Step(`^free_slots is (\d+)$`, func(f int64) error { return w.field(rediskeys.FieldFreeSlots, f) })
	sc.Step(`^from is (\d+) and to is (\d+)$`, func(from, to int64) error {
		if w.res.From != from || w.res.To != to {
			return fmt.Errorf("from/to = %d/%d, want %d/%d", w.res.From, w.res.To, from, to)
		}
		return nil
	})
	sc.Step(`^the state is (?:still )?"([A-Z_]+)"$`, func(st string) error {
		got, err := w.rdb.HGet(w.ctx, w.ev.QueueHash(), rediskeys.FieldState).Result()
		if err != nil {
			return err
		}
		return eq("state", got, st)
	})
	sc.Step(`^full_booked_at is not set$`, func() error {
		if v, err := w.rdb.HGet(w.ctx, w.ev.QueueHash(), rediskeys.FieldFullBookedAt).Result(); !errors.Is(err, goredis.Nil) {
			return fmt.Errorf("full_booked_at = %q (err %v), want not set", v, err)
		}
		return nil
	})
	sc.Step(`^full_booked_at is (\d+)$`, func(n int64) error { return w.field(rediskeys.FieldFullBookedAt, n) })
	sc.Step(`^(\d+) tokens are returned$`, func(n int) error {
		if len(w.res.Tokens) != n {
			return fmt.Errorf("%d tokens returned, want %d", len(w.res.Tokens), n)
		}
		return nil
	})
	sc.Step(`^no tokens are returned$`, func() error {
		if len(w.res.Tokens) != 0 {
			return fmt.Errorf("tokens returned: %v", w.res.Tokens)
		}
		return nil
	})
	sc.Step(`^the returned tokens are tickets? (.+?)(?: in ascending order)?$`, func(spec string) error { return tokensAre(w.res.Tokens, spec) })
	sc.Step(`^none of tickets (.+) are returned$`, func(spec string) error {
		nums, err := parseNumbers(spec)
		if err != nil {
			return err
		}
		for _, n := range nums {
			for _, tok := range w.res.Tokens {
				if tok == member(n) {
					return fmt.Errorf("ticket %d was returned again", n)
				}
			}
		}
		return nil
	})
	sc.Step(`^by_ticket still holds (\d+) members$`, func(n int64) error {
		got, err := w.rdb.ZCard(w.ctx, w.ev.ByTicket()).Result()
		if err != nil {
			return err
		}
		return eq("ZCARD by_ticket", got, n)
	})
	sc.Step(`^ZCOUNT by_ticket -inf now_serving is (\d+)$`, func(n int64) error {
		ns, err := w.rdb.HGet(w.ctx, w.ev.QueueHash(), rediskeys.FieldNowServing).Int64()
		if err != nil {
			return err
		}
		got, err := w.rdb.ZCount(w.ctx, w.ev.ByTicket(), "-inf", strconv.FormatInt(ns, 10)).Result()
		if err != nil {
			return err
		}
		return eq("ZCOUNT", got, n)
	})
	sc.Step(`^no key of event "[^"]*" changed$`, func() error { return w.unchanged(nil) })
	sc.Step(`^no key of event "[^"]*" changed except free_slots and free_slots_at$`, func() error {
		return w.unchanged([]string{rediskeys.FieldFreeSlots, rediskeys.FieldFreeSlotsAt})
	})

	// input safety, loading, cluster
	sc.Step(`^the script returns an error containing "([^"]*)"$`, func(sub string) error {
		if w.err == nil || !strings.Contains(w.err.Error(), sub) {
			return fmt.Errorf("error = %v, want one containing %q", w.err, sub)
		}
		return nil
	})
	sc.Step(`^no CROSSSLOT error is raised$`, func() error {
		if w.err != nil {
			return fmt.Errorf("script failed: %w", w.err)
		}
		return nil
	})
	sc.Step(`^the script is loaded and its SHA is known$`, func() error {
		var err error
		w.sha[0], err = w.script.Load(w.ctx)
		return err
	})
	sc.Step(`^Redis runs SCRIPT FLUSH$`, func() error { return w.rdb.ScriptFlush(w.ctx).Err() })
	sc.Step(`^the loader gets NOSCRIPT, loads the script again and the call succeeds$`, func() error {
		if w.err != nil {
			return w.err
		}
		if w.script.Reloads() != 1 {
			return fmt.Errorf("script reloaded %d times, want 1", w.script.Reloads())
		}
		return nil
	})
	sc.Step(`^the script is loaded twice$`, func() error {
		for i := range w.sha {
			w.sha[i], w.loadErr = w.script.Load(w.ctx)
			if w.loadErr != nil {
				return w.loadErr
			}
		}
		return nil
	})
	sc.Step(`^both loads return the same SHA1$`, func() error {
		if w.sha[0] == "" || w.sha[0] != w.sha[1] {
			return fmt.Errorf("SHAs differ or are empty: %q %q", w.sha[0], w.sha[1])
		}
		sum := sha1.Sum([]byte(redisscript.Source()))
		return eq("SHA1 of the source", w.sha[0], hex.EncodeToString(sum[:]))
	})
}

func (w *world) cleanRedis() error {
	w.rdb = goredis.NewClient(&goredis.Options{Addr: specsuite.RedisAddr()})
	info, err := w.rdb.ClusterInfo(w.ctx).Result()
	if err != nil {
		return fmt.Errorf("CLUSTER INFO: %w (is Redis running in cluster mode? make deps-up)", err)
	}
	if !strings.Contains(info, "cluster_state:ok") {
		return fmt.Errorf("cluster not ready: %s", info)
	}
	// A unique event id per scenario keeps parallel packages out of each other's keys.
	w.ev, err = rediskeys.NewEvent(fmt.Sprintf("script-%d", time.Now().UnixNano()))
	if err != nil {
		return err
	}
	w.script = redisscript.New(w.rdb)
	w.noCats = false
	return nil
}

func (w *world) cleanup() {
	if w.rdb == nil {
		return
	}
	keys := []string{w.ev.QueueHash(), w.ev.ByTicket()}
	for _, c := range w.cats {
		keys = append(keys, w.ev.Stock(c))
	}
	_ = w.rdb.Del(w.ctx, keys...).Err()
	_ = w.rdb.Close()
}

// seed writes the queue:{ev} hash. state "" leaves the field out; nil ns/issued leave theirs out.
func (w *world) seed(state string, ns, issued *int64, fullBookedAt int64) error {
	f := map[string]any{}
	if state != "" {
		f[rediskeys.FieldState] = state
	}
	if ns != nil {
		f[rediskeys.FieldNowServing] = *ns
	}
	if issued != nil {
		f[rediskeys.FieldIssued] = *issued
	}
	if fullBookedAt > 0 {
		f[rediskeys.FieldFullBookedAt] = fullBookedAt
	}
	if len(f) == 0 {
		return nil
	}
	return w.rdb.HSet(w.ctx, w.ev.QueueHash(), f).Err()
}

func (w *world) setStock(vip, cat1 int64) error {
	if err := w.rdb.Set(w.ctx, w.ev.Stock("VIP"), vip, 0).Err(); err != nil {
		return err
	}
	return w.rdb.Set(w.ctx, w.ev.Stock("CAT1"), cat1, 0).Err()
}

func (w *world) newCustomer(want int64) error {
	n, err := w.rdb.HIncrBy(w.ctx, w.ev.QueueHash(), rediskeys.FieldIssued, 1).Result()
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("new customer got ticket %d, want %d", n, want)
	}
	return w.rdb.ZAdd(w.ctx, w.ev.ByTicket(), goredis.Z{Score: float64(n), Member: member(n)}).Err()
}

func (w *world) keys() []string {
	keys := []string{w.ev.QueueHash(), w.ev.ByTicket()}
	if w.noCats {
		return keys
	}
	for _, c := range w.cats {
		keys = append(keys, w.ev.Stock(c))
	}
	return keys
}

func (w *world) run(max, open, browsing, seen int64) {
	w.snapshot()
	w.res, w.err = w.script.Run(w.ctx, w.keys(), max, open, browsing, seen, w.now)
}

func (w *world) runRaw(spec string) error {
	var argv []any
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(spec, -1) {
		argv = append(argv, m[1])
	}
	w.snapshot()
	w.res, w.err = w.script.Run(w.ctx, w.keys(), argv...)
	return nil
}

// snapshot records every key of the event so "no key changed" can compare.
func (w *world) snapshot() { w.before = w.dump() }

func (w *world) dump() map[string]string {
	out := map[string]string{}
	h, err := w.rdb.HGetAll(w.ctx, w.ev.QueueHash()).Result()
	if err != nil {
		out["error.hgetall"] = err.Error()
	}
	for k, v := range h {
		out["queue."+k] = v
	}
	z, err := w.rdb.ZRangeWithScores(w.ctx, w.ev.ByTicket(), 0, -1).Result()
	if err != nil {
		out["error.zrange"] = err.Error()
	}
	for _, m := range z {
		out[fmt.Sprintf("by_ticket.%v", m.Member)] = fmt.Sprint(m.Score)
	}
	for _, c := range w.cats {
		if v, err := w.rdb.Get(w.ctx, w.ev.Stock(c)).Result(); err == nil {
			out["stock."+c] = v
		}
	}
	return out
}

func (w *world) unchanged(except []string) error {
	drop := func(m map[string]string) {
		for _, f := range except {
			delete(m, "queue."+f)
		}
	}
	after := w.dump()
	before := map[string]string{}
	for k, v := range w.before {
		before[k] = v
	}
	drop(after)
	drop(before)
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("keys changed:\n before %v\n after  %v", sorted(before), sorted(after))
	}
	return nil
}

func (w *world) field(name string, want int64) error {
	v, err := w.rdb.HGet(w.ctx, w.ev.QueueHash(), name).Result()
	if errors.Is(err, goredis.Nil) && name == rediskeys.FieldNowServing {
		v = "0" // a missing now_serving counts as 0
	} else if err != nil {
		return fmt.Errorf("HGET %s: %w", name, err)
	}
	return eq(name, v, strconv.FormatInt(want, 10))
}

func member(n int64) string { return fmt.Sprintf("t%d", n) }

// parseNumbers reads "1 to 10", "11" or "1, 2, 4 and 5".
func parseNumbers(spec string) ([]int64, error) {
	spec = strings.TrimSpace(spec)
	if m := regexp.MustCompile(`^(\d+) to (\d+)$`).FindStringSubmatch(spec); m != nil {
		a, _ := strconv.ParseInt(m[1], 10, 64)
		b, _ := strconv.ParseInt(m[2], 10, 64)
		var out []int64
		for n := a; n <= b; n++ {
			out = append(out, n)
		}
		return out, nil
	}
	var out []int64
	for _, p := range regexp.MustCompile(`\d+`).FindAllString(spec, -1) {
		n, _ := strconv.ParseInt(p, 10, 64)
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no ticket numbers in %q", spec)
	}
	return out, nil
}

func tokensAre(got []string, spec string) error {
	nums, err := parseNumbers(spec)
	if err != nil {
		return err
	}
	want := make([]string, len(nums))
	for i, n := range nums {
		want[i] = member(n)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("tokens = %v, want %v", got, want)
	}
	return nil
}

func eq[T comparable](what string, got, want T) error {
	if got != want {
		return fmt.Errorf("%s = %v, want %v", what, got, want)
	}
	return nil
}

func sorted(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
