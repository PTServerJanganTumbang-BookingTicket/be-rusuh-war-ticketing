package features

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	queuehttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/http"
	queueredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/redis"
)

func (r *response) json() (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		return nil, fmt.Errorf("body is not a JSON object (%d): %q", r.status, truncate(string(r.body)))
	}
	return m, nil
}

func (r *response) data() (map[string]any, error) {
	m, err := r.json()
	if err != nil {
		return nil, err
	}
	d, ok := m["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("data is not an object: %s", truncate(string(r.body)))
	}
	return d, nil
}

func (r *response) meta() (map[string]any, error) {
	m, err := r.json()
	if err != nil {
		return nil, err
	}
	d, ok := m["meta"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("meta is not an object: %s", truncate(string(r.body)))
	}
	return d, nil
}

func (r *response) queueToken() string {
	d, _ := r.data()
	s, _ := d["queue_token"].(string)
	return s
}

func (r *response) cookie(name string) (*http.Cookie, error) {
	for _, line := range r.header.Values("Set-Cookie") {
		c, err := http.ParseSetCookie(line)
		if err == nil && c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no Set-Cookie %q in %v", name, r.header.Values("Set-Cookie"))
}

func truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

func (w *world) last() (*response, error) {
	if w.resp == nil {
		return nil, errors.New("no join was sent")
	}
	return w.resp, nil
}

func expectStatus(r *response, status int) error {
	if r.status != status {
		return fmt.Errorf("status = %d, want %d; body %s", r.status, status, truncate(string(r.body)))
	}
	return nil
}

func expectCode(r *response, status int, code string) error {
	if err := expectStatus(r, status); err != nil {
		return err
	}
	m, err := r.meta()
	if err != nil {
		return err
	}
	if m["error_code"] != code {
		return fmt.Errorf("meta.error_code = %v, want %q; body %s", m["error_code"], code, truncate(string(r.body)))
	}
	return nil
}

func num(v any) (int64, bool) {
	f, ok := v.(float64)
	return int64(f), ok
}

func registerAssertSteps(sc *godog.ScenarioContext, W func() *world) {
	lastDo := func(f func(*response) error) error {
		r, err := W().last()
		if err != nil {
			return err
		}
		return f(r)
	}

	// ---- status and envelope
	sc.Step(`^the response status is (\d+)$`, func(s int) error { return lastDo(func(r *response) error { return expectStatus(r, s) }) })
	sc.Step(`^the response status is (\d+) with error_code "([^"]*)"$`, func(s int, code string) error {
		return lastDo(func(r *response) error { return expectCode(r, s, code) })
	})
	sc.Step(`^the response status is (\d+) with message "([^"]*)"$`, func(s int, msg string) error {
		return lastDo(func(r *response) error {
			if err := expectStatus(r, s); err != nil {
				return err
			}
			m, err := r.meta()
			if err != nil {
				return err
			}
			if m["message"] != msg {
				return fmt.Errorf("meta.message = %v, want %q", m["message"], msg)
			}
			return nil
		})
	})
	sc.Step(`^the response status is (\d+) and data\.ticket_no is (\d+)$`, func(s int, n int64) error {
		return lastDo(func(r *response) error {
			if err := expectStatus(r, s); err != nil {
				return err
			}
			return dataInt(r, "ticket_no", n)
		})
	})
	sc.Step(`^the response status is (\d+) with rejoined true$`, func(s int) error {
		return lastDo(func(r *response) error {
			if err := expectStatus(r, s); err != nil {
				return err
			}
			return dataHas(r, "rejoined true")
		})
	})
	sc.Step(`^both responses have status (\d+)$`, func(s int) error { return W().allStatus(s) })
	sc.Step(`^every response status is (\d+)$`, func(s int) error { return W().allStatus(s) })
	sc.Step(`^the 4th response status is (\d+)$`, func(s int) error {
		w := W()
		if len(w.resps) < 4 {
			return fmt.Errorf("only %d responses", len(w.resps))
		}
		return expectStatus(w.resps[3], s)
	})
	sc.Step(`^the message is "([^"]*)"$`, func(want string) error {
		return lastDo(func(r *response) error {
			m, err := r.meta()
			if err != nil {
				return err
			}
			if m["message"] != want {
				return fmt.Errorf("meta.message = %v, want %q", m["message"], want)
			}
			return nil
		})
	})
	sc.Step(`^the body is a valid error envelope$`, func() error { return lastDo(validErrorEnvelope) })
	sc.Step(`^the response body matches:$`, func(doc *godog.DocString) error {
		return lastDo(func(r *response) error { return W().bodyMatches(r, doc.Content) })
	})
	sc.Step(`^the response has no "([^"]*)" field$`, func(f string) error {
		return lastDo(func(r *response) error {
			if strings.Contains(string(r.body), `"`+f+`"`) {
				return fmt.Errorf("body has %q: %s", f, truncate(string(r.body)))
			}
			return nil
		})
	})
	sc.Step(`^meta has no "([^"]*)"$`, func(f string) error {
		return lastDo(func(r *response) error {
			m, err := r.meta()
			if err != nil {
				return err
			}
			if _, ok := m[f]; ok {
				return fmt.Errorf("meta has %q", f)
			}
			return nil
		})
	})

	// ---- data
	sc.Step(`^data is (\{.*\})$`, func(want string) error {
		return lastDo(func(r *response) error {
			var exp any
			if err := json.Unmarshal([]byte(want), &exp); err != nil {
				return err
			}
			d, err := r.data()
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(any(d), exp) {
				got, _ := json.Marshal(d)
				return fmt.Errorf("data = %s, want %s", got, want)
			}
			return nil
		})
	})
	sc.Step(`^data\.queue_token is a "qt_" token$`, func() error {
		return lastDo(func(r *response) error {
			if !qtPattern.MatchString(r.queueToken()) {
				return fmt.Errorf("queue_token %q is not qt_ + ULID", r.queueToken())
			}
			return nil
		})
	})
	sc.Step(`^data\.retry_after is a positive number of seconds$`, func() error {
		return lastDo(func(r *response) error {
			d, err := r.data()
			if err != nil {
				return err
			}
			if n, ok := num(d["retry_after"]); !ok || n <= 0 {
				return fmt.Errorf("data.retry_after = %v", d["retry_after"])
			}
			return nil
		})
	})
	sc.Step(`^the response has a Retry-After header with the same value$`, func() error {
		return lastDo(func(r *response) error {
			d, _ := r.data()
			n, _ := num(d["retry_after"])
			if r.header.Get("Retry-After") != strconv.FormatInt(n, 10) {
				return fmt.Errorf("Retry-After = %q, data.retry_after = %d", r.header.Get("Retry-After"), n)
			}
			return nil
		})
	})
	sc.Step(`^data\.ticket_no is (\d+)$`, func(n int64) error { return lastDo(func(r *response) error { return dataInt(r, "ticket_no", n) }) })
	sc.Step(`^data\.ticket_no is (\d+) and data\.position is (\d+)$`, func(n, p int64) error {
		return lastDo(func(r *response) error {
			if err := dataInt(r, "ticket_no", n); err != nil {
				return err
			}
			return dataInt(r, "position", p)
		})
	})
	sc.Step(`^data has no (\w+) field$`, func(f string) error {
		return lastDo(func(r *response) error {
			d, err := r.data()
			if err != nil {
				return err
			}
			if _, ok := d[f]; ok {
				return fmt.Errorf("data has %q: %v", f, d)
			}
			return nil
		})
	})
	sc.Step(`^data has ((?:rejoined|ticket_no|state|queue_token|now_serving|position) .+)$`, func(pairs string) error { return lastDo(func(r *response) error { return dataHas(r, pairs) }) })
	sc.Step(`^the frontend routes by state \(position fields are ignored\)$`, func() error {
		return lastDo(func(r *response) error {
			d, _ := r.data()
			if d["state"] == domain.EngagementQueued || d["state"] == nil {
				return fmt.Errorf("state = %v: nothing to route on", d["state"])
			}
			return nil
		})
	})
	sc.Step(`^the response is a normal new join for "([^"]*)"$`, func(id string) error {
		w := W()
		r, err := w.last()
		if err != nil {
			return err
		}
		if err := expectStatus(r, 200); err != nil {
			return err
		}
		m, _ := r.meta()
		d, _ := r.data()
		if m["message"] != "Successfully joined queue" || d["rejoined"] != nil {
			return fmt.Errorf("not a new join: %s", truncate(string(r.body)))
		}
		if presented, _ := w.captured["presented"].(string); presented != "" && r.queueToken() == presented {
			return fmt.Errorf("the presented queue_token %q was reused", presented)
		}
		ev, _ := d["event"].(map[string]any)
		if ev["id"] != id {
			return fmt.Errorf("event.id = %v, want %s", ev["id"], id)
		}
		return nil
	})

	// ---- cookie
	cookieIs := func(want func(r *response) string) func(string) error {
		return func(name string) error {
			return lastDo(func(r *response) error {
				c, err := r.cookie(name)
				if err != nil {
					return err
				}
				if c.Value != want(r) || c.Value == "" {
					return fmt.Errorf("cookie %s = %q, want %q", name, c.Value, want(r))
				}
				return nil
			})
		}
	}
	sc.Step(`^the response sets cookie "([^"]*)" to the same value as data\.queue_token$`, cookieIs((*response).queueToken))
	sc.Step(`^the response sets cookie "([^"]*)" to data\.queue_token$`, cookieIs((*response).queueToken))
	sc.Step(`^the response sets cookie "([^"]*)" to "([^"]*)"$`, func(name, v string) error {
		return cookieIs(func(*response) string { return v })(name)
	})
	sc.Step(`^the cookie is HttpOnly, Secure, SameSite=Strict, Path=(\S+) and Max-Age=(\d+)$`, func(path string, age int) error {
		return lastDo(func(r *response) error {
			c, err := r.cookie(queuehttp.CookieName)
			if err != nil {
				return err
			}
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != path || c.MaxAge != age {
				return fmt.Errorf("cookie attributes: %s", c.String())
			}
			return nil
		})
	})
	sc.Step(`^the cookie Domain is "([^"]*)"(?: \(configurable via env COOKIE_DOMAIN\))?$`, func(d string) error {
		return lastDo(func(r *response) error {
			c, err := r.cookie(queuehttp.CookieName)
			if err != nil {
				return err
			}
			if c.Domain != d {
				return fmt.Errorf("cookie Domain = %q, want %q", c.Domain, d)
			}
			return nil
		})
	})

	// ---- Redis state
	issuedIs := func(id string, n int64) error {
		v, err := W().rdb.HGet(context.Background(), ev(id).QueueHash(), queueredis.FieldIssued).Int64()
		if err != nil && !errors.Is(err, goredis.Nil) {
			return err
		}
		if v != n {
			return fmt.Errorf("queue:{%s} issued = %d, want %d", id, v, n)
		}
		return nil
	}
	sc.Step(`^queue:\{([^}]*)\} issued is (\d+)$`, issuedIs)
	sc.Step(`^queue:\{([^}]*)\} issued is still (\d+)$`, issuedIs)
	sc.Step(`^exactly one new ticket number is issued$`, func() error { return issuedIs("E1", 40001) })
	sc.Step(`^token:<queue_token> is a hash with exactly these fields:$`, func(t *godog.Table) error { return W().tokenHashIs(t) })
	sc.Step(`^token:<queue_token> expires in (\d+) seconds$`, func(s int) error {
		w := W()
		return w.ttlAbout(rediskeys.Token(w.resp.queueToken()), time.Duration(s)*time.Second)
	})
	sc.Step(`^queue:\{([^}]*)\}:by_ticket scores <queue_token> at (\d+)$`, func(id string, n int64) error {
		return W().scoreIs(id, W().resp.queueToken(), n)
	})
	sc.Step(`^queue:\{([^}]*)\}:by_ticket scores "([^"]*)" at (\d+)$`, func(id, qt string, n int64) error { return W().scoreIs(id, qt, n) })
	sc.Step(`^no acct:\{([^}]*)\}:\* key exists$`, func(id string) error {
		keys, err := W().scan(context.Background(), "acct:{"+id+"}:*")
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			return fmt.Errorf("acct keys exist: %v", keys)
		}
		return nil
	})
	sc.Step(`^the raw device id is stored nowhere in Redis$`, func() error { return W().deviceNowhere(defaultDevice) })
	sc.Step(`^token:<queue_token> has field account_id "([^"]*)" and no device_hash field$`, func(a string) error {
		w := W()
		m, err := w.rdb.HGetAll(context.Background(), rediskeys.Token(w.resp.queueToken())).Result()
		if err != nil {
			return err
		}
		if m["account_id"] != a {
			return fmt.Errorf("account_id = %q, want %q", m["account_id"], a)
		}
		if _, ok := m["device_hash"]; ok {
			return fmt.Errorf("token has device_hash: %v", m)
		}
		return nil
	})
	sc.Step(`^acct:\{([^}]*)\}:(\S+) holds <queue_token> and expires in (\d+) seconds$`, func(id, a string, s int) error {
		w := W()
		if err := w.acctIs(id, a, w.resp.queueToken()); err != nil {
			return err
		}
		return w.ttlAbout(ev(id).Acct(a), time.Duration(s)*time.Second)
	})
	sc.Step(`^acct:\{([^}]*)\}:(\S+) now points to the new queue_token$`, func(id, a string) error {
		w := W()
		if w.resp.queueToken() == "qt_GONE" {
			return errors.New("the stale token was returned")
		}
		return w.acctIs(id, a, w.resp.queueToken())
	})
	sc.Step(`^acct:\{([^}]*)\}:(\S+) points to that queue_token$`, func(id, a string) error {
		qt, err := W().commonToken()
		if err != nil {
			return err
		}
		return W().acctIs(id, a, qt)
	})
	sc.Step(`^all successful responses carry the same queue_token$`, func() error {
		_, err := W().commonToken()
		return err
	})
	sc.Step(`^queue:\{([^}]*)\} now_serving is still (\d+)$`, func(id string, n int64) error {
		snap, err := W().snapshot(id)
		if err != nil {
			return err
		}
		if snap.NowServing != n {
			return fmt.Errorf("now_serving = %d, want %d", snap.NowServing, n)
		}
		return nil
	})
	sc.Step(`^no PUBLISH was sent to Redis$`, func() error {
		names, _ := W().hook.snapshot()
		for _, n := range names {
			if n == "publish" || n == "spublish" {
				return errors.New("join sent PUBLISH")
			}
		}
		return nil
	})
	sc.Step(`^no Lua script other than the rate limiter was run$`, func() error {
		_, scripts := W().hook.snapshot()
		for _, s := range scripts {
			if s != ratelimit.ScriptSHA() {
				return fmt.Errorf("join ran script %s", s)
			}
		}
		return nil
	})
	sc.Step(`^queue:\{([^}]*)\}:by_ticket holds (\d+) members$`, func(id string, n int64) error {
		c, err := W().rdb.ZCard(context.Background(), ev(id).ByTicket()).Result()
		if err != nil {
			return err
		}
		if c != n {
			return fmt.Errorf("by_ticket has %d members, want %d", c, n)
		}
		return nil
	})
	sc.Step(`^there are (\d+) token:\* hashes for "([^"]*)"$`, func(n int, id string) error { return W().tokenHashes(n, id) })
	sc.Step(`^every by_ticket score equals the ticket_no in its token hash$`, func() error { return W().scoresMatchTokens("E1") })
	sc.Step(`^admission-ctl later skips number 1 as a gap$`, func() error {
		// admission-ctl only ever grants by_ticket members; number 1 has none.
		ms, err := W().rdb.ZRangeByScore(context.Background(), ev("E1").ByTicket(), &goredis.ZRangeBy{Min: "1", Max: "1"}).Result()
		if err != nil {
			return err
		}
		if len(ms) != 0 {
			return fmt.Errorf("number 1 has members %v", ms)
		}
		return W().scoreIs("E1", W().resp.queueToken(), 2)
	})

	// ---- SQL
	sc.Step(`^no SQL statement was sent(?: to Postgres)?$`, func() error {
		if s := tracer.statements(); len(s) > 0 {
			return fmt.Errorf("join sent SQL: %v", s)
		}
		return W().needDB()
	})

	// ---- timing

	// ---- tickets
	sc.Step(`^the ticket numbers are (\d+) and (\d+)$`, func(a, b int64) error {
		got, err := W().tickets()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, []int64{a, b}) {
			return fmt.Errorf("tickets = %v, want [%d %d]", got, a, b)
		}
		return nil
	})
	sc.Step(`^the two queue_tokens differ$`, func() error {
		w := W()
		if len(w.resps) < 2 || w.resps[0].queueToken() == w.resps[1].queueToken() {
			return errors.New("queue_tokens are equal")
		}
		return nil
	})
	sc.Step(`^the ticket numbers are exactly 1 to (\d+), each once$`, func(n int64) error {
		got, err := W().tickets()
		if err != nil {
			return err
		}
		if int64(len(got)) != n {
			return fmt.Errorf("%d tickets, want %d", len(got), n)
		}
		for i, t := range got {
			if t != int64(i)+1 {
				return fmt.Errorf("ticket #%d is %d: not 1..%d each once", i, t, n)
			}
		}
		return nil
	})
	sc.Step(`^"([^"]*)" < "([^"]*)" < "([^"]*)" by ticket number$`, func(a, b, c string) error {
		w := W()
		var ns []int64
		for _, g := range []string{a, b, c} {
			r, _ := w.captured["guest:"+g].(*response)
			if r == nil {
				return fmt.Errorf("no response for %s", g)
			}
			n, err := w.ticketOf(r)
			if err != nil {
				return err
			}
			ns = append(ns, n)
		}
		if ns[0] >= ns[1] || ns[1] >= ns[2] {
			return fmt.Errorf("tickets %v are not increasing", ns)
		}
		return nil
	})
	sc.Step(`^at least (\d+) joins succeed$`, func(n int) error {
		if ok := W().countStatus(200); ok < n {
			return fmt.Errorf("%d joins succeeded, want at least %d", ok, n)
		}
		return nil
	})
	sc.Step(`^at most (\d+) joins succeed$`, func(n int) error {
		if ok := W().countStatus(200); ok > n {
			return fmt.Errorf("%d joins succeeded, want at most %d", ok, n)
		}
		return nil
	})
	sc.Step(`^the rest get 503 "QUEUE_FULL"$`, func() error {
		for _, r := range W().resps {
			if r.status != 200 {
				if err := expectCode(r, 503, domain.CodeQueueFull); err != nil {
					return err
				}
			}
		}
		return nil
	})
	sc.Step(`^the second guest gets ticket number (\d+)$`, func(n int64) error {
		return lastDo(func(r *response) error { return dataInt(r, "ticket_no", n) })
	})

	// ---- rate limits
	sc.Step(`^all (\d+) are answered without 429$`, func(n int) error {
		w := W()
		if len(w.resps) < n {
			return fmt.Errorf("only %d responses", len(w.resps))
		}
		for i, r := range w.resps[:n] {
			if r.status == 429 {
				return fmt.Errorf("attempt %d got 429", i+1)
			}
		}
		return nil
	})
	sc.Step(`^IP "([^"]*)" may join once more$`, func(ip string) error {
		w := W()
		r := w.do(joinReq{eventID: "E1", ip: ip})
		w.record(r)
		return expectStatus(r, 200)
	})
	sc.Step(`^the rate limit is charged to "([^"]*)"$`, func(ip string) error {
		keys := W().limiter.charged()
		want := rediskeys.JoinRateLimit(ip)
		for _, k := range keys {
			if k != want && strings.HasPrefix(k, "rl:join:") {
				return fmt.Errorf("rate limit charged to %s", k)
			}
		}
		if len(keys) == 0 || keys[0] != want {
			return fmt.Errorf("rate limit keys %v, want %s", keys, want)
		}
		return nil
	})
	sc.Step(`^the CAPTCHA provider was not called$`, func() error {
		if n := W().fcaptcha.calls.Load(); n != 0 {
			return fmt.Errorf("CAPTCHA called %d times", n)
		}
		return nil
	})

	// ---- metrics
	sc.Step(`^the metric "([^"]*)" is incremented$`, func(name string) error {
		if W().metrics.count(name) == 0 {
			return fmt.Errorf("metric %s not incremented", name)
		}
		return nil
	})
	sc.Step(`^the metric "([^"]*)" is recorded$`, func(name string) error {
		if W().metrics.recorded(name) == 0 {
			return fmt.Errorf("metric %s not recorded", name)
		}
		return nil
	})

	// ---- gate order
	sc.Step(`^no gate after "([^"]*)" was called$`, func(g string) error { return W().noGateAfter(g) })

	// ---- misc
	sc.Step(`^both return the same status, body shape and Set-Cookie header$`, func() error { return W().sameShape() })
	sc.Step(`^each documented join response is produced \(200, 200 rejoined, 400, 403, 409, 429, 503\)$`, func() error {
		return W().produceDocumented()
	})
	sc.Step(`^each body validates against its schema in documentations/api/rusuh-booking-apidog\.json$`, func() error {
		return W().validateApidog()
	})
	sc.Step(`^no log line contains "([^"]*)", the queue_token, "([^"]*)" or the access token$`, func(c, dev string) error {
		w := W()
		logs := w.logs.String()
		secrets := []string{c, dev, w.resp.queueToken()}
		if w.bearer != "" {
			secrets = append(secrets, w.bearer)
		}
		for _, s := range secrets {
			if s != "" && strings.Contains(logs, s) {
				return fmt.Errorf("a log line contains a secret (%d chars)", len(s))
			}
		}
		return nil
	})
}

