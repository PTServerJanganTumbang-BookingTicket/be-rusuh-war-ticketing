package features

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

func registerReliabilitySteps(sc *godog.ScenarioContext, W func() *world) {
	// ---- time passes
	sc.Step(`^(\d+) (?:more )?seconds pass(?: with no broadcast)?$`, func(n int) error {
		W().pass(time.Duration(n) * time.Second)
		return nil
	})
	sc.Step(`^(\d+) more seconds pass without a second "stale" message$`, func(n int) error {
		w := W()
		w.pass(time.Duration(n) * time.Second)
		if got := countStale(w.me, "true"); got != 1 {
			return fmt.Errorf("%d stale true messages, want 1", got)
		}
		return nil
	})
	sc.Step(`^(\d+) seconds pass with a heartbeat every 10 seconds$`, func(n int) error {
		w := W()
		for elapsed := 0; elapsed < n; elapsed += 10 {
			w.pass(10 * time.Second)
			if err := w.publishBroadcast("E1", w.nowServing("E1"), ""); err != nil {
				return err
			}
			w.settle()
		}
		return nil
	})
	sc.Step(`^no broadcast arrived for (\d+) seconds$`, func(n int) error {
		W().pass(time.Duration(n) * time.Second)
		return nil
	})
	sc.Step(`^only "([^"]*)" keeps receiving broadcasts for (\d+) seconds$`, func(id string, n int) error {
		w := W()
		for i := 0; i < n; i++ {
			w.pass(time.Second)
			if err := w.publishBroadcast(id, w.nowServing(id)+int64(i), ""); err != nil {
				return err
			}
		}
		w.settle()
		return nil
	})
	sc.Step(`^the server started 5 minutes ago and nobody watched "E1"$`, func() error {
		w := W()
		w.me.leave()
		w.settle()
		w.pass(5 * time.Minute)
		return nil
	})

	// ---- who connects later
	sc.Step(`^(?:a customer|a second customer) connects$`, func() error {
		w := W()
		cs, err := w.connectMany(w.srv(), "E1", 1)
		w.captured["new"] = cs[0]
		return err
	})
	sc.Step(`^a new customer connects with a valid token$`, func() error {
		w := W()
		_, err := w.connectMany(w.srv(), "E1", 1)
		return err
	})
	sc.Step(`^they receive no "stale" during their first 29 seconds$`, func() error {
		w := W()
		w.pass(29 * time.Second)
		if n := countStale(w.captured["new"].(*client), ""); n != 0 {
			return fmt.Errorf("%d stale messages", n)
		}
		return nil
	})
	sc.Step(`^they receive their snapshot followed by "stale" with message "([^"]*)"$`, func(message string) error {
		w := W()
		w.settle()
		fs := nonComment(w.captured["new"].(*client).frames())
		if len(fs) < 2 || fs[0].event != "" || fs[1].event != "stale" || !strings.Contains(fs[1].data, message) {
			return fmt.Errorf("frames: %v", fs)
		}
		return nil
	})

	// ---- stale and keepalive assertions
	sc.Step(`^I received exactly 3 keepalives, each ": keepalive" and a blank line$`, func() error {
		if n := strings.Count(W().me.w.body(), ": keepalive\n\n"); n != 3 {
			return fmt.Errorf("%d keepalives, want 3", n)
		}
		return nil
	})
	sc.Step(`^none of them triggered my app's event handlers$`, func() error {
		for _, f := range W().me.frames() {
			if strings.HasPrefix(f.raw, ": keepalive") && (!f.comment || f.event != "" || f.data != "" || f.id != "") {
				return fmt.Errorf("a keepalive is an event: %q", f.raw)
			}
		}
		return nil
	})
	sc.Step(`^I received "stale" with message "([^"]*)"$`, func(message string) error {
		w := W()
		switch message {
		case "Data antrian belum diperbarui":
			w.pass(31 * time.Second)
		case "Koneksi server terputus, mencoba menyambung kembali":
			w.dropRedis()
		}
		if countStale(w.me, "true") != 1 || !strings.Contains(w.me.w.body(), message) {
			return fmt.Errorf("no stale with message %s in %q", message, truncate(w.me.w.body()))
		}
		w.me.takeNew()
		return nil
	})
	sc.Step(`^I received exactly 1 "stale" true$`, func() error {
		if n := countStale(W().me, "true"); n != 1 {
			return fmt.Errorf("%d stale true messages, want 1", n)
		}
		return nil
	})
	sc.Step(`^I receive 0 "stale" messages$`, func() error {
		w := W()
		w.settle()
		if n := countStale(w.me, ""); n != 0 {
			return fmt.Errorf("%d stale messages, want 0", n)
		}
		return nil
	})
	sc.Step(`^I have not yet received "stale" false$`, func() error {
		W().settle()
		if countStale(W().me, "false") != 0 {
			return fmt.Errorf("stale false already received")
		}
		return nil
	})
	sc.Step(`^I receive "stale" false, then that broadcast$`, func() error {
		w := W()
		w.settle()
		got := w.me.takeNew()
		i, j := strings.Index(got, `"stale":false`), strings.Index(got, `"nowServing"`)
		if i < 0 || j < 0 || i > j {
			return fmt.Errorf("received %q, want stale false then a broadcast", got)
		}
		return nil
	})
	sc.Step(`^then a normal message with nowServing (\d+)$`, func(n int64) error { return W().expectData(W().me, n) })
	sc.Step(`^I receive "stale" and the "([^"]*)" customer does not$`, func(id string) error {
		w := W()
		if countStale(w.me, "true") != 1 {
			return fmt.Errorf("I did not receive stale")
		}
		for _, c := range w.byEvent[id] {
			if n := countStale(c, ""); n != 0 {
				return fmt.Errorf("a customer of %s received stale", id)
			}
		}
		return nil
	})
	sc.Step(`^my connection is still open and I received "stale" once$`, func() error {
		w := W()
		if !w.me.open() || countStale(w.me, "true") != 1 {
			return fmt.Errorf("open=%v stale=%d", w.me.open(), countStale(w.me, "true"))
		}
		return nil
	})
	sc.Step(`^my connection stays open and keepalives continue$`, func() error {
		w := W()
		before := strings.Count(w.me.w.body(), ": keepalive")
		w.pass(15 * time.Second)
		if !w.me.open() || strings.Count(w.me.w.body(), ": keepalive") <= before {
			return fmt.Errorf("connection closed or no more keepalives")
		}
		return nil
	})

	// ---- redis
	sc.Step(`^the Redis pub/sub connection drops$`, func() error { W().dropRedis(); return nil })
	sc.Step(`^the Redis pub/sub connection dropped$`, func() error { W().dropRedis(); return nil })
	sc.Step(`^the Redis pub/sub connection dropped and "([^"]*)" became "([^"]*)" meanwhile$`, func(id, state string) error {
		w := W()
		w.dropRedis()
		return w.setState(id, state)
	})
	sc.Step(`^(?:Redis comes back|the subscription is restored)$`, func() error { return W().restoreRedis() })
	sc.Step(`^the next broadcast arrives$`, func() error {
		w := W()
		err := w.publishBroadcast("E1", w.nowServing("E1")+4, "")
		w.settle()
		return err
	})
	sc.Step(`^the server subscribes again to "([^"]*)" and "([^"]*)"$`, func(_, _ string) error {
		if W().source.subscribeCalls() < 2 {
			return fmt.Errorf("the server did not subscribe again")
		}
		return nil
	})
	sc.Step(`^the delay between attempts grows, is capped at 5 seconds and has jitter$`, func() error {
		delays := W().metrics.series("SSEResubscribeDelaySeconds")
		if len(delays) < 3 {
			return fmt.Errorf("only %d attempts recorded: %v", len(delays), delays)
		}
		grew, distinct := false, map[float64]bool{}
		for i, d := range delays {
			distinct[d] = true
			if d > 5.0 {
				return fmt.Errorf("delay %v is over the 5 s cap", d)
			}
			if i > 0 && d > delays[0] {
				grew = true
			}
		}
		if !grew || len(distinct) < 3 {
			return fmt.Errorf("delays %v do not grow with jitter", delays)
		}
		return nil
	})
	sc.Step(`^Redis is unreachable for 5 minutes$`, func() error {
		w := W()
		w.mr.Close()
		w.dropRedis()
		w.pass(5 * time.Minute)
		return nil
	})

	// ---- shutdown
	sc.Step(`^(?:the server receives SIGTERM|the server is draining after SIGTERM)$`, func() error { W().startDrain(); return nil })
	sc.Step(`^(\d+) customers ignore the reconnect hint$`, func(n int) error {
		w := W()
		cs, err := w.connectMany(w.srv(), "E1", n)
		w.captured["ignorers"] = cs
		return err
	})
	sc.Step(`^(\d+) seconds pass after SIGTERM$`, func(n int) error {
		w := W()
		w.startDrain()
		w.pass(time.Duration(n) * time.Second)
		return nil
	})
	sc.Step(`^each receives one "reconnect" message with a retry between 1000 and 15000 ms$`, func() error {
		w := W()
		w.settle()
		seen := map[int]int{}
		all := append([]*client{w.me}, w.captured["others"].([]*client)...)
		for _, c := range all {
			rs := c.named("reconnect")
			if len(rs) != 1 {
				return fmt.Errorf("%d reconnect messages, want 1", len(rs))
			}
			n, err := strconv.Atoi(rs[0].retry)
			if err != nil || n < 1000 || n > 15000 {
				return fmt.Errorf("retry %q out of range", rs[0].retry)
			}
			seen[n]++
		}
		w.captured["retries"], w.captured["customers"] = seen, len(all)
		for _, c := range all { // an EventSource reconnects: the old connection ends
			c.cancel()
		}
		return nil
	})
	sc.Step(`^no single retry value is held by more than 5 % of them$`, func() error {
		w := W()
		for v, n := range w.captured["retries"].(map[int]int) {
			if n*100 > 5*w.captured["customers"].(int) {
				return fmt.Errorf("retry %d held by %d customers", v, n)
			}
		}
		return nil
	})
	sc.Step(`^all connections close within 20 seconds and the process exits 0$`, func() error { return W().expectDrained() })
	sc.Step(`^their connections are closed by the server and the process exits 0$`, func() error {
		w := W()
		for _, c := range w.captured["ignorers"].([]*client) {
			if err := w.waitClosed(c); err != nil {
				return err
			}
		}
		return w.expectDrained()
	})

	// ---- health
	sc.Step(`^GET /health is called$`, func() error { W().health(); return nil })
	sc.Step(`^GET /health answers 503$`, func() error {
		w := W()
		w.health()
		return w.expectStatus(http.StatusServiceUnavailable)
	})
	sc.Step(`^GET /health reports the server as degraded but alive$`, func() error {
		w := W()
		w.health()
		if err := w.expectStatus(200); err != nil {
			return err
		}
		if !strings.Contains(w.last.body, "degraded") {
			return fmt.Errorf("health body %q", w.last.body)
		}
		return nil
	})

	// ---- metrics
	sc.Step(`^the gauge "([^"]*)" for "([^"]*)" is (\d+)$`, func(name, id string, want float64) error {
		W().settle()
		got, ok := W().metrics.gauge(name, id)
		if !ok || got != want {
			return fmt.Errorf("gauge %s{%s} = %v (set=%v), want %v", name, id, got, ok, want)
		}
		return nil
	})
}

