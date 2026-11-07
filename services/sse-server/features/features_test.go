package features

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/specsuite"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/sse-server/internal/stream"
)

const (
	defaultIP   = "192.0.2.10"
	startClock  = "2026-10-24T13:10:00.000Z"
	myToken     = "qt_me"
	myTicket    = 31700
	connBuffer  = 16
	settleDelay = 25 * time.Millisecond
)

func TestFeatures(t *testing.T) { specsuite.Run(t, initScenario) }

// ---------------------------------------------------------------- fake clock

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

type fakeTicker struct {
	c       chan time.Time
	period  time.Duration
	next    time.Time
	stopped bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.c }
func (t *fakeTicker) Stop() {
	t.stopped = true
}

func newFakeClock(at time.Time) *fakeClock { return &fakeClock{now: at} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(d time.Duration) stream.Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{c: make(chan time.Time, 1), period: d, next: c.now.Add(d)}
	c.tickers = append(c.tickers, t)
	return t
}

func (c *fakeClock) set(at time.Time) {
	c.mu.Lock()
	c.now = at
	c.mu.Unlock()
}

// advance moves time by d, firing every ticker that comes due (non-blocking, like a real ticker).
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	live := c.tickers[:0]
	for _, t := range c.tickers {
		if t.stopped {
			continue
		}
		live = append(live, t)
		for !t.next.After(now) {
			select {
			case t.c <- now:
			default:
			}
			t.next = t.next.Add(t.period)
		}
	}
	c.tickers = live
	c.mu.Unlock()
}

// ---------------------------------------------------------------- fake source

type fakeSource struct {
	mu    sync.Mutex
	subs  []*fakeSub
	down  bool
	calls int
}

type fakeSub struct {
	ch     chan stream.Message
	closed bool
}

func (s *fakeSub) Messages() <-chan stream.Message { return s.ch }
func (s *fakeSub) Close() error                    { return nil }

func (f *fakeSource) Subscribe(context.Context) (stream.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.down {
		return nil, fmt.Errorf("fake source: redis is down")
	}
	sub := &fakeSub{ch: make(chan stream.Message, 4096)}
	f.subs = append(f.subs, sub)
	return sub, nil
}

func (f *fakeSource) publish(channel string, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		if !s.closed {
			s.ch <- stream.Message{Channel: channel, Payload: payload}
		}
	}
}

// drop closes every live subscription, like a lost connection.
func (f *fakeSource) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		if !s.closed {
			s.closed = true
			close(s.ch)
		}
	}
}

func (f *fakeSource) setDown(down bool) {
	f.mu.Lock()
	f.down = down
	f.mu.Unlock()
}

func (f *fakeSource) subscribeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// ---------------------------------------------------------------- fake metrics

type fakeMetrics struct {
	mu       sync.Mutex
	counts   map[string]int
	gauges   map[string]float64
	history  map[string][]float64
	recorded []string
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{counts: map[string]int{}, gauges: map[string]float64{}, history: map[string][]float64{}}
}

func (m *fakeMetrics) Incr(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[name]++
	m.recorded = append(m.recorded, name)
}

func (m *fakeMetrics) Gauge(name, event string, v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[name+"|"+event] = v
	m.history[name] = append(m.history[name], v)
	m.recorded = append(m.recorded, name+"|"+event)
}

func (m *fakeMetrics) count(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[name]
}

func (m *fakeMetrics) gauge(name, event string) (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.gauges[name+"|"+event]
	return v, ok
}

func (m *fakeMetrics) names() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.recorded, " ")
}

// ---------------------------------------------------------------- switchable limiter and store

type switchLimiter struct {
	inner stream.Limiter
	fail  bool
}

func (l *switchLimiter) Allow(ctx context.Context, key string, lim ratelimit.Limit) (ratelimit.Result, error) {
	if l.fail {
		return ratelimit.Result{}, fmt.Errorf("limiter: redis unreachable")
	}
	return l.inner.Allow(ctx, key, lim)
}

type failStore struct{}

func (failStore) State(context.Context, rediskeys.Event) (string, bool, error) {
	return "", false, fmt.Errorf("redis unreachable")
}

func (failStore) Numbers(context.Context, rediskeys.Event) (int64, int64, error) {
	return 0, 0, fmt.Errorf("redis unreachable")
}

func (failStore) TokenEvent(context.Context, string) (string, bool, error) {
	return "", false, fmt.Errorf("redis unreachable")
}

// ---------------------------------------------------------------- in-process connection

