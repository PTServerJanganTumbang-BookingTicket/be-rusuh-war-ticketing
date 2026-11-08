package features

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
)

var crashRe = regexp.MustCompile(`"c(\d+)"`)

// setCrash arms one crash point from its words in the spec.
func (w *tickWorld) setCrash(point string) error {
	nth := 1
	if m := crashRe.FindStringSubmatch(point); m != nil {
		nth, _ = strconv.Atoi(m[1])
	}
	switch {
	case containsAll(point, "UPDATE that lapses"):
		w.spyB.crashAfterLapse = true
	case containsAll(point, "EVALSHA"):
		w.spyQ.crashAfter["Advance"] = 1
	case containsAll(point, "INSERT of"):
		w.spyB.crashAfter["Insert"] = nth
	case containsAll(point, "HSET grant"):
		w.spyQ.crashAfter["PutGrant"] = nth
	case containsAll(point, "HSET token"):
		w.spyQ.crashAfter["AttachGrant"] = nth
	case containsAll(point, "last ZREM"):
		w.spyQ.crashAfter["Dequeue"] = 5
	default:
		return fmt.Errorf("unknown crash point %q", point)
	}
	return nil
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !regexp.MustCompile(regexp.QuoteMeta(p)).MatchString(s) {
			return false
		}
	}
	return true
}

func (w *tickWorld) pendingNow() int64 {
	n, _ := w.rdb.ZCard(w.ctx, w.key("E1").ByTicket()).Result()
	return n
}

