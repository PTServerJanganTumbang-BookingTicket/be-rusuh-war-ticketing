package features

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// registerTickSteps wires the steps of admission_tick, pending_grants, broadcast_and_status, rate_per_min.
// A fresh world is built for every scenario: miniredis + memory bookings for @unit, the real Redis cluster and
// Postgres for @integration.
func registerTickSteps(sc *godog.ScenarioContext) {
	w := newTickWorld(true)
	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		if strings.Contains(s.Uri, "bookings_store") {
			return ctx, nil // those scenarios build their own database
		}
		for _, t := range s.Tags {
			if t.Name == "@integration" {
				w.unit = false
			}
		}
		return ctx, w.setup()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.cleanup()
		return ctx, nil
	})

	registerTickGiven(sc, w)
	registerTickThen(sc, w)
	registerPendingSteps(sc, w)
	registerNewsSteps(sc, w)
	registerRateSteps(sc, w)
	registerSweepSteps(sc, w)
}

var quoted = regexp.MustCompile(`"([^"]*)"`)

// parseNames reads `"c1", "c2", "c3"` or `"c1" to "c5"`.
func parseNames(spec string) []string {
	q := quoted.FindAllStringSubmatch(spec, -1)
	var names []string
	for _, m := range q {
		names = append(names, m[1])
	}
	if strings.Contains(spec, " to ") && len(names) == 2 {
		a, b := names[0], names[1]
		prefix := strings.TrimRight(a, "0123456789")
		lo, _ := strconv.Atoi(strings.TrimPrefix(a, prefix))
		hi, _ := strconv.Atoi(strings.TrimPrefix(b, prefix))
		names = nil
		for i := lo; i <= hi; i++ {
			names = append(names, fmt.Sprintf("%s%d", prefix, i))
		}
	}
	return names
}

func (w *tickWorld) joinAll(names []string) error {
	for _, n := range names {
		if _, err := w.join("E1", n, false); err != nil {
			return err
		}
	}
	return nil
}

func (w *tickWorld) reconfig(f func(c *usecase.Config)) {
	f(&w.cfg)
	w.buildTick()
}

func (w *tickWorld) resetCaptures() {
	w.spyQ.mu.Lock()
	w.spyQ.broadcasts, w.spyQ.statuses = nil, nil
	w.spyQ.mu.Unlock()
}

func (w *tickWorld) runTickLogged() {
	w.log.reset()
	w.scratch["ran"] = true
	if v, err := w.field("E1", rediskeys.FieldNowServing); err == nil {
		w.scratch["ns0"] = v
	}
	w.runTick()
}

func (w *tickWorld) metricCount(name string) int { return w.metrics.count(name) }