var qtPattern = regexp.MustCompile(`^qt_[0-9A-HJKMNP-TV-Z]{26}$`)

func dataInt(r *response, field string, want int64) error {
	d, err := r.data()
	if err != nil {
		return err
	}
	if n, ok := num(d[field]); !ok || n != want {
		return fmt.Errorf("data.%s = %v, want %d; body %s", field, d[field], want, truncate(string(r.body)))
	}
	return nil
}

// dataHas checks "k v, k v and k v" where v is a number, true/false or "string".
func dataHas(r *response, pairs string) error {
	d, err := r.data()
	if err != nil {
		return err
	}
	for _, p := range strings.Split(strings.ReplaceAll(pairs, " and ", ", "), ", ") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), " ")
		if !ok {
			return fmt.Errorf("cannot read %q", p)
		}
		var want any
		switch {
		case v == "true" || v == "false":
			want = v == "true"
		case strings.HasPrefix(v, `"`):
			want, _ = strconv.Unquote(v)
		default:
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("value %q: %w", v, err)
			}
			want = n
		}
		if !reflect.DeepEqual(d[k], want) {
			return fmt.Errorf("data.%s = %#v, want %#v; body %s", k, d[k], want, truncate(string(r.body)))
		}
	}
	return nil
}

func validErrorEnvelope(r *response) error {
	if r.status < 400 {
		return fmt.Errorf("status %d is not an error", r.status)
	}
	m, err := r.meta()
	if err != nil {
		return err
	}
	if n, _ := num(m["code"]); n != int64(r.status) {
		return fmt.Errorf("meta.code = %v, status %d", m["code"], r.status)
	}
	for _, f := range []string{"message", "error_code", "server_time"} {
		if s, _ := m[f].(string); s == "" {
			return fmt.Errorf("meta.%s missing: %s", f, truncate(string(r.body)))
		}
	}
	_, err = r.data()
	return err
}

