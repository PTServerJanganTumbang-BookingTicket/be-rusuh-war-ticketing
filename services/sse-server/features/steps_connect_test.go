package features

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

var credentialPattern = regexp.MustCompile(`^(cookie queue_token=(\S+)|header X-Queue-Token: (\S+)|header Authorization: Bearer (\S+))$`)

func registerConnectSteps(sc *godog.ScenarioContext, W func() *world) {
	// ---- setup
	sc.Step(`^event "([^"]*)" is "OPEN" with now_serving (\d+) and rate_per_min (\d+)$`, func(id string, ns, rate int64) error {
		return W().seedEvent(id, "OPEN", ns, rate)
	})
	sc.Step(`^event "([^"]*)" is "([^"]*)"$`, func(id, state string) error {
		w := W()
		if err := w.ensureEvent(id); err != nil {
			return err
		}
		return w.setState(id, state)
	})
	sc.Step(`^"([^"]*)" is "([^"]*)"( and nowServing does not move)?$`, func(id, state, _ string) error {
		w := W()
		if err := w.ensureEvent(id); err != nil {
			return err
		}
		if err := w.setState(id, state); err != nil {
			return err
		}
		// admission-ctl announces a change; customers already connected know it
		if w.me != nil && (state == "FULL_BOOKED" || state == "OPEN") {
			if err := w.publishStatus(id, state); err != nil {
				return err
			}
			w.settle()
			w.me.takeNew()
		}
		return nil
	})
	sc.Step(`^the clock is frozen at "([^"]*)"$`, func(at string) error {
		t, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return err
		}
		if !W().integration {
			W().clk.set(t)
		}
		return nil
	})
	sc.Step(`^queue token "([^"]*)" exists for event "([^"]*)" with ticket_no (\d+)$`, func(tok, id string, n int64) error {
		w := W()
		if err := w.ensureEvent(id); err != nil {
			return err
		}
		return w.seedToken(tok, id, n, "")
	})
	sc.Step(`^queue token "([^"]*)" was issued to a guest on device "([^"]*)"$`, func(tok, dev string) error {
		return W().seedToken(tok, "E1", 31700, dev)
	})
	sc.Step(`^event "([^"]*)" has no queue state in Redis$`, func(id string) error {
		return W().rdb.Del(context.Background(), ev(id).QueueState()).Err()
	})
	sc.Step(`^the hash queue:\{E1\} has no rate_per_min field$`, func() error {
		return W().rdb.HDel(context.Background(), ev("E1").QueueHash(), "rate_per_min").Err()
	})
	sc.Step(`^the rate limiter cannot reach Redis$`, func() error {
		w := W()
		w.srv()
		w.limiter.fail = true
		return nil
	})
	sc.Step(`^Redis is unreachable$`, func() error {
		w := W()
		w.srv()
		w.mr.Close()
		return nil
	})
	sc.Step(`^IP "([^"]*)" opened 5 streams in the last second$`, func(ip string) error {
		w := W()
		w.srv()
		for i := 0; i < 5; i++ {
			if _, err := w.limiter.Allow(context.Background(), rediskeys.SSERateLimit(ip), w.cfg.IPLimit); err != nil {
				return err
			}
		}
		return nil
	})

	// ---- opening the stream
	sc.Step(`^I open the stream with query "([^"]*)" and no other credential$`, func(q string) error {
		w := W()
		w.me = w.open("E1", openOpts{query: q})
		return nil
	})
	sc.Step(`^I open the stream with header X-Queue-Token: (\S+) and header "([^"]*)"$`, func(tok, h string) error {
		w := W()
		k, v, _ := strings.Cut(h, ": ")
		w.me = w.open("E1", openOpts{header: map[string]string{"X-Queue-Token": tok, k: v}})
		return nil
	})
	sc.Step(`^I open the stream from IP "([^"]*)" with cookie queue_token=(\S+)$`, func(ip, tok string) error {
		w := W()
		w.me = w.open("E1", openOpts{cookie: tok, ip: ip})
		return nil
	})
	sc.Step(`^I open the stream of "([^"]*)" with cookie queue_token=(\S+)$`, func(id, tok string) error {
		w := W()
		w.me = w.open(id, openOpts{cookie: tok})
		return nil
	})
	sc.Step(`^I open the stream with no credential at all$`, func() error {
		w := W()
		w.me = w.open("E1", openOpts{})
		return nil
	})
	sc.Step(`^I reopen the stream with Last-Event-ID (\d+)$`, func(id string) error {
		w := W()
		w.me = w.open("E1", openOpts{cookie: "qt_alice", lastEvt: id})
		return nil
	})
	sc.Step(`^I open the stream with ((?:cookie queue_token=|header X-Queue-Token: |header Authorization: Bearer )\S+)$`, func(cred string) error {
		w := W()
		m := credentialPattern.FindStringSubmatch(cred)
		if m == nil {
			return fmt.Errorf("unknown credential %q", cred)
		}
		o := openOpts{}
		switch {
		case m[2] != "":
			o.cookie = m[2]
		case m[3] != "":
			o.header = map[string]string{"X-Queue-Token": m[3]}
		default:
			o.header = map[string]string{"Authorization": "Bearer " + m[4]}
		}
		w.me = w.open("E1", o)
		return nil
	})
	sc.Step(`^I can still open the stream from IP "([^"]*)"$`, func(ip string) error {
		w := W()
		c := w.open("E1", openOpts{cookie: "qt_alice", ip: ip})
		if c.w.code() != 200 {
			return fmt.Errorf("status %d from %s", c.w.code(), ip)
		}
		return nil
	})
	sc.Step(`^two customers with different tokens open the stream$`, func() error {
		w := W()
		if err := w.seedToken("qt_bob", "E1", 31800, ""); err != nil {
			return err
		}
		a := w.open("E1", openOpts{cookie: "qt_alice", ip: "10.0.0.1"})
		b := w.open("E1", openOpts{cookie: "qt_bob", ip: "10.0.0.2"})
		w.captured["pair"] = []*client{a, b}
		return nil
	})

	// ---- the answer
	sc.Step(`^the response status is (\d+)$`, func(code int) error { return W().expectStatus(code) })
	sc.Step(`^the response status is (\d+) with error_code "([^"]*)"$`, func(code int, ec string) error {
		w := W()
		if err := w.expectStatus(code); err != nil {
			return err
		}
		return w.expectMeta("error_code", ec)
	})
	sc.Step(`^the message is "([^"]*)"$`, func(msg string) error { return W().expectMeta("message", msg) })
	sc.Step(`^a Retry-After header is present$`, func() error {
		if W().last.header.Get("Retry-After") == "" {
			return fmt.Errorf("no Retry-After header")
		}
		return nil
	})
	sc.Step(`^I am connected to a "([^"]*)" with no caching or buffering$`, func(ct string) error {
		h := W().last.header
		if err := W().expectStatus(200); err != nil {
			return err
		}
		if h.Get("Content-Type") != ct || h.Get("Cache-Control") != "no-cache" || h.Get("X-Accel-Buffering") != "no" {
			return fmt.Errorf("headers: %v", h)
		}
		return nil
	})
	sc.Step(`^the first thing I receive is:$`, func(doc *godog.DocString) error {
		w := W()
		got := norm(w.me.w.body())
		if !strings.HasPrefix(got, norm(doc.Content)) {
			return fmt.Errorf("first bytes:\n%s\nwant prefix:\n%s", got, norm(doc.Content))
		}
		w.me.takeNew()
		return nil
	})
	sc.Step(`^my first message has nowServing (\d+)$`, func(n int64) error { return W().firstField("nowServing", n) })
	sc.Step(`^my first message has ratePerMin (\d+)$`, func(n int64) error { return W().firstField("ratePerMin", n) })
	sc.Step(`^my first message has the current nowServing$`, func() error {
		w := W()
		return w.firstField("nowServing", w.nowServing("E1"))
	})
	sc.Step(`^I was connected when now_serving was (\d+) and now_serving is now (\d+)$`, func(_, now int64) error {
		return W().rdb.HSet(context.Background(), ev("E1").QueueHash(), "now_serving", now).Err()
	})
	sc.Step(`^my ticket_no is (\d+)$`, func(int64) error { return nil })
	sc.Step(`^both first messages are identical except for serverTime$`, func() error {
		pair, _ := W().captured["pair"].([]*client)
		if len(pair) != 2 {
			return fmt.Errorf("no pair")
		}
		strip := regexp.MustCompile(`"serverTime":"[^"]*"`)
		a, b := strip.ReplaceAllString(norm(pair[0].w.body()), ""), strip.ReplaceAllString(norm(pair[1].w.body()), "")
		if a != b || a == "" {
			return fmt.Errorf("bodies differ:\n%s\n%s", a, b)
		}
		return nil
	})
	sc.Step(`^neither contains a ticket number, token or position$`, func() error {
		for _, c := range W().captured["pair"].([]*client) {
			b := c.w.body()
			if strings.Contains(b, "ticket") || strings.Contains(b, "qt_") || strings.Contains(b, "position") {
				return fmt.Errorf("personal data in %q", b)
			}
		}
		return nil
	})
	sc.Step(`^no connection is registered for "([^"]*)"$`, func(id string) error {
		if n := W().srv().Connections(id); n != 0 {
			return fmt.Errorf("%d connections registered for %s", n, id)
		}
		return nil
	})
	sc.Step(`^no log line and no metric label contains "([^"]*)"$`, func(secret string) error {
		w := W()
		w.settle()
		if strings.Contains(w.logs.String(), secret) || strings.Contains(w.metrics.names(), secret) {
			return fmt.Errorf("%q leaked into logs or metrics", secret)
		}
		return nil
	})
}

func (w *world) expectStatus(code int) error {
	if w.last == nil {
		return fmt.Errorf("no response yet")
	}
	if w.last.status != code {
		return fmt.Errorf("status = %d, want %d; body %s", w.last.status, code, truncate(w.last.body))
	}
	return nil
}

func (w *world) expectMeta(key, want string) error {
	var env struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal([]byte(w.last.body), &env); err != nil {
		return fmt.Errorf("body is not an envelope: %q", truncate(w.last.body))
	}
	if got := fmt.Sprint(env.Meta[key]); got != want {
		return fmt.Errorf("meta.%s = %q, want %q", key, got, want)
	}
	return nil
}

// firstField reads one numeric field of the first data message of my stream.
func (w *world) firstField(field string, want int64) error {
	for _, f := range w.me.frames() {
		if f.comment || f.event != "" || f.data == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(f.data), &m); err != nil {
			return err
		}
		got, _ := m[field].(float64)
		if int64(got) != want {
			return fmt.Errorf("%s = %v, want %d", field, m[field], want)
		}
		return nil
	}
	return fmt.Errorf("no data message received; body %q", truncate(w.me.w.body()))
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

var _ = strconv.Itoa
