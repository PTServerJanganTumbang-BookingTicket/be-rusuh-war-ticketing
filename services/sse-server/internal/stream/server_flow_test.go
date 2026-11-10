package stream

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

var errBoom = errors.New("redis down")

type fakeStore struct {
	mu         sync.Mutex
	state      string
	found      bool
	stateErr   error
	nowServing int64
	rate       int64
	numbersErr error
	tokens     map[string]string
	tokenErr   error
}

func (f *fakeStore) State(context.Context, rediskeys.Event) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, f.found, f.stateErr
}
func (f *fakeStore) setState(s string) { f.mu.Lock(); f.state = s; f.mu.Unlock() }
func (f *fakeStore) Numbers(context.Context, rediskeys.Event) (int64, int64, error) {
	return f.nowServing, f.rate, f.numbersErr
}
func (f *fakeStore) TokenEvent(_ context.Context, tok string) (string, bool, error) {
	ev, ok := f.tokens[tok]
	return ev, ok, f.tokenErr
}

type fakeLimiter struct {
	res ratelimit.Result
	err error
	key string
}

func (l *fakeLimiter) Allow(_ context.Context, key string, _ ratelimit.Limit) (ratelimit.Result, error) {
	l.key = key
	return l.res, l.err
}

type countMetrics struct {
	mu sync.Mutex
	n  map[string]int
}

func (m *countMetrics) Incr(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.n == nil {
		m.n = map[string]int{}
	}
	m.n[name]++
}
func (m *countMetrics) Gauge(string, string, float64) {}
func (m *countMetrics) count(name string) int         { m.mu.Lock(); defer m.mu.Unlock(); return m.n[name] }

type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixedClock) Now() time.Time                 { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fixedClock) NewTicker(time.Duration) Ticker { return RealClock().NewTicker(time.Hour) }
func (c *fixedClock) advance(d time.Duration)        { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func okStore() *fakeStore {
	return &fakeStore{state: StateOpen, found: true, nowServing: 41, rate: 318, tokens: map[string]string{"tok": "E1"}}
}

func flowServer(st *fakeStore, lim *fakeLimiter) (*Server, *countMetrics) {
	cfg := DefaultConfig()
	m := &countMetrics{}
	return NewServer(cfg, Deps{Store: st, Limiter: lim, Metrics: m, Clock: RealClock(), Rand: func(int64) int64 { return 0 }}), m
}

func get(s *Server, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "10.0.0.9:5555"
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func withToken(r *http.Request) { r.Header.Set("X-Queue-Token", "tok") }

func TestConnectGatesAnswerWithTheContractStatus(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeStore, *fakeLimiter, *Server)
		path   string
		req    func(*http.Request)
		status int
		code   string
	}{
		{"an event id with braces is not found", nil, "/events/%7Bx%7D/queue/stream", withToken, 404, "NOT_FOUND"},
		{"a rate-limited ip gets 429", func(_ *fakeStore, l *fakeLimiter, _ *Server) { l.res = ratelimit.Result{RetryAfter: 2 * time.Second} }, "/events/E1/queue/stream", withToken, 429, "TOO_MANY_REQUESTS"},
		{"a state read error is 503", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.stateErr = errBoom }, "/events/E1/queue/stream", withToken, 503, "SERVICE_UNAVAILABLE"},
		{"an unknown event is 404", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.found = false }, "/events/E1/queue/stream", withToken, 404, "NOT_FOUND"},
		{"a scheduled sale is 409", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.state = StateScheduled }, "/events/E1/queue/stream", withToken, 409, "SALE_NOT_OPEN"},
		{"a closed sale is 410", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.state = StateClosed }, "/events/E1/queue/stream", withToken, 410, "SALE_CLOSED"},
		{"an unrecognised state is 503 not ready", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.state = "WARMING" }, "/events/E1/queue/stream", withToken, 503, "QUEUE_NOT_READY"},
		{"no token is 401", nil, "/events/E1/queue/stream", nil, 401, "UNAUTHORIZED"},
		{"a token read error is 503", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.tokenErr = errBoom }, "/events/E1/queue/stream", withToken, 503, "SERVICE_UNAVAILABLE"},
		{"an unknown token is 401", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.tokens = map[string]string{} }, "/events/E1/queue/stream", withToken, 401, "UNAUTHORIZED"},
		{"a token of another event is 401", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.tokens["tok"] = "E2" }, "/events/E1/queue/stream", withToken, 401, "UNAUTHORIZED"},
		{"a numbers read error is 503", func(s *fakeStore, _ *fakeLimiter, _ *Server) { s.numbersErr = errBoom }, "/events/E1/queue/stream", withToken, 503, "SERVICE_UNAVAILABLE"},
		{"a draining server is 503", func(_ *fakeStore, _ *fakeLimiter, s *Server) { s.draining = true }, "/events/E1/queue/stream", withToken, 503, "SERVICE_UNAVAILABLE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, lim := okStore(), &fakeLimiter{res: ratelimit.Result{Allowed: true}}
			s, _ := flowServer(st, lim)
			if c.mutate != nil {
				c.mutate(st, lim, s)
			}
			rec := get(s, c.path, c.req)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.code) {
				t.Fatalf("status %d body %s, want %d %s", rec.Code, rec.Body.String(), c.status, c.code)
			}
			if s.total() != 0 {
				t.Fatal("a refused request left a connection registered")
			}
		})
	}
}