func (w *world) allStatus(s int) error {
	if len(w.resps) == 0 {
		return errors.New("no responses")
	}
	for i, r := range w.resps {
		if r.status != s {
			return fmt.Errorf("response %d: status %d, want %d; body %s", i, r.status, s, truncate(string(r.body)))
		}
	}
	return nil
}

func (w *world) countStatus(s int) int {
	n := 0
	for _, r := range w.resps {
		if r.status == s {
			n++
		}
	}
	return n
}

// bodyMatches compares JSON exactly, with two placeholders.
func (w *world) bodyMatches(r *response, want string) error {
	var exp, got any
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		return fmt.Errorf("expected JSON: %w", err)
	}
	if err := json.Unmarshal(r.body, &got); err != nil {
		return err
	}
	sse := queuehttp.NewHandler(nil, w.opt).SSEURL("E1")
	var match func(path string, e, g any) error
	match = func(path string, e, g any) error {
		switch ev := e.(type) {
		case map[string]any:
			gm, ok := g.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: got %T, want object", path, g)
			}
			if len(gm) != len(ev) {
				return fmt.Errorf("%s: keys %v, want %v", path, keys(gm), keys(ev))
			}
			for k, v := range ev {
				if err := match(path+"."+k, v, gm[k]); err != nil {
					return err
				}
			}
			return nil
		case string:
			switch ev {
			case "<any qt_ token>":
				if s, _ := g.(string); !qtPattern.MatchString(s) {
					return fmt.Errorf("%s = %v, want a qt_ token", path, g)
				}
				return nil
			case "<configured stream url for E1>":
				if g != sse {
					return fmt.Errorf("%s = %v, want %s", path, g, sse)
				}
				return nil
			}
		}
		if !reflect.DeepEqual(e, g) {
			return fmt.Errorf("%s = %#v, want %#v", path, g, e)
		}
		return nil
	}
	if err := match("body", exp, got); err != nil {
		return fmt.Errorf("%w\nbody: %s", err, truncate(string(r.body)))
	}
	return nil
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (w *world) tokenHashIs(t *godog.Table) error {
	m, err := w.rdb.HGetAll(context.Background(), rediskeys.Token(w.resp.queueToken())).Result()
	if err != nil {
		return err
	}
	want := map[string]string{}
	for _, row := range t.Rows[1:] {
		k, v := row.Cells[0].Value, row.Cells[1].Value
		if strings.HasPrefix(v, "HMAC-SHA256(DEVICE_PEPPER, ") {
			dev := strings.TrimSuffix(strings.TrimPrefix(v, `HMAC-SHA256(DEVICE_PEPPER, "`), `") hex`)
			h := hmac.New(sha256.New, w.cfg.DevicePepper)
			h.Write([]byte(dev))
			v = hex.EncodeToString(h.Sum(nil))
		}
		want[k] = v
	}
	if !reflect.DeepEqual(m, want) {
		return fmt.Errorf("token hash = %v, want %v", m, want)
	}
	return nil
}