// memWriter is a ResponseWriter whose body the test can read at any time. A
// blocked writer never returns from Write, like a phone that stopped reading.
type memWriter struct {
	mu        sync.Mutex
	h         http.Header
	status    int
	buf       bytes.Buffer
	block     chan struct{}
	writes    atomic.Int64
	lastWrite atomic.Int64 // unix nanos of the last write
}

func newMemWriter() *memWriter { return &memWriter{h: http.Header{}} }

func (m *memWriter) Header() http.Header { return m.h }

func (m *memWriter) WriteHeader(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status == 0 {
		m.status = code
	}
}

func (m *memWriter) Write(b []byte) (int, error) {
	if m.block != nil {
		<-m.block
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status == 0 {
		m.status = http.StatusOK
	}
	m.writes.Add(1)
	m.lastWrite.Store(time.Now().UnixNano())
	return m.buf.Write(b)
}

func (m *memWriter) Flush() {}

func (m *memWriter) body() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

func (m *memWriter) code() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

type client struct {
	w      *memWriter
	cancel context.CancelFunc
	done   chan struct{}
	ev     string
	token  string
	cursor int // event frames (not keepalive comments) already consumed by "I receive ..." steps
	srv    *stream.Server
}

func (c *client) open() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *client) leave() {
	c.cancel()
	<-c.done
}

// takeNew consumes and returns every event frame since the last call (no keepalive comments).
func (c *client) takeNew() string { return c.take(-1) }

// take consumes the next n event frames (all of them when n < 0).
func (c *client) take(n int) string {
	var fs []frame
	for _, f := range c.frames() {
		if !f.comment {
			fs = append(fs, f)
		}
	}
	if c.cursor > len(fs) {
		c.cursor = len(fs)
	}
	fs = fs[c.cursor:]
	if n >= 0 && n < len(fs) {
		fs = fs[:n]
	}
	c.cursor += len(fs)
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.raw + "\n\n")
	}
	return b.String()
}

// ---------------------------------------------------------------- world

type world struct {
	lastPublish time.Time // when the last message was published (latency checks)
	integration bool
	mr          *miniredis.Miniredis
	rdb         goredis.UniversalClient

	clk     *fakeClock
	source  *fakeSource
	metrics *fakeMetrics
	limiter *switchLimiter
	cfg     stream.Config

	servers []*stream.Server
	cancels []context.CancelFunc
	runErrs []chan error

	clients []*client
	me      *client
	last    *connectResult
	byEvent map[string][]*client
	tokens  int

	drain    chan error
	logs     *bytes.Buffer
	prevLog  *slog.Logger
	captured map[string]any
}

type connectResult struct {
	status int
	header http.Header
	body   string
	client *client
}

func hasTag(s *godog.Scenario, tag string) bool {
	for _, t := range s.Tags {
		if t.Name == tag {
			return true
		}
	}
	return false
}

func initScenario(sc *godog.ScenarioContext) {
	var w *world
	W := func() *world { return w }
	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		w = newWorld(hasTag(s, "@integration"))
		return ctx, w.start()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.stop()
		return ctx, err
	})
	registerConnectSteps(sc, W)
	registerUpdateSteps(sc, W)
	registerReliabilitySteps(sc, W)
	registerContractSteps(sc, W)
	registerScaleSteps(sc, W)
}

func newWorld(integration bool) *world {
	cfg := stream.DefaultConfig()
	cfg.ConnBuffer = connBuffer
	at, _ := time.Parse(time.RFC3339Nano, startClock)
	return &world{integration: integration, cfg: cfg, clk: newFakeClock(at), source: &fakeSource{},
		metrics: newFakeMetrics(), byEvent: map[string][]*client{}, captured: map[string]any{}}
}

func (w *world) start() error {
	w.logs = &bytes.Buffer{}
	w.prevLog = slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&lockedWriter{b: w.logs}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if w.integration {
		w.rdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{specsuite.RedisAddr()}})
		if err := w.rdb.Ping(context.Background()).Err(); err != nil {
			return fmt.Errorf("redis %s: %w (make deps-up)", specsuite.RedisAddr(), err)
		}
		return w.cleanRedis()
	}
	w.mr = miniredis.NewMiniRedis()
	if err := w.mr.Start(); err != nil {
		return err
	}
	w.rdb = goredis.NewClient(&goredis.Options{Addr: w.mr.Addr()})
	return nil
}

