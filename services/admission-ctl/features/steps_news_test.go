package features

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
)

func (w *tickWorld) setFullBooked(sinceEpoch int64) error {
	if err := w.setStock("E1", 0); err != nil {
		return err
	}
	return w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldState, "FULL_BOOKED", rediskeys.FieldFullBookedAt, sinceEpoch).Err()
}

// putState makes the next tick read the wanted sale state. FULL_BOOKED and OPEN come from stock (the Lua decides);
// CLOSED is written, because only admission-ctl sets it.
func (w *tickWorld) putState(state string) error {
	switch state {
	case "FULL_BOOKED":
		return w.setStock("E1", 0)
	case "OPEN":
		return w.setStock("E1", 9000)
	case "CLOSED":
		return w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldState, "CLOSED").Err()
	}
	return fmt.Errorf("unknown state %q", state)
}

func (w *tickWorld) lastBroadcast() (map[string]any, error) {
	w.spyQ.mu.Lock()
	defer w.spyQ.mu.Unlock()
	if len(w.spyQ.broadcasts) == 0 {
		return nil, fmt.Errorf("no broadcast was published")
	}
	var m map[string]any
	return m, json.Unmarshal(w.spyQ.broadcasts[len(w.spyQ.broadcasts)-1], &m)
}

func (w *tickWorld) statusStates() []string {
	w.spyQ.mu.Lock()
	defer w.spyQ.mu.Unlock()
	var out []string
	for _, p := range w.spyQ.statuses {
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		s, _ := m["state"].(string)
		out = append(out, s)
	}
	return out
}

func (w *tickWorld) exactlyOneStatus(state string) error {
	if got := w.statusStates(); len(got) != 1 || got[0] != state {
		return fmt.Errorf("status messages %v, want exactly one %q", got, state)
	}
	return nil
}

