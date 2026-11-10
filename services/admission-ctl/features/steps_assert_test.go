package features

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

func (w *tickWorld) rowOf(name string) (rowView, bool, error) {
	c := w.customers[name]
	if c == nil {
		return rowView{}, false, fmt.Errorf("no customer %q", name)
	}
	rows, err := w.wantRows("E1")
	if err != nil {
		return rowView{}, false, err
	}
	for _, r := range rows {
		if r.QueueToken == c.token {
			return r, true, nil
		}
	}
	return rowView{}, false, nil
}

func (w *tickWorld) ttlSeconds(key string) (int, error) {
	d, err := w.rdb.TTL(w.ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return int(d.Round(time.Second) / time.Second), nil
}

func (w *tickWorld) grantRows() ([]rowView, error) {
	rows, err := w.wantRows("E1")
	if err != nil {
		return nil, err
	}
	var out []rowView
	for _, r := range rows {
		for _, c := range w.customers {
			if c.token == r.QueueToken {
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QueueToken < out[j].QueueToken })
	return out, nil
}

func (w *tickWorld) nowServing() (int64, error) {
	v, err := w.field("E1", rediskeys.FieldNowServing)
	if err != nil || v == "" {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

func (w *tickWorld) inByTicket(name string) (bool, error) {
	_, err := w.rdb.ZScore(w.ctx, w.key("E1").ByTicket(), w.customers[name].token).Result()
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}
	return err == nil, err
}

// subsequence reports whether want appears in got in order (other calls may sit between).
func subsequence(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

var callOps = []struct{ prefix, op string }{
	{"ZRANGEBYSCORE", "Pending"}, {"HGET queue", "Snapshot"}, {"HMGET queue", "Snapshot"}, {"ZCOUNT", "PendingCount"},
	{"SELECT", "Count"}, {"EVALSHA", "Advance"}, {"HMGET token", "Token"}, {"INSERT", "Insert"},
	{"HSET grant", "PutGrant"}, {"HSET token", "AttachGrant"}, {"ZREM", "Dequeue"}, {"PUBLISH sse-broadcast", "PublishBroadcast"},
}

func opOf(text string) (string, error) {
	for _, c := range callOps {
		if strings.HasPrefix(text, c.prefix) {
			return c.op, nil
		}
	}
	return "", fmt.Errorf("unknown call %q in the call log table", text)
}

func registerTickThen(sc *godog.ScenarioContext, w *tickWorld) {
	sc.Step(`^bookings has exactly (\d+) rows for "E1" with status "([A-Z_]+)"$`, func(n int, status string) error {
		rows, err := w.grantRows()
		if err != nil {
			return err
		}
		got := 0
		for _, r := range rows {
			if r.Status == status {
				got++
			}
		}
		if got != n {
			return fmt.Errorf("%d %s rows, want %d (all rows: %+v)", got, status, n, rows)
		}
		return nil
	})
	sc.Step(`^each row has the customer's account_id and queue_token$`, func() error {
		rows, err := w.grantRows()
		if err != nil {
			return err
		}
		for _, r := range rows {
			ok := false
			for _, c := range w.customers {
				if c.token == r.QueueToken && c.account == r.Account {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("row %+v does not match any customer", r)
			}
		}
		return nil
	})
	sc.Step(`^each row has a unique grant_token$`, func() error {
		rows, err := w.grantRows()
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, r := range rows {
			if r.GrantToken == "" || seen[r.GrantToken] {
				return fmt.Errorf("grant_token %q is empty or repeated", r.GrantToken)
			}
			seen[r.GrantToken] = true
		}
		return nil
	})
	sc.Step(`^each row has booking_expires_at "([^"]*)"$`, func(ts string) error {
		want, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return err
		}
		rows, err := w.grantRows()
		if err != nil {
			return err
		}
		for _, r := range rows {
			if !r.ExpiresAt.Equal(want) {
				return fmt.Errorf("booking_expires_at = %s, want %s", r.ExpiresAt, want)
			}
		}
		return nil
	})
	sc.Step(`^grant:<grant_token> exists for each row with a TTL of (\d+) seconds$`, func(secs int) error {
		rows, err := w.grantRows()
		if err != nil {
			return err
		}
		for _, r := range rows {
			ttl, err := w.ttlSeconds(rediskeys.Grant(r.GrantToken))
			if err != nil {
				return err
			}
			if ttl < secs-2 || ttl > secs {
				return fmt.Errorf("TTL of grant:%s = %d s, want %d", r.GrantToken, ttl, secs)
			}
		}
		return nil
	})
	sc.Step(`^token:<queue_token> of each customer holds that row's grant_token, booking_id and booking_expires_at$`, func() error {
		rows, err := w.grantRows()
		if err != nil {
			return err
		}
		for _, r := range rows {
			h, err := w.rdb.HGetAll(w.ctx, rediskeys.Token(r.QueueToken)).Result()
			if err != nil {
				return err
			}
			if h[rediskeys.FieldGrantToken] != r.GrantToken || h[rediskeys.FieldBookingID] != r.ID ||
				h[rediskeys.FieldBookingExpiresAt] != r.ExpiresAt.UTC().Format(time.RFC3339) {
				return fmt.Errorf("token hash %v does not hold row %+v", h, r)
			}
		}
		return nil
	})
	sc.Step(`^by_ticket no longer holds (.+)$`, func(names string) error {
		for _, n := range parseNames(names) {
			if in, err := w.inByTicket(n); err != nil || in {
				return fmt.Errorf("%s is still in by_ticket (err %v)", n, err)
			}
		}
		return nil
	})
	sc.Step(`^now_serving is (\d+)$`, func(want int64) error {
		got, err := w.nowServing()
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("now_serving = %d, want %d", got, want)
		}
		return nil
	})
	sc.Step(`^now_serving is unchanged$`, func() error {
		got, _ := w.field("E1", rediskeys.FieldNowServing)
		if want, _ := w.scratch["ns0"].(string); got != want {
			return fmt.Errorf("now_serving = %q, was %q", got, want)
		}
		return nil
	})

	// ---- the guest / logged-in grant fields
	sc.Step(`^grant:<grant_token> holds exactly the fields (.+)$`, func(fields string) error {
		r, ok, err := w.rowOf("c1")
		if err != nil || !ok {
			return fmt.Errorf("no row for c1 (err %v)", err)
		}
		h, err := w.rdb.HGetAll(w.ctx, rediskeys.Grant(r.GrantToken)).Result()
		if err != nil {
			return err
		}
		var got []string
		for k := range h {
			got = append(got, k)
		}
		var want []string
		for _, f := range strings.Split(fields, ",") {
			want = append(want, strings.TrimSpace(f))
		}
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			return fmt.Errorf("grant fields %v, want %v", got, want)
		}
		return nil
	})
	sc.Step(`^event_id is "E1", queue_token is c1's queue_token and booking_id is the GRANTED row's id$`, func() error {
		r, _, _ := w.rowOf("c1")
		h, _ := w.rdb.HGetAll(w.ctx, rediskeys.Grant(r.GrantToken)).Result()
		if h["event_id"] != w.evID("E1") || h["queue_token"] != w.customers["c1"].token || h["booking_id"] != r.ID {
			return fmt.Errorf("grant hash %v does not match row %+v", h, r)
		}
		return nil
	})
	sc.Step(`^account_id and device_hash are copied from token:<queue_token>, never supplied by the client$`, func() error {
		r, _, _ := w.rowOf("c1")
		h, _ := w.rdb.HGetAll(w.ctx, rediskeys.Grant(r.GrantToken)).Result()
		c := w.customers["c1"]
		if h["account_id"] != c.account || h["device_hash"] != c.device {
			return fmt.Errorf("grant %v, customer %+v", h, c)
		}
		return nil
	})
	sc.Step(`^grant:<grant_token> still exists$`, func() error {
		r, ok, err := w.rowOf("c1")
		if err != nil || !ok {
			return fmt.Errorf("no row for c1 (err %v)", err)
		}
		ttl, err := w.ttlSeconds(rediskeys.Grant(r.GrantToken))
		if err != nil {
			return err
		}
		if ttl < 930 { // the longest trip: 300 + 600 + 30
			return fmt.Errorf("grant TTL is %d s, shorter than the longest trip (930 s)", ttl)
		}
		return nil
	})
	sc.Step(`^the TTL of grant:<grant_token> was set from GRANT_TOKEN_TTL_SECONDS, not from the booking deadline$`, func() error {
		r, _, _ := w.rowOf("c1")
		ttl, _ := w.ttlSeconds(rediskeys.Grant(r.GrantToken))
		if ttl < 1075 || ttl > 1080 {
			return fmt.Errorf("TTL = %d s, want 1080", ttl)
		}
		return nil
	})

	// ---- the call log
	sc.Step(`^the call log is, in order:$`, func(t *godog.Table) error {
		var want []string
		for _, row := range t.Rows[1:] {
			op, err := opOf(row.Cells[1].Value)
			if err != nil {
				return err
			}
			want = append(want, op)
		}
		var got []string
		for _, c := range w.log.snapshot() {
			got = append(got, c.op)
		}
		if !subsequence(got, want) {
			return fmt.Errorf("call log %v does not contain %v in order", got, want)
		}
		return nil
	})

	// ---- scheduled / token expiry / outages
	sc.Step(`^COUNT and ADVANCE run for "E2" with open 0 and browsing 0$`, func() error {
		var counted, advanced bool
		for _, c := range w.log.snapshot() {
			if c.event != w.evID("E2") {
				continue
			}
			counted = counted || c.op == "Count"
			if c.op == "Advance" {
				advanced = c.args.Open == 0 && c.args.Browsing == 0
			}
		}
		if !counted || !advanced {
			return fmt.Errorf("E2 counted=%v advanced(open 0, browsing 0)=%v", counted, advanced)
		}
		return nil
	})
	sc.Step(`^no INSERT is attempted for "E2"$`, func() error {
		for _, c := range w.log.snapshot() {
			if c.op == "Insert" && c.event == w.evID("E2") {
				return errors.New("an INSERT was attempted for E2")
			}
		}
		return nil
	})
	sc.Step(`^"c1" has a "GRANTED" row$`, func() error {
		r, ok, err := w.rowOf("c1")
		if err != nil || !ok || r.Status != "GRANTED" {
			return fmt.Errorf("c1 row ok=%v %+v err=%v", ok, r, err)
		}
		return nil
	})
	sc.Step(`^no token:<c1> hash exists$`, func() error {
		n, err := w.rdb.Exists(w.ctx, rediskeys.Token(w.customers["c1"].token)).Result()
		if err != nil || n != 0 {
			return fmt.Errorf("token hash exists (n=%d err=%v)", n, err)
		}
		return nil
	})
	sc.Step(`^nobody is admitted$`, func() error { return w.admittedIs(0) })
	sc.Step(`^exactly (\d+) customers? (?:is|are) admitted$`, func(n int) error { return w.admittedIs(n) })
	sc.Step(`^(\d+) customers are admitted$`, func(n int) error { return w.admittedIs(n) })
	sc.Step(`^ADVANCE runs again only at the next 500 ms tick$`, func() error {
		if got := w.log.count("Advance"); got != 0 {
			return fmt.Errorf("ADVANCE ran %d times between ticks", got)
		}
		w.clock.add(400 * time.Millisecond)
		w.runTickLogged()
		if w.log.count("Advance") != 1 {
			return errors.New("ADVANCE did not run at the next tick")
		}
		return nil
	})
	sc.Step(`^ADVANCE is called with open (\d+) and browsing (\d+)$`, func(open, browsing int64) error {
		a, ok := w.lastAdvance()
		if !ok || a.Open != open || a.Browsing != browsing {
			return fmt.Errorf("ADVANCE args %+v (called=%v), want open %d browsing %d", a, ok, open, browsing)
		}
		return nil
	})
	sc.Step(`^ADVANCE is called with the now_serving read in SNAPSHOT$`, func() error {
		a, _ := w.lastAdvance()
		ns, err := w.nowServing()
		if err != nil {
			return err
		}
		if a.NowServing != ns {
			return fmt.Errorf("ADVANCE fenced on %d, SNAPSHOT read %d", a.NowServing, ns)
		}
		return nil
	})
	sc.Step(`^COUNT and ADVANCE both run$`, func() error {
		if w.log.count("Count") == 0 || w.log.count("Advance") == 0 {
			return errors.New("COUNT or ADVANCE did not run")
		}
		return nil
	})
	sc.Step(`^ADVANCE is not called$`, func() error {
		if n := w.log.count("Advance"); n != 0 {
			return fmt.Errorf("ADVANCE was called %d times", n)
		}
		return nil
	})
	sc.Step(`^the metric "([A-Za-z]+)" is incremented$`, func(name string) error {
		if w.metricCount(name) < 1 {
			return fmt.Errorf("metric %s was not incremented", name)
		}
		return nil
	})
	sc.Step(`^the metric "([A-Za-z]+)" is incremented once$`, func(name string) error {
		if n := w.metricCount(name); n != 1 {
			return fmt.Errorf("metric %s = %d, want 1", name, n)
		}
		return nil
	})
	sc.Step(`^the heartbeat broadcast still goes out on schedule$`, func() error { return w.broadcastCount(1) })
	sc.Step(`^"E1" admits as usual$`, func() error {
		for _, c := range w.log.snapshot() {
			if c.op == "Advance" && c.event == w.evID("E1") {
				return nil
			}
		}
		return errors.New("E1 did not reach ADVANCE")
	})
	sc.Step(`^"E2" skips ADVANCE$`, func() error {
		for _, c := range w.log.snapshot() {
			if c.op == "Advance" && c.event == w.evID("E2") {
				return errors.New("E2 reached ADVANCE")
			}
		}
		return nil
	})
	sc.Step(`^the second tick starts only after the first one finishes$`, func() error {
		if m := w.spyQ.maxFlight.Load(); m != 1 {
			return fmt.Errorf("%d Redis calls ran at once", m)
		}
		if w.log.count("Snapshot") < 3 {
			return errors.New("fewer than two ticks ran")
		}
		return nil
	})
	sc.Step(`^no INSERT is attempted(?: this tick)?$`, func() error {
		if n := w.log.count("Insert"); n != 0 {
			return fmt.Errorf("%d INSERTs were attempted", n)
		}
		return nil
	})
	sc.Step(`^the tick does not fail$`, func() error {
		if w.lastErr != nil || w.crashErr != nil {
			return fmt.Errorf("tick failed: %v %v", w.lastErr, w.crashErr)
		}
		return nil
	})
}

func (w *tickWorld) admittedIs(n int) error {
	if got := w.log.count("Insert"); got != n {
		return fmt.Errorf("%d customers admitted (INSERTs), want %d", got, n)
	}
	return nil
}

func (w *tickWorld) lastAdvance() (a struct {
	Open, Browsing, NowServing int64
}, ok bool) {
	for _, c := range w.log.snapshot() {
		if c.op == "Advance" {
			a.Open, a.Browsing, a.NowServing, ok = c.args.Open, c.args.Browsing, c.args.NowServing, true
		}
	}
	return a, ok
}

func (w *tickWorld) broadcastCount(atLeast int) error {
	w.spyQ.mu.Lock()
	defer w.spyQ.mu.Unlock()
	if len(w.spyQ.broadcasts) < atLeast {
		return fmt.Errorf("%d broadcasts published, want at least %d", len(w.spyQ.broadcasts), atLeast)
	}
	return nil
}