func (w *world) ttlAbout(key string, want time.Duration) error {
	ttl, err := w.rdb.TTL(context.Background(), key).Result()
	if err != nil {
		return err
	}
	if ttl > want || ttl < want-10*time.Second {
		return fmt.Errorf("TTL %s = %v, want ~%v", key, ttl, want)
	}
	return nil
}

func (w *world) scoreIs(id, qt string, n int64) error {
	s, err := w.rdb.ZScore(context.Background(), ev(id).ByTicket(), qt).Result()
	if err != nil {
		return fmt.Errorf("%s not in by_ticket: %w", qt, err)
	}
	if int64(s) != n {
		return fmt.Errorf("by_ticket score of %s = %v, want %d", qt, s, n)
	}
	return nil
}

func (w *world) acctIs(id, a, qt string) error {
	v, err := w.rdb.Get(context.Background(), ev(id).Acct(a)).Result()
	if err != nil {
		return fmt.Errorf("acct:{%s}:%s: %w", id, a, err)
	}
	if v != qt {
		return fmt.Errorf("acct:{%s}:%s = %q, want %q", id, a, v, qt)
	}
	return nil
}

func (w *world) commonToken() (string, error) {
	qt := ""
	for _, r := range w.resps {
		if r.status != 200 {
			continue
		}
		if qt == "" {
			qt = r.queueToken()
		} else if r.queueToken() != qt {
			return "", fmt.Errorf("responses carry %q and %q", qt, r.queueToken())
		}
	}
	if qt == "" {
		return "", errors.New("no successful response")
	}
	return qt, nil
}