func TestARateLimitedResponseTellsTheClientWhenToRetry(t *testing.T) {
	s, _ := flowServer(okStore(), &fakeLimiter{res: ratelimit.Result{RetryAfter: 2 * time.Second}})
	if rec := get(s, "/events/E1/queue/stream", withToken); rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

func TestALimiterOutageFailsOpenAndIsCounted(t *testing.T) {
	st := okStore()
	st.tokens = map[string]string{} // stop after the limiter step: unknown token → 401
	s, m := flowServer(st, &fakeLimiter{err: errBoom})
	if rec := get(s, "/events/E1/queue/stream", withToken); rec.Code != 401 {
		t.Fatalf("status %d, want the request to pass the limiter and fail on the token", rec.Code)
	}
	if m.count(MetricRateLimitOpen) != 1 {
		t.Fatalf("%s = %d, want 1", MetricRateLimitOpen, m.count(MetricRateLimitOpen))
	}
}

func TestTheRateLimitKeyIsTheRightMostForwardedAddress(t *testing.T) {
	lim := &fakeLimiter{res: ratelimit.Result{Allowed: true}}
	st := okStore()
	st.state = StateClosed
	s, _ := flowServer(st, lim)
	get(s, "/events/E1/queue/stream", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2") })
	if lim.key != rediskeys.SSERateLimit("2.2.2.2") {
		t.Fatalf("key = %q", lim.key)
	}
	get(s, "/events/E1/queue/stream", nil)
	if lim.key != rediskeys.SSERateLimit("10.0.0.9") {
		t.Fatalf("key without a header = %q, want the peer host", lim.key)
	}
	get(s, "/events/E1/queue/stream", func(r *http.Request) { r.RemoteAddr = "no-port" })
	if lim.key != rediskeys.SSERateLimit("no-port") {
		t.Fatalf("key for an address without a port = %q", lim.key)
	}
}

func TestTheQueueTokenComesFromTheHeaderBeforeTheCookieAndNeverFromTheQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?queue_token=q", nil)
	if got := queueToken(r); got != "" {
		t.Fatalf("query token accepted: %q", got)
	}
	r.AddCookie(&http.Cookie{Name: "queue_token", Value: "cookie"})
	if got := queueToken(r); got != "cookie" {
		t.Fatalf("cookie token = %q", got)
	}
	r.Header.Set("X-Queue-Token", "header")
	if got := queueToken(r); got != "header" {
		t.Fatalf("header token = %q, want it to win over the cookie", got)
	}
}

type noFlushWriter struct {
	h    http.Header
	code int
}

func (w *noFlushWriter) Header() http.Header         { return w.h }
func (w *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *noFlushWriter) WriteHeader(c int)           { w.code = c }

func TestAWriterThatCannotFlushIsRefusedWith503(t *testing.T) {
	s, _ := flowServer(okStore(), &fakeLimiter{res: ratelimit.Result{Allowed: true}})
	w := &noFlushWriter{h: http.Header{}}
	req := httptest.NewRequest(http.MethodGet, "/events/E1/queue/stream", nil)
	withToken(req)
	s.Handler().ServeHTTP(w, req)
	if w.code != 503 {
		t.Fatalf("status %d, want 503", w.code)
	}
}

// readUntil reads SSE lines until one contains want, or fails after a timeout.
func readUntil(t *testing.T, br *bufio.Reader, want string) string {
	t.Helper()
	var got strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			line, err := br.ReadString('\n')
			got.WriteString(line)
			if err != nil || strings.Contains(line, want) {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %q; got %q", want, got.String())
	}
	return got.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAnOpenStreamSendsTheSnapshotThenUpdatesThenEndsWhenTheSaleCloses(t *testing.T) {
	st := okStore()
	s, _ := flowServer(st, &fakeLimiter{res: ratelimit.Result{Allowed: true}})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events/E1/queue/stream", nil)
	req.AddCookie(&http.Cookie{Name: "queue_token", Value: "tok"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("status %d headers %v", resp.StatusCode, resp.Header)
	}
	br := bufio.NewReader(resp.Body)
	if got := readUntil(t, br, `"nowServing":41`); !strings.Contains(got, "id: 41") {
		t.Fatalf("snapshot = %q", got)
	}
	waitFor(t, "registration", func() bool { return s.Connections("E1") == 1 })

	s.handleBroadcast(broadcast(50, StateOpen))
	readUntil(t, br, `"nowServing":50`)

	s.handleBroadcast(broadcast(51, StateClosed))
	readUntil(t, br, "Tiket sudah habis") // the data line of sold-out
	if rest, _ := io.ReadAll(br); strings.TrimSpace(string(rest)) != "" {
		t.Fatalf("the stream kept sending after the ending: %q", rest)
	}
	waitFor(t, "unregistration", func() bool { return s.total() == 0 })
}

func TestAnIdleStreamSendsKeepalivesAndStopsWhenTheClientLeaves(t *testing.T) {
	st := okStore()
	cfg := DefaultConfig()
	cfg.Keepalive = 10 * time.Millisecond
	s := NewServer(cfg, Deps{Store: st, Limiter: &fakeLimiter{res: ratelimit.Result{Allowed: true}}, Metrics: nopMetrics{}, Clock: RealClock()})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events/E1/queue/stream", nil)
	withToken(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	readUntil(t, bufio.NewReader(resp.Body), ": keepalive")
	cancel()
	waitFor(t, "the handler to stop", func() bool { return s.total() == 0 })
}

func TestHealthReportsOkDegradedAndDraining(t *testing.T) {
	s, _ := flowServer(okStore(), &fakeLimiter{})
	if rec := get(s, "/health", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "degraded") {
		t.Fatalf("before the subscription: %d %s", rec.Code, rec.Body.String())
	}
	s.connected.Store(true)
	if rec := get(s, "/health", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("connected: %d %s", rec.Code, rec.Body.String())
	}
	s.draining = true
	if rec := get(s, "/health", nil); rec.Code != 503 || !strings.Contains(rec.Body.String(), "draining") {
		t.Fatalf("draining: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMalformedMessagesAreCountedAndDropped(t *testing.T) {
	s, m := flowServer(okStore(), &fakeLimiter{})
	c := newConn(8)
	s.register(c, "E1", StateOpen, 1, 1)
	drain(c)
	for _, p := range [][]byte{
		[]byte("not json"),
		[]byte(`{"eventId":"E1","state":"OPEN"}`), // no numbers
		[]byte(`{"eventId":"E1","nowServing":1,"ratePerMin":1,"state":"WARMING"}`), // bad state
	} {
		s.handle(Message{Channel: rediskeys.ChannelBroadcast, Payload: p})
	}
	for _, p := range [][]byte{[]byte("{"), []byte(`{"state":"OPEN"}`), []byte(`{"eventId":"E1","state":"x"}`)} {
		s.handle(Message{Channel: rediskeys.ChannelStatus, Payload: p})
	}
	s.handle(Message{Channel: "other", Payload: []byte("{}")})
	if m.count(MetricBadMessage) != 6 || drain(c) != "" {
		t.Fatalf("bad=%d frames=%q", m.count(MetricBadMessage), drain(c))
	}
}

func TestABroadcastForAnUnwatchedEventOrAnOlderNumberSendsNothing(t *testing.T) {
	s, _ := flowServer(okStore(), &fakeLimiter{})
	c := newConn(8)
	s.register(c, "E1", StateOpen, 100, 1)
	drain(c)
	s.handleBroadcast([]byte(`{"eventId":"E9","nowServing":5,"ratePerMin":1,"state":"OPEN"}`))
	s.handleBroadcast(broadcast(99, StateOpen))
	if got := drain(c); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestAStatusChangeIsAnnouncedOnceAndClosedEndsTheStream(t *testing.T) {
	s, _ := flowServer(okStore(), &fakeLimiter{})
	c := newConn(8)
	s.register(c, "E1", StateOpen, 1, 1)
	drain(c)
	full := []byte(`{"eventId":"E1","state":"FULL_BOOKED","fullBookedAt":1792847380}`)
	s.handleStatus(full)
	s.handleStatus(full) // a republish after an admission-ctl restart
	if got := drain(c); strings.Count(got, "event: status") != 1 {
		t.Fatalf("want one status event, got %q", got)
	}
	s.handleStatus([]byte(`{"eventId":"E1","state":"CLOSED"}`))
	got := drain(c)
	if !strings.Contains(got, "event: sold-out") || s.total() != 0 {
		t.Fatalf("closing: %q rooms=%d", got, s.total())
	}
	s.handleStatus([]byte(`{"eventId":"E1","state":"OPEN"}`)) // room is gone: nothing to do
}

func TestFreshnessMarksAQuietOpenSaleStaleOnceAndABroadcastClearsIt(t *testing.T) {
	st := okStore()
	clk := &fixedClock{now: time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)}
	s := NewServer(DefaultConfig(), Deps{Store: st, Limiter: &fakeLimiter{}, Metrics: nopMetrics{}, Clock: clk})
	c := newConn(8)
	s.register(c, "E1", StateOpen, 1, 1)
	drain(c)

	clk.advance(10 * time.Second)
	s.checkFreshness(context.Background())
	if got := drain(c); got != "" {
		t.Fatalf("stale too early: %q", got)
	}
	clk.advance(31 * time.Second)
	s.checkFreshness(context.Background())
	s.checkFreshness(context.Background())
	if got := drain(c); strings.Count(got, `"stale":true`) != 1 || !strings.Contains(got, staleNoUpdates) {
		t.Fatalf("want one stale frame, got %q", got)
	}

	late := newConn(8)
	s.register(late, "E1", StateOpen, 1, 1)
	if got := drain(late); !strings.Contains(got, `"stale":true`) {
		t.Fatalf("a customer joining a stale room must be told: %q", got)
	}

	s.handleBroadcast(broadcast(2, StateOpen))
	if got := drain(c); !strings.Contains(got, `"stale":false`) {
		t.Fatalf("recovery not announced: %q", got)
	}
}

func TestFreshnessLeavesAQuietSaleAloneWhenItCannotBeOpenOrRedisFails(t *testing.T) {
	for name, set := range map[string]func(*fakeStore){
		"the sale is scheduled": func(s *fakeStore) { s.state = StateScheduled },
		"the event is unknown":  func(s *fakeStore) { s.found = false },
		"redis cannot be read":  func(s *fakeStore) { s.stateErr = errBoom },
	} {
		t.Run(name, func(t *testing.T) {
			st := okStore()
			set(st)
			clk := &fixedClock{now: time.Now()}
			s := NewServer(DefaultConfig(), Deps{Store: st, Limiter: &fakeLimiter{}, Metrics: nopMetrics{}, Clock: clk})
			c := newConn(8)
			s.register(c, "E1", StateOpen, 1, 1)
			drain(c)
			clk.advance(time.Minute)
			s.checkFreshness(context.Background())
			if got := drain(c); got != "" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestWatchFreshnessChecksOnEveryTickUntilTheContextEnds(t *testing.T) {
	st := okStore()
	cfg := DefaultConfig()
	cfg.StaleCheck, cfg.StaleAfter = 5*time.Millisecond, 10*time.Millisecond
	s := NewServer(cfg, Deps{Store: st, Limiter: &fakeLimiter{}, Metrics: nopMetrics{}, Clock: RealClock()})
	c := newConn(8)
	s.register(c, "E1", StateOpen, 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.watchFreshness(ctx); close(done) }()
	waitFor(t, "a stale frame", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.rooms["E1"].stale
	})
	cancel()
	<-done
}

// fakeSource hands out one scripted result per Subscribe call, then blocks until the context ends.
type fakeSource struct {
	mu    sync.Mutex
	steps []srcStep
	calls int
}
type srcStep struct {
	err error
	sub *fakeSub
}
type fakeSub struct {
	ch     chan Message
	closed bool
}

func (f *fakeSub) Messages() <-chan Message { return f.ch }
func (f *fakeSub) Close() error             { f.closed = true; return nil }

func (f *fakeSource) Subscribe(ctx context.Context) (Subscription, error) {
	f.mu.Lock()
	f.calls++
	if len(f.steps) == 0 {
		f.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	st := f.steps[0]
	f.steps = f.steps[1:]
	f.mu.Unlock()
	if st.err != nil {
		return nil, st.err
	}
	return st.sub, nil
}

func TestRunMarksStaleOnALostLinkThenResyncsStateAndDeliversNewMessages(t *testing.T) {
	st := okStore()
	met := &countMetrics{}
	cfg := DefaultConfig()
	cfg.BackoffMax = 20 * time.Millisecond
	cfg.StaleCheck = time.Hour
	sub := &fakeSub{ch: make(chan Message, 4)}
	src := &fakeSource{steps: []srcStep{{err: errBoom}, {sub: sub}}}
	s := NewServer(cfg, Deps{Store: st, Limiter: &fakeLimiter{}, Source: src, Metrics: met, Clock: RealClock(), Rand: func(int64) int64 { return 0 }})
	c := newConn(32)
	s.register(c, "E1", StateOpen, 1, 1)
	drain(c)
	st.setState(StateFullBooked) // changed while the link was down

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	var out strings.Builder
	collect := func(want string) {
		t.Helper()
		waitFor(t, want, func() bool {
			out.WriteString(drain(c))
			return strings.Contains(out.String(), want)
		})
	}
	collect(staleRedisDown)
	collect(`"state":"FULL_BOOKED"`) // the resync found the change the pub/sub missed
	waitFor(t, "connected", func() bool { return s.connected.Load() })
	sub.ch <- Message{Channel: rediskeys.ChannelBroadcast, Payload: broadcast(77, StateFullBooked)}
	collect(`"nowServing":77`)

	close(sub.ch) // the link drops again
	waitFor(t, "disconnected", func() bool { return !s.connected.Load() })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunStopsWhenTheContextEndsDuringTheBackoff(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BackoffMax = time.Hour
	src := &fakeSource{steps: []srcStep{{err: errBoom}}}
	s := NewServer(cfg, Deps{Store: okStore(), Limiter: &fakeLimiter{}, Source: src, Metrics: nopMetrics{}, Clock: RealClock(), Rand: func(int64) int64 { return 0 }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitFor(t, "the first subscribe", func() bool { src.mu.Lock(); defer src.mu.Unlock(); return src.calls == 1 })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestBackoffGrowsFromAHundredMillisecondsAndIsCappedByTheConfig(t *testing.T) {
	var asked []int64
	cfg := DefaultConfig()
	cfg.BackoffMax = 40 * time.Millisecond
	s := NewServer(cfg, Deps{Metrics: nopMetrics{}, Clock: RealClock(), Rand: func(n int64) int64 { asked = append(asked, n); return 0 }})
	for _, attempt := range []int{0, 1, 20} {
		if !s.backoff(context.Background(), attempt) {
			t.Fatal("backoff reported cancellation")
		}
	}
	// base = min(100ms<<attempt, 40ms) = 40ms for all three; jitter range is base/2+1 in nanoseconds
	for _, n := range asked {
		if n != int64(20*time.Millisecond)+1 {
			t.Fatalf("jitter asked %v", asked)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.cfg.BackoffMax = time.Hour
	if s.backoff(ctx, 0) {
		t.Fatal("a cancelled context must stop the backoff")
	}
}

func TestResyncSkipsEventsItCannotReadOrDoesNotUnderstand(t *testing.T) {
	st := okStore()
	s, _ := flowServer(st, &fakeLimiter{})
	c := newConn(8)
	s.register(c, "E1", StateOpen, 1, 1)
	drain(c)
	for _, set := range []func(){
		func() { st.stateErr = errBoom },
		func() { st.stateErr, st.found = nil, false },
		func() { st.found, st.state = true, "WARMING" },
	} {
		set()
		s.resync(context.Background())
	}
	if got := drain(c); got != "" {
		t.Fatalf("got %q", got)
	}
	s.rooms["{bad}"] = &room{id: "{bad}", conns: map[*conn]struct{}{}} // an id the key builder refuses
	s.resync(context.Background())
	s.checkFreshness(context.Background())
}

func TestDrainTellsEveryoneToReconnectAndReturnsOnceTheyLeave(t *testing.T) {
	s, _ := flowServer(okStore(), &fakeLimiter{})
	c := newConn(8)
	s.register(c, "E1", StateOpen, 1, 1)
	drain(c)
	done := make(chan error, 1)
	go func() { done <- s.Drain(context.Background()) }()
	waitFor(t, "draining", s.isDraining)
	waitFor(t, "the reconnect frame", func() bool { return len(c.ch) == 1 })
	if got := drain(c); !strings.Contains(got, "event: reconnect") || !strings.Contains(got, "retry: 1000") {
		t.Fatalf("got %q", got)
	}
	s.unregister(c, "E1")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDrainClosesWhoeverIsLeftAtTheDeadlineAndStopsWhenTheContextEnds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DrainDeadline = 20 * time.Millisecond
	s := NewServer(cfg, Deps{Metrics: nopMetrics{}, Clock: RealClock(), Rand: func(int64) int64 { return 0 }})
	c := newConn(8)
	s.register(c, "E1", StateOpen, 1, 1)
	if err := s.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	default:
		t.Fatal("a connection still there at the deadline was left open")
	}

	cfg.DrainDeadline = time.Hour
	s = NewServer(cfg, Deps{Metrics: nopMetrics{}, Clock: RealClock(), Rand: func(int64) int64 { return 0 }})
	s.register(newConn(8), "E1", StateOpen, 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Drain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestTheRealClockTicksAndTheFramesCarryTheirContractShape(t *testing.T) {
	tk := RealClock().NewTicker(time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C():
	case <-time.After(time.Second):
		t.Fatal("no tick")
	}
	now := time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)
	if got := string(staleFrame(true, StateOpen, "m", now)); !strings.HasPrefix(got, "event: stale\n") {
		t.Fatalf("stale frame %q", got)
	}
	if got := string(reconnectFrame(1500, StateOpen, now)); !strings.Contains(got, "retry: 1500") {
		t.Fatalf("reconnect frame %q", got)
	}
}
