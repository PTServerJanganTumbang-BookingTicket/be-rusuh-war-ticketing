package ratelimit_test

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/specsuite"
)

func TestFeatures(t *testing.T) { specsuite.Run(t, initScenario) }

// joinLimit is the per-IP join limit from the contract (20 / 5 min).
var joinLimit = ratelimit.Limit{Capacity: 20, Refill: 20, Per: 300 * time.Second}

type world struct {
	rdb    goredis.UniversalClient
	lim    *ratelimit.Limiter
	suffix string // per-scenario, so parallel package runs never share a bucket
	key    string
	limit  ratelimit.Limit
	last   ratelimit.Result
	counts int64
}

var scenarioSeq atomic.Int64

func initScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.rdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{specsuite.RedisAddr()}})
		w.lim = ratelimit.New(w.rdb)
		w.suffix = ":t" + strconv.FormatInt(time.Now().UnixNano(), 36) + strconv.FormatInt(scenarioSeq.Add(1), 36)
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.rdb != nil {
			_ = w.rdb.Close()
		}
		return ctx, err
	})
	sc.Step(`^a clean Redis cluster$`, w.cleanCluster)
	sc.Step(`^a limiter with capacity (\d+) and refill (\d+) per (\d+) s on key "([^"]*)"$`, w.limiter)
	sc.Step(`^(\d+) checks run at once$`, w.parallel)
	sc.Step(`^(\d+) checks run in parallel$`, w.parallel)
	sc.Step(`^all (\d+) are allowed$`, w.allowedCount)
	sc.Step(`^exactly (\d+) are allowed$`, w.allowedCount)
	sc.Step(`^a 21st check runs$`, w.oneCheck)
	sc.Step(`^one check runs$`, w.oneCheck)
	sc.Step(`^it is refused with retry_after (\d+) s$`, w.refusedWithRetry)
	sc.Step(`^the bucket was emptied$`, w.emptyBucket)
	sc.Step(`^(\d+) seconds pass$`, func(s int) error { return w.timePasses(time.Duration(s) * time.Second) })
	sc.Step(`^1 hour passes$`, func() error { return w.timePasses(time.Hour) })
	sc.Step(`^exactly (\d+) checks? (?:is|are) allowed$`, w.exactlyAllowedNow)
	sc.Step(`^the key expires within (\d+) s$`, w.expiresWithin)
	sc.Step(`^IP "([^"]*)" has an empty bucket$`, w.ipEmpty)
	sc.Step(`^IP "([^"]*)" is still allowed$`, w.ipAllowed)
}

func (w *world) cleanCluster() error {
	info, err := w.rdb.ClusterInfo(context.Background()).Result()
	if err != nil {
		return fmt.Errorf("CLUSTER INFO: %w (make deps-up)", err)
	}
	if !strings.Contains(info, "cluster_state:ok") {
		return fmt.Errorf("cluster not ready: %s", info)
	}
	return nil
}

func (w *world) limiter(capacity, refill, perSeconds int, key string) error {
	w.limit = ratelimit.Limit{Capacity: capacity, Refill: refill, Per: time.Duration(perSeconds) * time.Second}
	w.key = key + w.suffix
	return w.rdb.Del(context.Background(), w.key).Err()
}

func (w *world) check() (ratelimit.Result, error) {
	return w.lim.Allow(context.Background(), w.key, w.limit)
}

func (w *world) parallel(n int) error {
	var wg sync.WaitGroup
	var allowed atomic.Int64
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := w.check()
			if err != nil {
				errs <- err
				return
			}
			if r.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	w.counts = allowed.Load()
	return nil
}

func (w *world) allowedCount(n int) error {
	if w.counts != int64(n) {
		return fmt.Errorf("%d allowed, want %d", w.counts, n)
	}
	return nil
}

func (w *world) oneCheck() error {
	r, err := w.check()
	w.last = r
	return err
}

func (w *world) refusedWithRetry(seconds int) error {
	if w.last.Allowed {
		return fmt.Errorf("check was allowed, want refused")
	}
	got := int(math.Ceil(w.last.RetryAfter.Seconds()))
	if got != seconds {
		return fmt.Errorf("retry_after = %v (%d s), want %d s", w.last.RetryAfter, got, seconds)
	}
	return nil
}

func (w *world) emptyBucket() error {
	for {
		r, err := w.check()
		if err != nil {
			return err
		}
		if !r.Allowed {
			return nil
		}
	}
}

// timePasses moves the bucket's last-refill timestamp into the past, which is
// what elapsed Redis TIME looks like to the script.
func (w *world) timePasses(d time.Duration) error {
	return w.rdb.HIncrBy(context.Background(), w.key, ratelimit.FieldTimestamp, -d.Microseconds()).Err()
}

func (w *world) exactlyAllowedNow(n int) error {
	allowed := 0
	for range n + 5 {
		r, err := w.check()
		if err != nil {
			return err
		}
		if !r.Allowed {
			break
		}
		allowed++
	}
	if allowed != n {
		return fmt.Errorf("%d checks allowed, want exactly %d", allowed, n)
	}
	return nil
}

func (w *world) expiresWithin(seconds int) error {
	ttl, err := w.rdb.PTTL(context.Background(), w.key).Result()
	if err != nil {
		return err
	}
	if ttl <= 0 || ttl > time.Duration(seconds)*time.Second {
		return fmt.Errorf("PTTL = %v, want in (0, %ds]", ttl, seconds)
	}
	return nil
}

func (w *world) ipKey(ip string) string { return rediskeys.JoinRateLimit(ip) + w.suffix }

func (w *world) ipEmpty(ip string) error {
	w.key, w.limit = w.ipKey(ip), joinLimit
	return w.emptyBucket()
}

func (w *world) ipAllowed(ip string) error {
	r, err := w.lim.Allow(context.Background(), w.ipKey(ip), joinLimit)
	if err != nil {
		return err
	}
	if !r.Allowed {
		return fmt.Errorf("IP %s was refused, want allowed", ip)
	}
	return nil
}