func registerTickGiven(sc *godog.ScenarioContext, w *tickWorld) {
	g := func(re string, f any) { sc.Step(re, f) }

	// ---- background
	g(`^event "E1" is "OPEN" with MAX_CONCURRENT_SLOTS (\d+)$`, func(max int64) error {
		w.cfg.MaxSlots = max
		return w.addEvent("E1", "OPEN", 9000)
	})
	g(`^event "E1" is "OPEN" with MAX_CONCURRENT_SLOTS (\d+) and stock (\d+)$`, func(max, stock int64) error {
		w.cfg.MaxSlots = max
		return w.addEvent("E1", "OPEN", stock)
	})
	g(`^event "E1" is "OPEN"$`, func() error { return w.addEvent("E1", "OPEN", 9000) })
	g(`^events "E1" and "E2" are "OPEN"$`, func() error {
		if err := w.addEvent("E1", "OPEN", 9000); err != nil {
			return err
		}
		return w.addEvent("E2", "OPEN", 9000)
	})
	g(`^event "E2" is "SCHEDULED" and opens at "([^"]*)"$`, func(string) error { return w.addEvent("E2", "SCHEDULED", 9000) })
	g(`^total stock is (\d+)$`, func(n int64) error { return w.setStock("E1", n) })
	g(`^the booking TTL is 5 minutes$`, func() error { w.reconfig(func(c *usecase.Config) { c.BookingTTL = 5 * time.Minute }); return nil })
	g(`^GRANT_TOKEN_TTL_SECONDS is (\d+)$`, func(n int) error {
		w.reconfig(func(c *usecase.Config) { c.GrantTTL = time.Duration(n) * time.Second })
		return nil
	})
	g(`^the clock is frozen at "([^"]*)"$`, func(ts string) error {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return err
		}
		w.clock.set(t)
		return nil
	})
	g(`^the clock is frozen at epoch (\d+)$`, func(n int64) error { w.clock.set(time.Unix(n, 0).UTC()); return nil })

	// ---- customers
	g(`^customers (.+) joined with ticket numbers (.+) and issued is (\d+)$`, func(names, _ string, _ int) error {
		return w.joinAll(parseNames(names))
	})
	g(`^customers hold ticket numbers 1 to (\d+) in by_ticket and issued is (\d+)$`, func(n, _ int) error {
		var names []string
		for i := 1; i <= n; i++ {
			names = append(names, fmt.Sprintf("t%d", i))
		}
		return w.joinAll(names)
	})
	g(`^customer "([^"]*)" is waiting with ticket number (\d+) and issued is (\d+)$`, func(name string, _, _ int) error {
		_, err := w.join("E1", name, false)
		return err
	})
	g(`^customer "([^"]*)" is a (guest|logged-in customer) with ticket number (\d+) and issued is (\d+)$`, func(name, kind string, _, _ int) error {
		_, err := w.join("E1", name, kind == "guest")
		return err
	})

	// ---- running
	for _, re := range []string{`^one tick runs$`, `^the next tick runs$`, `^the tick runs$`, `^the first tick runs$`} {
		g(re, func() error { w.runTickLogged(); return w.lastErr })
	}

	// ---- inside / outside
	g(`^(\d+) customers are inside with status "([A-Z_]+)"$`, func(n int, status string) error {
		return w.bks.seedInside(w.ctx, w.evID("E1"), n, status)
	})
	g(`^(\d+) more customers are waiting$`, func(n int) error {
		for i := 0; i < n; i++ {
			if _, err := w.join("E1", fmt.Sprintf("w%d", i), false); err != nil {
				return err
			}
		}
		return nil
	})
	g(`^(\d+) bookings? moves? to "([A-Z_]+)"$`, func(_ int, to string) error {
		return w.bks.moveOne(w.ctx, w.evID("E1"), "GRANTED", to)
	})
	g(`^the tick interval is 500 ms$`, func() error { return nil })
	g(`^a booking is confirmed 100 ms after a tick$`, func() error {
		if err := w.bks.seedInside(w.ctx, w.evID("E1"), 1, "GRANTED"); err != nil {
			return err
		}
		w.runTickLogged()
		w.scratch["advances"] = w.log.count("Advance")
		w.clock.add(100 * time.Millisecond)
		w.log.reset()
		return w.bks.moveOne(w.ctx, w.evID("E1"), "GRANTED", "CONFIRMED")
	})
	g(`^Aurora counts open (\d+) and browsing (\d+) for "E1"$`, func(open, browsing int64) error {
		w.spyB.forcedCount = &domain.Admission{Open: open, Browsing: browsing}
		return nil
	})
	g(`^(\d+) tokens are pending grants$`, func(n int) error {
		for i := 0; i < n; i++ {
			if _, err := w.join("E1", fmt.Sprintf("p%d", i), false); err != nil {
				return err
			}
		}
		w.spyB.failAll = true // their bookings cannot be saved, so they stay pending
		return w.setNowServing("E1", int64(n))
	})
	g(`^breaker "([^"]*)" is "OPEN"$`, func(key string) error { return w.rdb.Set(w.ctx, key, "OPEN", time.Minute).Err() })

	// ---- failures
	g(`^the Aurora COUNT fails with "([^"]*)"$`, func(msg string) error {
		w.spyB.countErr[""] = errors.New(msg)
		return nil
	})
	g(`^the Aurora COUNT fails only for "E2"$`, func() error {
		w.spyB.countErr[w.evID("E2")] = errDown
		return nil
	})
	g(`^token:<c1> expires right after the INSERT of "c1"$`, func() error {
		c := w.customers["c1"]
		w.spyB.afterInsert = func() { _ = w.rdb.Del(w.ctx, rediskeys.Token(c.token)).Err() }
		return nil
	})
	g(`^one tick takes (\d+) ms$`, func(ms int) error {
		w.spyQ.slow = time.Duration(ms) * time.Millisecond
		return nil
	})
	g(`^SNAPSHOT reads now_serving (\d+)$`, func(n int64) error {
		if err := w.setNowServing("E1", n); err != nil {
			return err
		}
		return w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, n+10).Err()
	})
	g(`^another writer raises now_serving to (\d+) before ADVANCE$`, func(n int64) error {
		w.spyQ.beforeAdv = func() { _ = w.setNowServing("E1", n) }
		return nil
	})
	g(`^ADVANCE returns "([A-Z_]+)"$`, func(code string) error {
		if w.scratch["ran"] == true { // used as a Then in the stale scenario
			if w.spyQ.lastResult.Code != code {
				return fmt.Errorf("ADVANCE returned %q, want %q", w.spyQ.lastResult.Code, code)
			}
			return nil
		}
		w.spyQ.forced = &redisscript.Result{Code: code}
		return nil
	})
	g(`^customer "c1" was granted at "([^"]*)"$`, func(ts string) error {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return err
		}
		w.clock.set(t)
		if _, err := w.join("E1", "c1", false); err != nil {
			return err
		}
		w.runTickLogged()
		return w.lastErr
	})
	g(`^the clock reads "([^"]*)"$`, func(ts string) error {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return err
		}
		w.clock.set(t)
		return nil
	})
	g(`^the ticker fires at 500 ms$`, func() error {
		ctx, cancel := context.WithTimeout(w.ctx, 1700*time.Millisecond)
		defer cancel()
		w.tick.Loop(ctx)
		return nil
	})
}

var _ = sync.Mutex{}
var _ = goredis.Nil
