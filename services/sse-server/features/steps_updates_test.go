package features

import (
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

func registerUpdateSteps(sc *godog.ScenarioContext, W func() *world) {
	// ---- who is connected
	sc.Step(`^I am connected to "([^"]*)"$`, func(id string) error {
		w := W()
		if err := w.ensureEvent(id); err != nil {
			return err
		}
		if err := w.seedToken(myToken, id, myTicket, ""); err != nil {
			return err
		}
		w.me = w.open(id, openOpts{cookie: myToken})
		if err := w.expectStatus(200); err != nil {
			return err
		}
		w.me.takeNew() // the snapshot is checked by the connect scenarios; here I wait for what comes next
		return nil
	})
	sc.Step(`^(\d+) other customers are connected to "([^"]*)"$`, func(n int, id string) error {
		cs, err := W().connectMany(W().srv(), id, n)
		W().captured["others"] = cs
		return err
	})
	sc.Step(`^(\d+) customers are connected to "([^"]*)"$`, func(n int, id string) error {
		_, err := W().connectMany(W().srv(), id, n)
		return err
	})
	sc.Step(`^another customer is connected to "([^"]*)"$`, func(id string) error {
		_, err := W().connectMany(W().srv(), id, 1)
		return err
	})
	sc.Step(`^(\d+) customers are connected$`, func(n int) error {
		w := W()
		cs, err := w.connectMany(w.srv(), "E1", n)
		w.captured["others"] = cs
		return err
	})
	sc.Step(`^(\d+) customers are connected and 1 never reads$`, func(n int) error {
		w := W()
		cs, err := w.connectMany(w.srv(), "E1", n-1)
		if err != nil {
			return err
		}
		w.captured["others"] = cs
		w.captured["slow"] = w.openSlow("E1")
		return nil
	})
	sc.Step(`^(\d+) customers are connected and one leaves during a broadcast$`, func(n int) error {
		w := W()
		cs, err := w.connectMany(w.srv(), "E1", n)
		w.captured["others"] = cs
		w.captured["leaver"] = cs[0]
		return err
	})
	sc.Step(`^(\d+) customers connect to "([^"]*)" and leave$`, func(n int, id string) error {
		w := W()
		srv := w.srv()
		w.captured["goroutines"] = runtime.NumGoroutine()
		for i := 0; i < n; i++ {
			cs, err := w.connectMany(srv, id, 1)
			if err != nil {
				return err
			}
			cs[0].leave()
		}
		w.settle()
		return nil
	})
	sc.Step(`^my connection was dropped for being too slow$`, func() error {
		w := W()
		slow := w.openSlow("E1")
		for i := 0; i < 3*connBuffer; i++ {
			if err := w.publishBroadcast("E1", 31601+int64(i), ""); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
		}
		w.settle()
		close(slow.w.block)
		select {
		case <-slow.done:
		case <-time.After(2 * time.Second):
			return fmt.Errorf("the slow connection was not dropped")
		}
		return nil
	})
	sc.Step(`^I reconnect$`, func() error {
		w := W()
		w.me = w.open("E1", openOpts{cookie: myToken})
		return nil
	})
	sc.Step(`^I registered after the CLOSED message was published$`, func() error { return nil })
	sc.Step(`^the last nowServing I received was (\d+)$`, func(n int64) error {
		w := W()
		if err := w.publishBroadcast("E1", n, ""); err != nil {
			return err
		}
		w.settle()
		w.me.takeNew()
		return nil
	})

	// ---- admission-ctl publishes
	sc.Step(`^admission-ctl publishes on sse-broadcast:$`, func(doc *godog.DocString) error {
		w := W()
		err := w.publish(rediskeys.ChannelBroadcast, []byte(doc.Content))
		w.settle()
		return err
	})
	sc.Step(`^admission-ctl publishes on sse-broadcast: (.+)$`, func(raw string) error {
		w := W()
		err := w.publish(rediskeys.ChannelBroadcast, []byte(raw))
		w.settle()
		return err
	})
	sc.Step(`^admission-ctl publishes on sse-status:$`, func(doc *godog.DocString) error {
		w := W()
		err := w.publish(rediskeys.ChannelStatus, []byte(doc.Content))
		w.settle()
		return err
	})
	sc.Step(`^admission-ctl publishes on sse-status state "([^"]*)" for "([^"]*)"( twice)?$`, func(state, id, twice string) error {
		w := W()
		n := 1
		if twice != "" {
			n = 2
		}
		for i := 0; i < n; i++ {
			if err := w.publishStatus(id, state); err != nil {
				return err
			}
		}
		w.settle()
		return nil
	})
	sc.Step(`^admission-ctl publishes sse-status state "([^"]*)"$`, func(state string) error {
		w := W()
		err := w.publishStatus("E1", state)
		w.settle()
		return err
	})
	sc.Step(`^(?:admission-ctl publishes one broadcast|a broadcast for "([^"]*)" is published)$`, func(id string) error {
		if id == "" {
			id = "E1"
		}
		w := W()
		err := w.publishBroadcast(id, w.nowServing(id)+4, "")
		w.settle()
		return err
	})
	sc.Step(`^admission-ctl publishes a broadcast after Redis is back$`, func() error {
		w := W()
		err := w.publishBroadcast("E1", w.nowServing("E1")+4, "")
		w.settle()
		return err
	})
	sc.Step(`^a broadcast with nowServing (\d+) is published$`, func(n int64) error {
		w := W()
		err := w.publishBroadcast("E1", n, "")
		w.settle()
		return err
	})
	sc.Step(`^a late broadcast with nowServing (\d+) is published$`, func(n int64) error {
		w := W()
		err := w.publishBroadcast("E1", n, "")
		w.settle()
		return err
	})
	sc.Step(`^a broadcast with state "([^"]*)" is published$`, func(state string) error {
		w := W()
		err := w.publishBroadcast("E1", w.nowServing("E1")+4, state)
		w.settle()
		return err
	})
	sc.Step(`^a heartbeat with nowServing (\d+) is delivered$`, func(n int64) error {
		w := W()
		if err := w.publishBroadcast("E1", n, ""); err != nil {
			return err
		}
		w.settle()
		return w.expectData(w.me, n)
	})
	sc.Step(`^(\d+) broadcasts are published$`, func(n int) error {
		w := W()
		var worst time.Duration
		for i := 0; i < n; i++ {
			start := time.Now()
			if err := w.publishBroadcast("E1", 31601+int64(i), ""); err != nil {
				return err
			}
			if d := time.Since(start); d > worst {
				worst = d
			}
			time.Sleep(time.Millisecond)
		}
		w.captured["worstPublish"] = worst
		w.settle()
		return nil
	})

	// ---- what I receive
	sc.Step(`^I receive:$`, func(doc *godog.DocString) error { return W().expectNew(W().me, doc.Content, false) })
	sc.Step(`^I receive exactly:$`, func(doc *godog.DocString) error { return W().expectNew(W().me, doc.Content, true) })
	sc.Step(`^I receive, in this order:$`, func(doc *godog.DocString) error { return W().expectNew(W().me, doc.Content, false) })
	sc.Step(`^I receive nothing( and stay connected)?$`, func(stay string) error {
		w := W()
		w.settle()
		if got := w.me.takeNew(); got != "" {
			return fmt.Errorf("received %q, want nothing", got)
		}
		if stay != "" && !w.me.open() {
			return fmt.Errorf("the connection was closed")
		}
		return nil
	})
	sc.Step(`^I receive nowServing (\d+)$`, func(n int64) error { return W().expectData(W().me, n) })
	sc.Step(`^I receive a "status" message with state "([^"]*)" and fullBookedAt null$`, func(state string) error {
		w := W()
		w.settle()
		got := w.me.takeNew()
		want := fmt.Sprintf(`"state":%q,"fullBookedAt":null`, state)
		if !strings.Contains(got, "event: status") || !strings.Contains(got, want) {
			return fmt.Errorf("received %q, want status with %s", got, want)
		}
		return nil
	})
	sc.Step(`^I receive 0 "status" messages$`, func() error {
		w := W()
		w.settle()
		if got := w.me.takeNew(); strings.Contains(got, "event: status") {
			return fmt.Errorf("received a status message: %q", got)
		}
		return nil
	})
	sc.Step(`^I receive "status" CLOSED, then "sold-out", and my response ends$`, func() error { return W().expectClosedSequence(W().me) })
	sc.Step(`^I receive it and the customers of "([^"]*)" receive nothing$`, func(id string) error {
		w := W()
		if got := w.me.takeNew(); !strings.Contains(got, "data:") {
			return fmt.Errorf("I received %q", got)
		}
		for _, c := range w.byEvent[id] {
			if got := c.takeNew(); got != "" {
				return fmt.Errorf("a customer of %s received %q", id, got)
			}
		}
		return nil
	})
	sc.Step(`^the customers of "([^"]*)" are still connected$`, func(id string) error {
		for _, c := range W().byEvent[id] {
			if !c.open() {
				return fmt.Errorf("a customer of %s was disconnected", id)
			}
		}
		return nil
	})
	sc.Step(`^I am still connected$`, func() error {
		if !W().me.open() {
			return fmt.Errorf("my connection was closed")
		}
		return nil
	})
	sc.Step(`^my response ends and I am no longer registered$`, func() error {
		w := W()
		if err := w.waitClosed(w.me); err != nil {
			return err
		}
		return W().expectUnregistered("E1")
	})
	sc.Step(`^all (\d+) of us receive identical bytes$`, func(n int) error {
		w := W()
		all := append([]*client{w.me}, w.captured["others"].([]*client)...)
		if len(all) != n {
			return fmt.Errorf("%d customers, want %d", len(all), n)
		}
		want := all[0].w.body()
		for i, c := range all {
			if c.w.body() != want {
				return fmt.Errorf("customer %d received different bytes", i)
			}
		}
		return nil
	})
	sc.Step(`^the message was built once, not once per customer$`, func() error {
		if n := W().metrics.count("SSEMessageBuilt"); n != 1 {
			return fmt.Errorf("SSEMessageBuilt = %d, want 1", n)
		}
		return nil
	})
	sc.Step(`^no "grant" message is ever sent to anyone$`, func() error {
		for _, c := range W().clients {
			if strings.Contains(c.w.body(), "event: grant") {
				return fmt.Errorf("a grant message was sent")
			}
		}
		return nil
	})
	sc.Step(`^the other 99 receive it$`, func() error {
		w := W()
		for _, c := range w.captured["others"].([]*client) {
			if c == w.captured["leaver"] {
				continue
			}
			if !strings.Contains(c.w.body(), "data:") {
				return fmt.Errorf("a customer did not receive the broadcast")
			}
		}
		return nil
	})
	sc.Step(`^the slow connection is closed and counted in "([^"]*)"$`, func(metric string) error {
		w := W()
		slow := w.captured["slow"].(*client)
		close(slow.w.block)
		select {
		case <-slow.done:
		case <-time.After(2 * time.Second):
			return fmt.Errorf("the slow connection was not closed")
		}
		if w.metrics.count(metric) < 1 {
			return fmt.Errorf("%s not counted", metric)
		}
		return nil
	})
	sc.Step(`^the other 99 received all 50$`, func() error {
		for i, c := range W().captured["others"].([]*client) {
			if n := strings.Count(c.w.body(), "data: "); n < 51 {
				return fmt.Errorf("customer %d received %d messages, want 51", i, n)
			}
		}
		return nil
	})
	sc.Step(`^no broadcast waited more than 50 ms on the slow one$`, func() error {
		if d, _ := W().captured["worstPublish"].(time.Duration); d > 50*time.Millisecond {
			return fmt.Errorf("a publish took %s", d)
		}
		return nil
	})
	sc.Step(`^the metric "([^"]*)" is (\d+)$`, func(name string, want int) error {
		W().settle()
		if got := W().metrics.count(name); got != want {
			return fmt.Errorf("metric %s = %d, want %d", name, got, want)
		}
		return nil
	})
	sc.Step(`^"([^"]*)" has only my connection$`, func(id string) error { return W().expectRegistered(id, 1) })
	sc.Step(`^the goroutine count is back to its start$`, func() error {
		start := W().captured["goroutines"].(int)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if runtime.NumGoroutine() <= start+5 {
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		return fmt.Errorf("goroutines: %d now, %d at the start", runtime.NumGoroutine(), start)
	})
}

// ---------------------------------------------------------------- helpers

func (w *world) openSlow(id string) *client {
	c := w.open(id, openOpts{slow: true, cookie: w.slowToken(id)})
	return c
}

func (w *world) slowToken(id string) string {
	w.tokens++
	tok := fmt.Sprintf("qt_slow%03d", w.tokens)
	_ = w.seedToken(tok, id, int64(50000+w.tokens), "")
	return tok
}

func (w *world) publishBroadcast(id string, nowServing int64, state string) error {
	if state == "" {
		state, _ = w.rdb.HGet(contextBG(), ev(id).QueueHash(), "state").Result()
		if state == "" {
			state = "OPEN"
		}
	}
	// admission-ctl writes now_serving to Redis, then publishes it (never lowering it)
	if nowServing > w.nowServing(id) {
		if err := w.rdb.HSet(contextBG(), ev(id).QueueHash(), "now_serving", nowServing).Err(); err != nil {
			return err
		}
	}
	p := fmt.Sprintf(`{"eventId":%q,"nowServing":%d,"state":%q,"ratePerMin":318,"serverTime":%d}`,
		id, nowServing, state, w.clock().Now().Unix())
	return w.publish(rediskeys.ChannelBroadcast, []byte(p))
}

func (w *world) publishStatus(id, state string) error {
	var full any
	if state == "FULL_BOOKED" {
		full = w.clock().Now().Unix()
	}
	b, _ := json.Marshal(map[string]any{"eventId": id, "state": state, "fullBookedAt": full, "serverTime": w.clock().Now().Unix()})
	return w.publish(rediskeys.ChannelStatus, b)
}

var retryValue = regexp.MustCompile(`retry: \d+`)

// expectNew compares what arrived since the last assertion with a docstring.
// With prefix set, only as many frames as the docstring has are consumed, so
// "I receive exactly: <this frame>" can be followed by "then <the next one>".
var serverTimeValue = regexp.MustCompile(`"serverTime":"[^"]*"`)

func (w *world) expectNew(c *client, want string, prefix bool) error {
	w.settle()
	var got string
	if prefix {
		got = c.take(len(parseFrames(want)))
	} else {
		got = c.takeNew()
	}
	if strings.Contains(want, "event: reconnect") { // the retry value is random; the docstring shows an example
		got = retryValue.ReplaceAllString(got, "retry: 7300")
	}
	if strings.Contains(want, `"serverTime":"<any>"`) { // the moment depends on when the fake clock moved
		got = serverTimeValue.ReplaceAllString(got, `"serverTime":"<any>"`)
	}
	if norm(got) != norm(want) {
		return fmt.Errorf("received:\n%s\nwant:\n%s", norm(got), norm(want))
	}
	return nil
}

func (w *world) expectData(c *client, nowServing int64) error {
	w.settle()
	got := c.takeNew()
	if !strings.Contains(got, fmt.Sprintf(`"nowServing":%d`, nowServing)) {
		return fmt.Errorf("received %q, want nowServing %d", got, nowServing)
	}
	return nil
}

func (w *world) waitClosed(c *client) error {
	select {
	case <-c.done:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("the response did not end")
	}
}

func (w *world) expectUnregistered(id string) error { return w.expectRegistered(id, 0) }

func (w *world) expectRegistered(id string, want int) error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.srv().Connections(id) == want {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("%d connections registered for %s, want %d", w.srv().Connections(id), id, want)
}

// expectClosedSequence: status CLOSED, then sold-out, then the response ends.
func (w *world) expectClosedSequence(c *client) error {
	w.settle()
	got := c.takeNew()
	i, j := strings.Index(got, "event: status"), strings.Index(got, "event: sold-out")
	if i < 0 || j < 0 || i > j || !strings.Contains(got[i:j], `"state":"CLOSED"`) {
		return fmt.Errorf("received %q, want status CLOSED then sold-out", got)
	}
	return w.waitClosed(c)
}

var _ sync.Mutex