// deviceNowhere scans every key and value the test events can own.
func (w *world) deviceNowhere(dev string) error {
	ctx := context.Background()
	keys, err := w.scan(ctx, "*")
	if err != nil {
		return err
	}
	for _, k := range keys {
		if strings.Contains(k, dev) {
			return fmt.Errorf("key %s contains the device id", k)
		}
		var vals []string
		switch t, _ := w.rdb.Type(ctx, k).Result(); t {
		case "string":
			v, _ := w.rdb.Get(ctx, k).Result()
			vals = []string{v}
		case "hash":
			m, _ := w.rdb.HGetAll(ctx, k).Result()
			for f, v := range m {
				vals = append(vals, f, v)
			}
		case "zset":
			vals, _ = w.rdb.ZRange(ctx, k, 0, -1).Result()
		}
		for _, v := range vals {
			if strings.Contains(v, dev) {
				return fmt.Errorf("key %s stores the raw device id", k)
			}
		}
	}
	return nil
}

func (w *world) tokenHashes(n int, id string) error {
	ctx := context.Background()
	count := 0
	for _, r := range w.resps {
		qt := r.queueToken()
		if qt == "" {
			continue
		}
		e, err := w.rdb.HGet(ctx, rediskeys.Token(qt), "event_id").Result()
		if err == nil && e == id {
			count++
		}
	}
	if count != n {
		return fmt.Errorf("%d token hashes for %s, want %d", count, id, n)
	}
	return nil
}