type lockedWriter struct {
	mu sync.Mutex
	b  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

var testEvents = []string{"E1", "E2", "E3", "E9"}

func (w *world) cleanRedis() error {
	ctx := context.Background()
	for _, id := range testEvents {
		e := ev(id)
		for _, k := range []string{e.QueueHash(), e.QueueState(), e.FullBookedAt()} {
			if err := w.rdb.Del(ctx, k).Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *world) stop() {
	for _, c := range w.clients {
		c.cancel()
	}
	for _, c := range w.cancels {
		c()
	}
	slog.SetDefault(w.prevLog)
	if w.integration && w.rdb != nil {
		_ = w.cleanRedis()
	}
	if w.rdb != nil {
		_ = w.rdb.Close()
	}
	if w.mr != nil {
		w.mr.Close()
	}
}

func ev(id string) rediskeys.Event {
	e, err := rediskeys.NewEvent(id)
	if err != nil {
		panic(err)
	}
	return e
}

// clock is the fake clock in unit scenarios and the wall clock in integration ones.
func (w *world) clock() stream.Clock {
	if w.integration {
		return stream.RealClock()
	}
	return w.clk
}

func (w *world) newServer(store stream.Store) *stream.Server {
	w.limiter = &switchLimiter{inner: ratelimit.New(w.rdb)}
	var src stream.Source = w.source
	if w.integration {
		src = stream.NewRedisSource(w.rdb)
	}
	if store == nil {
		store = stream.NewRedisStore(w.rdb)
	}
	var n int64
	srv := stream.NewServer(w.cfg, stream.Deps{
		Store: store, Limiter: w.limiter, Source: src, Metrics: w.metrics, Clock: w.clock(),
		Rand: func(m int64) int64 {
			n = (n*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
			return n % m
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()
	w.cancels = append(w.cancels, cancel)
	w.runErrs = append(w.runErrs, errc)
	w.servers = append(w.servers, srv)
	w.settle()
	return srv
}

// srv is the primary server, started on first use.
func (w *world) srv() *stream.Server {
	if len(w.servers) == 0 {
		w.newServer(nil)
	}
	return w.servers[0]
}

// settle gives goroutines time to deliver what was just triggered.
func (w *world) settle() { time.Sleep(settleDelay) }

// pass moves the fake clock forward in 1 s steps so every ticker fires in order.
func (w *world) pass(d time.Duration) {
	if w.integration {
		time.Sleep(d)
		return
	}
	for ; d > 0; d -= time.Second {
		step := time.Second
		if d < step {
			step = d
		}
		w.clk.advance(step)
		time.Sleep(2 * time.Millisecond)
	}
	w.settle()
}

// publish sends a message the way admission-ctl would.
func (w *world) publish(channel string, payload []byte) error {
	w.lastPublish = time.Now()
	if w.integration {
		return w.rdb.Publish(context.Background(), channel, string(payload)).Err()
	}
	w.source.publish(channel, payload)
	return nil
}

// ---------------------------------------------------------------- seeding

func (w *world) seedEvent(id, state string, nowServing, rate int64) error {
	ctx := context.Background()
	e := ev(id)
	if err := w.rdb.Set(ctx, e.QueueState(), state, 0).Err(); err != nil {
		return err
	}
	if err := w.rdb.HSet(ctx, e.QueueHash(), "now_serving", nowServing, "rate_per_min", rate).Err(); err != nil {
		return err
	}
	return nil
}

func (w *world) setState(id, state string) error {
	ctx := context.Background()
	e := ev(id)
	if err := w.rdb.Set(ctx, e.QueueState(), state, 0).Err(); err != nil {
		return err
	}
	if state == "FULL_BOOKED" {
		return w.rdb.Set(ctx, e.FullBookedAt(), w.clock().Now().UTC().Format(time.RFC3339), 0).Err()
	}
	return nil
}

func (w *world) seedToken(token, eventID string, ticketNo int64, device string) error {
	fields := []any{"event_id", eventID, "ticket_no", ticketNo}
	if device != "" {
		fields = append(fields, "device_hash", "hash-of-"+device)
	}
	return w.rdb.HSet(context.Background(), rediskeys.Token(token), fields...).Err()
}

func (w *world) ensureEvent(id string) error {
	n, err := w.rdb.Exists(context.Background(), ev(id).QueueState()).Result()
	if err != nil || n > 0 {
		return err
	}
	return w.seedEvent(id, "OPEN", 31600, 318)
}

func (w *world) nowServing(id string) int64 {
	v, _ := w.rdb.HGet(context.Background(), ev(id).QueueHash(), "now_serving").Int64()
	return v
}

// ---------------------------------------------------------------- connecting

type openOpts struct {
	slow    bool
	srv     *stream.Server
	cookie  string
	header  map[string]string
	ip      string
	query   string
	lastEvt string
}

// open runs the stream handler in a goroutine and returns once it has answered.
func (w *world) open(eventID string, o openOpts) *client {
	srv := o.srv
	if srv == nil {
		srv = w.srv()
	}
	path := "/events/" + eventID + "/queue/stream" + o.query
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	if o.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "queue_token", Value: o.cookie})
	}
	for k, v := range o.header {
		req.Header.Set(k, v)
	}
	if o.lastEvt != "" {
		req.Header.Set("Last-Event-ID", o.lastEvt)
	}
	ip := o.ip
	if ip == "" {
		ip = defaultIP
	}
	req.RemoteAddr = ip + ":40000"
	c := &client{w: newMemWriter(), cancel: cancel, done: make(chan struct{}), ev: eventID, token: o.cookie, srv: srv}
	if o.slow {
		c.w.block = make(chan struct{})
	}
	go func() {
		defer close(c.done)
		srv.Handler().ServeHTTP(c.w, req)
	}()
	if o.slow { // a blocked writer never answers; do not wait for it
		w.clients = append(w.clients, c)
		w.byEvent[eventID] = append(w.byEvent[eventID], c)
		return c
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.w.code() == http.StatusOK && len(c.w.body()) > 0 { // a stream answers with its snapshot
			break
		}
		select {
		case <-c.done:
			deadline = time.Now()
		default:
			time.Sleep(time.Millisecond)
		}
	}
	w.clients = append(w.clients, c)
	w.byEvent[eventID] = append(w.byEvent[eventID], c)
	w.last = &connectResult{status: c.w.code(), header: c.w.Header(), body: c.w.body(), client: c}
	return c
}

// connectMany opens n valid streams on an event, one new token each.
func (w *world) connectMany(srv *stream.Server, eventID string, n int) ([]*client, error) {
	if err := w.ensureEvent(eventID); err != nil {
		return nil, err
	}
	out := make([]*client, 0, n)
	for i := 0; i < n; i++ {
		w.tokens++
		tok := fmt.Sprintf("qt_c%06d", w.tokens)
		if err := w.seedToken(tok, eventID, int64(40000+w.tokens), ""); err != nil {
			return nil, err
		}
		c := w.open(eventID, openOpts{srv: srv, cookie: tok, ip: fmt.Sprintf("10.%d.%d.%d", (w.tokens>>16)&255, (w.tokens>>8)&255, w.tokens&255)})
		c.takeNew() // their snapshot is not what the scenarios look at
		out = append(out, c)
	}
	return out, nil
}

// ---------------------------------------------------------------- SSE frames

type frame struct {
	comment bool
	event   string
	data    string
	id      string
	retry   string
	raw     string
}

func parseFrames(body string) []frame {
	var out []frame
	for _, blk := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(blk) == "" {
			continue
		}
		f := frame{raw: blk}
		for _, line := range strings.Split(blk, "\n") {
			switch {
			case strings.HasPrefix(line, ":"):
				f.comment = true
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				f.data = strings.TrimPrefix(line, "data: ")
			case strings.HasPrefix(line, "id: "):
				f.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "retry: "):
				f.retry = strings.TrimPrefix(line, "retry: ")
			}
		}
		out = append(out, f)
	}
	return out
}

func (c *client) frames() []frame { return parseFrames(c.w.body()) }

func (c *client) named(name string) []frame {
	var out []frame
	for _, f := range c.frames() {
		if f.event == name {
			out = append(out, f)
		}
	}
	return out
}

func norm(s string) string { return strings.TrimRight(s, "\n") }

// openAsync starts a stream handler and returns at once (large crowds).
func (w *world) openAsync(srv *stream.Server, eventID, token, ip string) *client {
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events/"+eventID+"/queue/stream", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "queue_token", Value: token})
	req.RemoteAddr = ip + ":40000"
	c := &client{w: newMemWriter(), cancel: cancel, done: make(chan struct{}), ev: eventID, token: token, srv: srv}
	go func() {
		defer close(c.done)
		srv.Handler().ServeHTTP(c.w, req)
	}()
	w.clients = append(w.clients, c)
	return c
}

// series is every value a gauge was set to, in order.
func (m *fakeMetrics) series(name string) []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]float64(nil), m.history[name]...)
}