// ---------------------------------------------------------------- helpers

func nonComment(fs []frame) []frame {
	var out []frame
	for _, f := range fs {
		if !f.comment {
			out = append(out, f)
		}
	}
	return out
}

// countStale counts stale messages; want is "true", "false" or "" for any.
func countStale(c *client, want string) int {
	n := 0
	for _, f := range c.named("stale") {
		if want == "" || strings.Contains(f.data, `"stale":`+want) {
			n++
		}
	}
	return n
}

// dropRedis loses the subscription and keeps Redis down while the server retries.
func (w *world) dropRedis() {
	w.source.setDown(true)
	w.source.drop()
	w.pass(20 * time.Second)
}

// restoreRedis lets the server resubscribe: it walks the clock through the backoff.
func (w *world) restoreRedis() error {
	w.source.setDown(false)
	before := w.source.subscribeCalls()
	for i := 0; i < 12 && w.source.subscribeCalls() == before; i++ {
		w.pass(time.Second)
	}
	if w.source.subscribeCalls() == before {
		return fmt.Errorf("the server did not resubscribe within 12 s")
	}
	w.settle()
	return nil
}

func (w *world) startDrain() {
	if w.drain != nil {
		return
	}
	w.drain = make(chan error, 1)
	srv := w.srv()
	go func() { w.drain <- srv.Drain(context.Background()) }()
	w.settle()
}

func (w *world) expectDrained() error {
	select {
	case err := <-w.drain:
		return err
	case <-time.After(3 * time.Second):
		return fmt.Errorf("the server did not finish draining")
	}
}

func (w *world) health() {
	rec := httptest.NewRecorder()
	w.srv().Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	w.last = &connectResult{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}