func (w *world) scoresMatchTokens(id string) error {
	ctx := context.Background()
	zs, err := w.rdb.ZRangeWithScores(ctx, ev(id).ByTicket(), 0, -1).Result()
	if err != nil {
		return err
	}
	for _, z := range zs {
		qt := z.Member.(string)
		v, err := w.rdb.HGet(ctx, rediskeys.Token(qt), "ticket_no").Int64()
		if err != nil {
			return fmt.Errorf("token %s: %w", qt, err)
		}
		if v != int64(z.Score) {
			return fmt.Errorf("token %s ticket_no %d, score %v", qt, v, z.Score)
		}
	}
	return nil
}

func (w *world) tickets() ([]int64, error) {
	var out []int64
	for _, r := range w.resps {
		if r.status != 200 {
			continue
		}
		n, err := w.ticketOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (w *world) noGateAfter(g string) error {
	order := []string{"request shape", "identity", "rate limit", "CAPTCHA required", "CAPTCHA", "sale state", "fully booked", "capacity"}
	probe := map[string]func() int64{
		"rate limit":   w.limiter.calls.Load,
		"CAPTCHA":      w.fcaptcha.calls.Load,
		"sale state":   w.store.snapshots.Load,
		"fully booked": w.store.issues.Load,
		"capacity":     w.store.issues.Load,
	}
	after := false
	for _, name := range order {
		if after {
			if p, ok := probe[name]; ok && p() != 0 {
				return fmt.Errorf("gate %q ran after %q failed", name, g)
			}
		}
		if name == g {
			after = true
		}
	}
	if w.store.issues.Load() != 0 {
		return errors.New("a ticket number was issued")
	}
	return nil
}

func shape(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			out[k] = shape(x)
		}
		return out
	default:
		return fmt.Sprintf("%T", v)
	}
}