func registerPendingSteps(sc *godog.ScenarioContext, w *tickWorld) {
	sc.Step(`^the controller will crash (.+)$`, func(point string) error { return w.setCrash(point) })
	sc.Step(`^one tick runs and the controller dies$`, func() error {
		w.runTickLogged()
		if w.crashErr == nil {
			return errors.New("the controller did not crash at the injected point")
		}
		w.scratch["pendingAtCrash"] = w.pendingNow()
		return nil
	})
	sc.Step(`^a new controller starts and runs one tick$`, func() error {
		w.newController(false)
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^the controller crashed after the INSERT of "c1", before its HSET$`, func() error {
		w.spyB.crashAfter["Insert"] = 1
		w.runTickLogged()
		if w.crashErr == nil {
			return errors.New("the controller did not crash")
		}
		return nil
	})
	sc.Step(`^the row for "c1" has booking_id "B1", grant_token "G1" and deadline "([^"]*)"$`, func(deadline string) error {
		return w.noteLabels(deadline)
	})
	sc.Step(`^the row for "c1" still has booking_id "B1", grant_token "G1" and deadline "([^"]*)"$`, func(deadline string) error {
		r, ok, err := w.rowOf("c1")
		if err != nil || !ok {
			return fmt.Errorf("no row for c1 (err %v)", err)
		}
		if r.ID != w.scratch["B1"] || r.GrantToken != w.scratch["G1"] || r.ExpiresAt.Format(time.RFC3339) != deadline {
			return fmt.Errorf("row changed: %+v (was %v/%v)", r, w.scratch["B1"], w.scratch["G1"])
		}
		return nil
	})
	sc.Step(`^the clock moves (\d+) seconds and a new controller runs one tick$`, func(secs int) error {
		w.clock.add(time.Duration(secs) * time.Second)
		w.newController(false)
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^grant:G1 has the same fields as before the crash$`, func() error {
		h, err := w.rdb.HGetAll(w.ctx, rediskeys.Grant(w.scratch["G1"].(string))).Result()
		if err != nil {
			return err
		}
		c := w.customers["c1"]
		if h["event_id"] != w.evID("E1") || h["booking_id"] != w.scratch["B1"] || h["queue_token"] != c.token || h["account_id"] != c.account || len(h) != 4 {
			return fmt.Errorf("grant fields %v", h)
		}
		return nil
	})
	sc.Step(`^grant:G1 has a TTL of (\d+) seconds$`, func(secs int) error {
		ttl, err := w.ttlSeconds(rediskeys.Grant(w.scratch["G1"].(string)))
		if err != nil {
			return err
		}
		if ttl < secs-2 || ttl > secs {
			return fmt.Errorf("TTL = %d s, want %d", ttl, secs)
		}
		return nil
	})
	sc.Step(`^token:<c1> holds grant_token "G1" and booking_expires_at "([^"]*)"$`, func(deadline string) error {
		h, err := w.rdb.HGetAll(w.ctx, rediskeys.Token(w.customers["c1"].token)).Result()
		if err != nil {
			return err
		}
		if h[rediskeys.FieldGrantToken] != w.scratch["G1"] || h[rediskeys.FieldBookingExpiresAt] != deadline {
			return fmt.Errorf("token hash %v", h)
		}
		return nil
	})

	sc.Step(`^bookings has exactly 1 row per customer (.+)$`, func(names string) error {
		for _, n := range parseNames(names) {
			rows, err := w.wantRows("E1")
			if err != nil {
				return err
			}
			count := 0
			for _, r := range rows {
				if r.QueueToken == w.customers[n].token {
					count++
				}
			}
			if count != 1 {
				return fmt.Errorf("%s has %d rows", n, count)
			}
		}
		return nil
	})
	sc.Step(`^each customer has exactly one grant:<grant_token> hash$`, func() error {
		rows, err := w.grantRows()
		if err != nil {
			return err
		}
		for _, r := range rows {
			n, err := w.rdb.Exists(w.ctx, rediskeys.Grant(r.GrantToken)).Result()
			if err != nil || n != 1 {
				return fmt.Errorf("grant hash for %s: exists=%d err=%v", r.QueueToken, n, err)
			}
		}
		return nil
	})
	sc.Step(`^each customer's token:<queue_token> holds that same grant_token$`, func() error {
		rows, err := w.grantRows()
		if err != nil {
			return err
		}
		for _, r := range rows {
			got, _ := w.rdb.HGet(w.ctx, rediskeys.Token(r.QueueToken), rediskeys.FieldGrantToken).Result()
			if got != r.GrantToken {
				return fmt.Errorf("token hash of %s holds %q, row has %q", r.QueueToken, got, r.GrantToken)
			}
		}
		return nil
	})
	sc.Step(`^by_ticket holds no member with score <= now_serving$`, func() error {
		ns, err := w.nowServing()
		if err != nil {
			return err
		}
		n, err := w.rdb.ZCount(w.ctx, w.key("E1").ByTicket(), "-inf", strconv.FormatInt(ns, 10)).Result()
		if err != nil || n != 0 {
			return fmt.Errorf("%d members still pending (err %v)", n, err)
		}
		return nil
	})
	sc.Step(`^the metric "PendingGrantsRecovered" equals the number of tokens still in by_ticket when it crashed$`, func() error {
		want, _ := w.scratch["pendingAtCrash"].(int64)
		if got := w.metricCount(usecase.MetricPendingGrantsRecovery); int64(got) != want {
			return fmt.Errorf("PendingGrantsRecovered = %d, want %d", got, want)
		}
		return nil
	})

	// ---- database failures
	sc.Step(`^the INSERT for "([^"]*)" fails 3 times in a row$`, func(name string) error {
		w.spyB.insertFails[w.customers[name].token] = 3
		return nil
	})
	sc.Step(`^"c1", "c3", "c4", "c5" are granted$`, func() error {
		for _, n := range []string{"c1", "c3", "c4", "c5"} {
			if r, ok, err := w.rowOf(n); err != nil || !ok || r.Status != "GRANTED" {
				return fmt.Errorf("%s not granted (ok=%v err=%v)", n, ok, err)
			}
		}
		return nil
	})
	sc.Step(`^"c2" is still in by_ticket with score <= now_serving$`, func() error {
		in, err := w.inByTicket("c2")
		if err != nil || !in {
			return fmt.Errorf("c2 in by_ticket=%v err=%v", in, err)
		}
		return nil
	})
	sc.Step(`^no grant hash exists for "c2"$`, func() error {
		if _, ok, _ := w.rowOf("c2"); ok {
			return errors.New("c2 has a booking row, so a grant could exist")
		}
		return nil
	})
	sc.Step(`^the broadcast is still published$`, func() error { return w.broadcastCount(1) })
	sc.Step(`^"c2" is pending after failed INSERTs$`, func() error {
		w.spyB.insertFails[w.customers["c2"].token] = 1 << 20
		w.runTickLogged()
		return nil
	})
	sc.Step(`^the database recovers and the next tick runs$`, func() error {
		w.spyB.mu.Lock()
		w.spyB.insertFails = map[string]int{}
		w.spyB.mu.Unlock()
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^"c2" is granted by RECOVER before ADVANCE runs$`, func() error {
		insert, advance := -1, -1
		for i, c := range w.log.snapshot() {
			if c.op == "Insert" && c.arg == w.customers["c2"].token && insert < 0 {
				insert = i
			}
			if c.op == "Advance" && advance < 0 {
				advance = i
			}
		}
		if insert < 0 || (advance >= 0 && insert > advance) {
			return fmt.Errorf("INSERT of c2 at %d, ADVANCE at %d", insert, advance)
		}
		return nil
	})
	sc.Step(`^"c2" stays pending$`, func() error {
		w.spyB.insertFails[w.customers["c2"].token] = 1 << 20
		return nil
	})
	sc.Step(`^(\d+) ticks run$`, func(n int) error {
		for i := 0; i < n; i++ {
			w.runTickLogged()
		}
		return nil
	})
	sc.Step(`^the 11th tick runs$`, func() error { w.runTickLogged(); return nil })
	sc.Step(`^the alert "([A-Za-z]+)" is (not )?raised$`, func(name, not string) error {
		raised := w.metricCount(name) > 0
		if raised == (not != "") {
			return fmt.Errorf("alert %s raised=%v", name, raised)
		}
		return nil
	})

	// ---- cheap recovery
	sc.Step(`^no token is pending$`, func() error { w.runTickLogged(); return nil })
	sc.Step(`^RECOVER runs$`, func() error {
		w.log.reset()
		w.tick.RecoverEvent(w.ctx, w.primary())
		return nil
	})
	sc.Step(`^exactly 1 Redis call is made: ZRANGEBYSCORE by_ticket -inf now_serving$`, func() error {
		calls := w.log.snapshot()
		if len(calls) != 1 || calls[0].op != "Pending" {
			return fmt.Errorf("calls during RECOVER: %+v", calls)
		}
		return nil
	})
	sc.Step(`^no database call is made$`, func() error {
		if w.log.count("Count")+w.log.count("Insert") != 0 {
			return errors.New("RECOVER touched the database")
		}
		return nil
	})

	// ---- two controllers
	sc.Step(`^tickets 1 to 5 are pending grants$`, func() error { return w.setNowServing("E1", 5) })
	sc.Step(`^controllers A and B run RECOVER at the same time$`, func() error {
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t := usecase.NewTick(w.cfg, w.spyB, w.spyQ, w.clock, w.metrics)
				t.RecoverEvent(w.ctx, w.primary())
			}()
		}
		wg.Wait()
		return nil
	})
	sc.Step(`^a double ZREM causes no error$`, func() error {
		if n := w.pendingNow(); n != 0 {
			return fmt.Errorf("%d tokens still pending", n)
		}
		return nil
	})
	sc.Step(`^controllers A and B read the same snapshot$`, func() error {
		ns, issued, err := w.spyQ.inner.Snapshot(w.ctx, w.primary().ID)
		w.scratch["snapshot"] = [2]int64{ns, issued}
		return err
	})
	sc.Step(`^both run ADVANCE$`, func() error {
		snap := w.scratch["snapshot"].([2]int64)
		var results []string
		total := 0
		for i := 0; i < 2; i++ {
			res, err := w.spyQ.inner.Advance(w.ctx, w.primary(), advArgs(w.cfg.MaxSlots, snap[0], w.clock.Now().Unix()))
			if err != nil {
				return err
			}
			results = append(results, res.Code)
			total += len(res.Tokens)
		}
		w.scratch["advance"], w.scratch["admittedTotal"] = results, total
		return nil
	})
	sc.Step(`^only one of them gets "ADMITTED"$`, func() error {
		r := w.scratch["advance"].([]string)
		if (r[0] != "ADMITTED" || r[1] != "STALE") && (r[0] != "STALE" || r[1] != "ADMITTED") {
			return fmt.Errorf("results %v", r)
		}
		return nil
	})
	sc.Step(`^the total admitted never exceeds MAX_CONCURRENT_SLOTS$`, func() error {
		if n := w.scratch["admittedTotal"].(int); int64(n) > w.cfg.MaxSlots {
			return fmt.Errorf("%d admitted, MAX %d", n, w.cfg.MaxSlots)
		}
		return nil
	})

	// ---- counting the line
	sc.Step(`^tickets 1 to 5 are pending and 20 customers wait behind them$`, func() error {
		for i := 0; i < 20; i++ {
			if _, err := w.join("E1", fmt.Sprintf("b%d", i), false); err != nil {
				return err
			}
		}
		return w.setNowServing("E1", 5)
	})
	sc.Step(`^the reported queue depth is issued − now_serving$`, func() error {
		w.runTickLogged()
		w.metrics.mu.Lock()
		got := w.metrics.gauges[usecase.GaugeQueueDepth]
		w.metrics.mu.Unlock()
		if got != 20 {
			return fmt.Errorf("QueueDepth = %v, want 20 (25 issued − 5 serving)", got)
		}
		return nil
	})
	sc.Step(`^it does not use ZCARD by_ticket$`, func() error {
		for _, c := range w.log.snapshot() {
			if c.op == "ZCard" {
				return errors.New("ZCARD was used")
			}
		}
		return nil
	})

	// ---- a token that expired
	sc.Step(`^"c4" is pending and token:<c4> no longer exists$`, func() error {
		if err := w.setNowServing("E1", 5); err != nil {
			return err
		}
		return w.rdb.Del(w.ctx, rediskeys.Token(w.customers["c4"].token)).Err()
	})
	sc.Step(`^no row is written for "c4"$`, func() error {
		if _, ok, _ := w.rowOf("c4"); ok {
			return errors.New("c4 has a row")
		}
		return nil
	})
	sc.Step(`^"c4" is removed from by_ticket$`, func() error {
		if in, _ := w.inByTicket("c4"); in {
			return errors.New("c4 is still in by_ticket")
		}
		return nil
	})
}

func (w *tickWorld) noteLabels(deadline string) error {
	r, ok, err := w.rowOf("c1")
	if err != nil || !ok {
		return fmt.Errorf("no row for c1 (err %v)", err)
	}
	if r.ExpiresAt.Format(time.RFC3339) != deadline {
		return fmt.Errorf("deadline %s, want %s", r.ExpiresAt.Format(time.RFC3339), deadline)
	}
	w.scratch["B1"], w.scratch["G1"] = r.ID, r.GrantToken
	return nil
}