func registerNewsSteps(sc *godog.ScenarioContext, w *tickWorld) {
	sc.Step(`^now_serving is (\d+) and rate_per_min is (\d+)$`, func(ns, rate int64) error {
		// The tick measures the rate itself; with MAX x / 5 minutes seed the first answer is exactly the wanted one.
		w.reconfig(func(c *usecase.Config) { c.MaxSlots = rate * 5 })
		if err := w.setNowServing("E1", ns); err != nil {
			return err
		}
		return w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, ns).Err()
	})
	sc.Step(`^a tick publishes the broadcast$`, func() error { w.runTickLogged(); return w.lastErr })
	sc.Step(`^the sse-broadcast message for "E1" is:$`, func(d *godog.DocString) error {
		w.spyQ.mu.Lock()
		defer w.spyQ.mu.Unlock()
		if len(w.spyQ.broadcasts) != 1 {
			return fmt.Errorf("%d broadcasts, want 1", len(w.spyQ.broadcasts))
		}
		if got, want := string(w.spyQ.broadcasts[0]), strings.TrimSpace(d.Content); got != want {
			return fmt.Errorf("broadcast %s, want %s", got, want)
		}
		return nil
	})
	sc.Step(`^the message contains no ticket_no, queue_token or grant_token$`, func() error {
		w.spyQ.mu.Lock()
		defer w.spyQ.mu.Unlock()
		for _, p := range w.spyQ.broadcasts {
			for _, bad := range []string{"ticket_no", "queue_token", "grant_token"} {
				if strings.Contains(string(p), bad) {
					return fmt.Errorf("broadcast leaks %s: %s", bad, p)
				}
			}
		}
		return nil
	})
	sc.Step(`^(\d+) tokens are admitted this tick$`, func(n int) error {
		for i := 1; i <= n; i++ {
			if _, err := w.join("E1", fmt.Sprintf("c%d", i), false); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^PUBLISH sse-broadcast comes after the 3rd ZREM in the call log$`, func() error {
		zrem, pub := 0, -1
		for i, c := range w.log.snapshot() {
			if c.op == "Dequeue" {
				zrem++
				if zrem == 3 && pub >= 0 {
					return fmt.Errorf("broadcast at %d came before the 3rd ZREM at %d", pub, i)
				}
			}
			if c.op == "PublishBroadcast" && pub < 0 {
				pub = i
				if zrem < 3 {
					return fmt.Errorf("broadcast came after only %d ZREMs", zrem)
				}
			}
		}
		return nil
	})
	sc.Step(`^the last broadcast had now_serving (\d+)$`, func(n int64) error {
		if err := w.setNowServing("E1", n); err != nil {
			return err
		}
		if err := w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, n).Err(); err != nil {
			return err
		}
		w.runTickLogged()
		w.resetCaptures()
		return w.lastErr
	})
	sc.Step(`^a tick moves now_serving to (\d+)$`, func(to int64) error {
		cur, _ := w.nowServing()
		for i := cur; i < to; i++ {
			if _, err := w.join("E1", fmt.Sprintf("n%d", i), false); err != nil {
				return err
			}
		}
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^one broadcast is published with now_serving (\d+)$`, func(n int64) error {
		w.spyQ.mu.Lock()
		count := len(w.spyQ.broadcasts)
		w.spyQ.mu.Unlock()
		if count != 1 {
			return fmt.Errorf("%d broadcasts, want 1", count)
		}
		m, err := w.lastBroadcast()
		if err != nil {
			return err
		}
		if int64(m["nowServing"].(float64)) != n {
			return fmt.Errorf("broadcast %v, want nowServing %d", m, n)
		}
		return nil
	})
	sc.Step(`^one heartbeat broadcast is published with now_serving (\d+)$`, func(n int64) error {
		w.spyQ.mu.Lock()
		count := len(w.spyQ.broadcasts)
		w.spyQ.mu.Unlock()
		if count != 1 {
			return fmt.Errorf("%d broadcasts, want 1 heartbeat", count)
		}
		m, _ := w.lastBroadcast()
		if int64(m["nowServing"].(float64)) != n {
			return fmt.Errorf("heartbeat %v, want nowServing %d", m, n)
		}
		return nil
	})
	sc.Step(`^the last broadcast was at epoch (\d+) with now_serving (\d+)$`, func(at, n int64) error {
		w.clock.set(time.Unix(at, 0).UTC())
		w.scratch["base"] = at
		if err := w.setNowServing("E1", n); err != nil {
			return err
		}
		if err := w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, n).Err(); err != nil {
			return err
		}
		w.runTickLogged()
		w.resetCaptures()
		return w.lastErr
	})
	sc.Step(`^ticks run for ([\d.]+) s with now_serving still (\d+)$`, func(secs float64, _ int64) error {
		for elapsed := 0.5; elapsed <= secs+1e-9; elapsed += 0.5 {
			w.clock.add(500 * time.Millisecond)
			w.runTickLogged()
			if w.lastErr != nil {
				return w.lastErr
			}
		}
		return nil
	})
	sc.Step(`^no broadcast is published$`, func() error {
		w.spyQ.mu.Lock()
		defer w.spyQ.mu.Unlock()
		if len(w.spyQ.broadcasts) != 0 {
			return fmt.Errorf("%d broadcasts were published", len(w.spyQ.broadcasts))
		}
		return nil
	})
	sc.Step(`^the tick at (\d+) s runs$`, func(secs int) error {
		base := w.scratch["base"].(int64)
		w.clock.set(time.Unix(base+int64(secs), 0).UTC())
		w.runTickLogged()
		return w.lastErr
	})

	// ---- state changes
	sc.Step(`^the last published state for "E1" is "([A-Z_]+)"$`, func(state string) error {
		if err := w.putState(state); err != nil {
			return err
		}
		w.runTickLogged()
		w.resetCaptures()
		return w.lastErr
	})
	sc.Step(`^a tick reads state "([A-Z_]+)"$`, func(state string) error {
		if err := w.putState(state); err != nil {
			return err
		}
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^exactly one sse-status message is published with state "([A-Z_]+)"$`, func(state string) error { return w.exactlyOneStatus(state) })
	sc.Step(`^one sse-status message is published with state "([A-Z_]+)"$`, func(state string) error { return w.exactlyOneStatus(state) })
	sc.Step(`^the next tick reads state "([A-Z_]+)" again$`, func(string) error {
		w.resetCaptures()
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^no sse-status message is published$`, func() error {
		if got := w.statusStates(); len(got) != 0 {
			return fmt.Errorf("status messages %v, want none", got)
		}
		return nil
	})
	sc.Step(`^"E1" was "([A-Z_]+)" since epoch (\d+)$`, func(state string, since int64) error {
		if state == "FULL_BOOKED" {
			return w.setFullBooked(since)
		}
		return nil
	})
	sc.Step(`^"E1" becomes "([A-Z_]+)" at epoch (\d+)$`, func(state string, at int64) error {
		w.clock.set(time.Unix(at, 0).UTC())
		switch state {
		case "FULL_BOOKED", "CLOSED":
			if err := w.setStock("E1", 0); err != nil {
				return err
			}
		case "OPEN":
			if err := w.setStock("E1", 9000); err != nil {
				return err
			}
		}
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^the sse-status message is:$`, func(d *godog.DocString) error {
		w.spyQ.mu.Lock()
		defer w.spyQ.mu.Unlock()
		if len(w.spyQ.statuses) == 0 {
			return fmt.Errorf("no status message was published")
		}
		got := string(w.spyQ.statuses[len(w.spyQ.statuses)-1])
		if want := strings.TrimSpace(d.Content); got != want {
			return fmt.Errorf("status %s, want %s", got, want)
		}
		return nil
	})
	sc.Step(`^the controller just started and "E1" is "FULL_BOOKED"$`, func() error {
		return w.setFullBooked(w.clock.Now().Unix())
	})

	// ---- closing
	sc.Step(`^"E1" is "FULL_BOOKED" since epoch (\d+)$`, func(since int64) error { return w.setFullBooked(since) })
	sc.Step(`^the clock reads epoch (\d+) and the tick runs$`, func(n int64) error {
		w.clock.set(time.Unix(n, 0).UTC())
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^the state is (?:still )?"([A-Z_]+)"$`, func(want string) error {
		got, err := w.field("E1", rediskeys.FieldState)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("state = %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^an expired hold returns 3 tickets at epoch (\d+) and the tick runs$`, func(at int64) error {
		w.clock.set(time.Unix(at, 0).UTC())
		if err := w.setStock("E1", 3); err != nil {
			return err
		}
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^"E1" sells out again at epoch (\d+)$`, func(at int64) error {
		w.clock.set(time.Unix(at, 0).UTC())
		if err := w.setStock("E1", 0); err != nil {
			return err
		}
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^"E1" is (still )?"([A-Z_]+)" at epoch (\d+)$`, func(_ string, want string, at int64) error {
		w.clock.set(time.Unix(at, 0).UTC())
		w.runTickLogged()
		if w.lastErr != nil {
			return w.lastErr
		}
		got, _ := w.field("E1", rediskeys.FieldState)
		if got != want {
			return fmt.Errorf("state at %d = %q, want %q", at, got, want)
		}
		return nil
	})
	sc.Step(`^"E1" is "CLOSED"$`, func() error { return w.putState("CLOSED") })
	sc.Step(`^an expired hold returns 3 tickets and the tick runs$`, func() error {
		if err := w.setStock("E1", 3); err != nil {
			return err
		}
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^no customer is admitted$`, func() error { return w.admittedIs(0) })
}

func registerRateSteps(sc *godog.ScenarioContext, w *tickWorld) {
	sc.Step(`^MAX_CONCURRENT_SLOTS is (\d+) and ETA_SEED_SLOT_MINUTES is (\d+)$`, func(max, minutes int64) error {
		w.cfg.MaxSlots, w.cfg.EtaSeedMinutes = max, minutes
		return nil
	})
	sc.Step(`^event "E1" opened at epoch (\d+)$`, func(at int64) error {
		w.clock.set(time.Unix(at, 0).UTC())
		return w.addEvent("E1", "OPEN", 9000)
	})
	sc.Step(`^ticks run for ([\d.]+) s and now_serving moves from (\d+) to (\d+)$`, func(secs float64, from, to int64) error {
		return w.drive(secs, func(i, n int) int64 { return from + (to-from)*int64(i)/int64(n) }, from)
	})
	sc.Step(`^the first tick admitted (\d+) at once$`, func(n int64) error {
		if err := w.setNowServing("E1", n); err != nil {
			return err
		}
		if err := w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, n).Err(); err != nil {
			return err
		}
		w.scratch["ns"] = n
		w.runTickLogged()
		return w.lastErr
	})
	sc.Step(`^afterwards now_serving moved by (\d+) per tick$`, func(n int64) error { w.scratch["per"] = n; return nil })
	sc.Step(`^(\d+) s have passed$`, func(secs float64) error {
		per, _ := w.scratch["per"].(int64)
		base, _ := w.scratch["ns"].(int64)
		return w.driveFrom(secs, per, base)
	})
	sc.Step(`^now_serving moved steadily by (\d+) per tick for (\d+) s$`, func(per int64, secs float64) error {
		w.runTickLogged()
		return w.driveFrom(secs, per, 0)
	})
	sc.Step(`^the window covers (\d+) s and now_serving did not move$`, func(secs float64) error {
		if err := w.setNowServing("E1", 5); err != nil {
			return err
		}
		if err := w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, 5).Err(); err != nil {
			return err
		}
		w.runTickLogged()
		return w.driveFrom(secs, 0, 5)
	})
	sc.Step(`^rate_per_min is (\d+)$`, func(want int64) error {
		got, err := w.field("E1", rediskeys.FieldRatePerMin)
		if err != nil {
			return err
		}
		if got != strconv.FormatInt(want, 10) {
			return fmt.Errorf("rate_per_min = %q, want %d", got, want)
		}
		return nil
	})
	sc.Step(`^rate_per_min is (\d+) after (\d+) minutes OPEN$`, func(want, minutes int64) error {
		w.runTickLogged()
		if err := w.driveFrom(float64(minutes*60), 5, 0); err != nil {
			return err
		}
		got, _ := w.field("E1", rediskeys.FieldRatePerMin)
		if got != strconv.FormatInt(want, 10) {
			return fmt.Errorf("rate_per_min = %q after %d minutes, want %d", got, minutes, want)
		}
		return nil
	})
	sc.Step(`^"E1" is FULL_BOOKED for (\d+) minutes$`, func(minutes int64) error {
		w.scratch["samples"] = w.tick.RateSamples(w.primary().ID)
		if err := w.setStock("E1", 0); err != nil {
			return err
		}
		ns := w.scratch["ns"].(int64)
		var seen []string
		for i := 0; i < int(minutes*120); i++ {
			w.clock.add(500 * time.Millisecond)
			w.runTickLogged()
			if w.lastErr != nil {
				return w.lastErr
			}
			r, _ := w.field("E1", rediskeys.FieldRatePerMin)
			seen = append(seen, r)
		}
		w.scratch["seen"], w.scratch["ns"] = seen, ns
		return nil
	})
	sc.Step(`^rate_per_min stays 600 for the whole FULL_BOOKED time$`, func() error {
		for _, r := range w.scratch["seen"].([]string) {
			if r != "600" {
				return fmt.Errorf("rate_per_min changed to %s while fully booked", r)
			}
		}
		return nil
	})
	sc.Step(`^no sample was added while FULL_BOOKED$`, func() error {
		// The first FULL_BOOKED tick reads state OPEN→FULL_BOOKED in the same run; allow that single tick.
		if got, was := w.tick.RateSamples(w.primary().ID), w.scratch["samples"].(int); got > was+1 {
			return fmt.Errorf("samples grew from %d to %d while fully booked", was, got)
		}
		return nil
	})
	sc.Step(`^"E1" reopens and now_serving moves by (\d+) per tick$`, func(per int64) error {
		if err := w.setStock("E1", 9000); err != nil {
			return err
		}
		return w.driveFrom(60, per, w.scratch["ns"].(int64))
	})
	sc.Step(`^rate_per_min is computed from OPEN samples only$`, func() error {
		got, _ := w.field("E1", rediskeys.FieldRatePerMin)
		if got != "600" {
			return fmt.Errorf("rate_per_min = %q, want 600 from 60 s of OPEN samples at 5 per tick", got)
		}
		return nil
	})
	sc.Step(`^rate_per_min was 600 before the controller restarted$`, func() error {
		w.runTickLogged()
		if err := w.driveFrom(120, 5, 0); err != nil {
			return err
		}
		w.scratch["ns"] = int64(1200)
		got, _ := w.field("E1", rediskeys.FieldRatePerMin)
		if got != "600" {
			return fmt.Errorf("rate before restart = %q", got)
		}
		return nil
	})
	sc.Step(`^the new controller has run for (\d+) s$`, func(secs float64) error {
		w.newController(false)
		w.runTickLogged()
		return w.driveFrom(secs, 5, w.scratch["ns"].(int64))
	})
	sc.Step(`^the measured rate is 612.6 per minute$`, func() error {
		w.runTickLogged() // sample at ns 0
		w.clock.add(100 * time.Second)
		if err := w.setNowServing("E1", 1021); err != nil {
			return err
		}
		return w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, 1021).Err()
	})
	sc.Step(`^queue:\{E1\} field rate_per_min is "([^"]*)"$`, func(want string) error {
		got, _ := w.field("E1", rediskeys.FieldRatePerMin)
		if got != want {
			return fmt.Errorf("rate_per_min = %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^the next broadcast carries ratePerMin (\d+)$`, func(want int64) error {
		m, err := w.lastBroadcast()
		if err != nil {
			return err
		}
		if int64(m["ratePerMin"].(float64)) != want {
			return fmt.Errorf("broadcast %v, want ratePerMin %d", m, want)
		}
		return nil
	})
}

// drive moves the clock in 500 ms ticks for secs seconds; ns(i, n) is now_serving at tick i of n.
func (w *tickWorld) drive(secs float64, ns func(i, n int) int64, from int64) error {
	n := int(secs * 2)
	if err := w.setNowServing("E1", from); err != nil {
		return err
	}
	w.runTickLogged()
	for i := 1; i <= n; i++ {
		w.clock.add(500 * time.Millisecond)
		v := ns(i, n)
		if err := w.setNowServing("E1", v); err != nil {
			return err
		}
		if err := w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, v).Err(); err != nil {
			return err
		}
		w.runTickLogged()
		if w.lastErr != nil {
			return w.lastErr
		}
	}
	return nil
}

// driveFrom adds per to now_serving on every tick for secs seconds, starting at base.
func (w *tickWorld) driveFrom(secs float64, per, base int64) error {
	n := int(secs * 2)
	for i := 1; i <= n; i++ {
		w.clock.add(500 * time.Millisecond)
		v := base + per*int64(i)
		if err := w.setNowServing("E1", v); err != nil {
			return err
		}
		if err := w.rdb.HSet(w.ctx, w.key("E1").QueueHash(), rediskeys.FieldIssued, v).Err(); err != nil {
			return err
		}
		w.runTickLogged()
		if w.lastErr != nil {
			return w.lastErr
		}
	}
	w.scratch["ns"] = base + per*int64(n)
	return nil
}