func (w *world) sameShape() error {
	if len(w.resps) != 2 {
		return fmt.Errorf("%d responses, want 2", len(w.resps))
	}
	a, b := w.resps[0], w.resps[1]
	if a.status != b.status {
		return fmt.Errorf("status %d vs %d", a.status, b.status)
	}
	var ja, jb any
	if err := json.Unmarshal(a.body, &ja); err != nil {
		return err
	}
	if err := json.Unmarshal(b.body, &jb); err != nil {
		return err
	}
	if !reflect.DeepEqual(shape(ja), shape(jb)) {
		return fmt.Errorf("body shapes differ:\n%s\n%s", a.body, b.body)
	}
	ca, err := a.cookie(queuehttp.CookieName)
	if err != nil {
		return fmt.Errorf("lambda: %w", err)
	}
	cb, err := b.cookie(queuehttp.CookieName)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	ca.Value, cb.Value = "", ""
	if ca.String() != cb.String() {
		return fmt.Errorf("Set-Cookie differs: %q vs %q", ca.String(), cb.String())
	}
	return nil
}

// produceDocumented produces one response per documented case.
func (w *world) produceDocumented() error {
	w.limiter = &countingLimiter{}
	if err := w.seedEvent("E1", "OPEN", ptr(int64(10)), ptr(int64(20)), "Konser A — Jakarta"); err != nil {
		return err
	}
	out := map[string]*response{}
	out["200"] = w.do(joinReq{eventID: "E1", deviceID: "dev-contract-0000001"})
	out["200 rejoined"] = w.do(joinReq{eventID: "E1", deviceID: "dev-contract-0000001", queueToken: out["200"].queueToken()})
	if err := w.setCounters("E1", 21, 21); err != nil {
		return err
	}
	out["400"] = w.do(joinReq{eventID: "E1", captcha: ptr("")})
	out["403"] = w.do(joinReq{eventID: "E1", captcha: ptr("cap-bad")})
	w.limiter.refuse = true
	out["429"] = w.do(joinReq{eventID: "E1"})
	w.limiter.refuse = false
	if err := w.setCounters("E1", 0, 10000000); err != nil {
		return err
	}
	out["503"] = w.do(joinReq{eventID: "E1"})
	if err := w.rdb.HSet(context.Background(), ev("E1").QueueHash(), "sale_starts_at", "2026-11-10T02:00:00Z").Err(); err != nil {
		return err
	}
	if err := w.setState("E1", "SCHEDULED"); err != nil {
		return err
	}
	out["409"] = w.do(joinReq{eventID: "E1"})
	for name, r := range out {
		want := 200
		if code, err := strconv.Atoi(strings.Fields(name)[0]); err == nil {
			want = code
		}
		if r.status != want {
			return fmt.Errorf("%s: status %d; body %s", name, r.status, truncate(string(r.body)))
		}
	}
	w.captured["documented"] = out
	return nil
}

func (w *world) validateApidog() error {
	out, _ := w.captured["documented"].(map[string]*response)
	path := filepath.Join("..", "..", "..", "..", "documentations", "api", "rusuh-booking-apidog.json")
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Apidog file (documentations repo next to this one): %w", err)
	}
	defer func() { _ = f.Close() }()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		return err
	}
	api, err := findJoinAPI(doc)
	if err != nil {
		return err
	}
	schemas := map[int]any{}
	for _, r := range api["responses"].([]any) {
		rm := r.(map[string]any)
		code, _ := strconv.Atoi(fmt.Sprint(rm["code"]))
		schemas[code] = rm["jsonSchema"]
	}
	var problems []string
	for name, r := range out {
		code := r.status
		sch, ok := schemas[code]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: Apidog has no %d schema", name, code))
			continue
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("join.json", sch); err != nil {
			return err
		}
		compiled, err := c.Compile("join.json")
		if err != nil {
			return fmt.Errorf("schema %d: %w", code, err)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(r.body)))
		if err != nil {
			return err
		}
		if err := compiled.Validate(inst); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func findJoinAPI(doc any) (map[string]any, error) {
	var found map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if api, ok := t["api"].(map[string]any); ok && api["path"] == "/events/{event_id}/queue/join" {
				found = api
				return
			}
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(doc)
	if found == nil {
		return nil, errors.New("join API not found in Apidog")
	}
	return found, nil
}
