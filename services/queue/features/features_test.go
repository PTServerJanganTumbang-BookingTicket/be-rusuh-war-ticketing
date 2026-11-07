package features

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/idgen"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/specsuite"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	queuehttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/http"
	queuepg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/postgres"
	queueredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

const (
	testJWTSecret  = "test-jwt-secret"
	testPepper     = "test-device-pepper"
	testSSEBase    = "https://events.rusuhtiket.test"
	defaultDevice  = "dev-aaaaaaaaaaaaaaaa"
	defaultIP      = "192.0.2.10"
	defaultCaptcha = "cap-ok"
)

func TestFeatures(t *testing.T) {
	t.Cleanup(dropTestDB)
	specsuite.Run(t, initScenario)
}

// ---------------------------------------------------------------- Postgres

var (
	dbOnce   sync.Once
	dbErr    error
	adminDB  *pgxpool.Pool // stand-in controller, seeding, truncation
	appDB    *pgxpool.Pool // the app's writer pool, traced
	tracer   = &sqlTracer{}
	dbName   string
	dbServer string // DATABASE_URL of the server database (for DROP)
)

// testDB creates one throwaway database per package run with every migration
// applied (real migrations, real Postgres).
func testDB() error {
	dbOnce.Do(func() { dbErr = createTestDB() })
	return dbErr
}

func createTestDB() error {
	dbServer = specsuite.DatabaseURL()
	if dbServer == "" {
		return fmt.Errorf("DATABASE_URL is not set (make verify / make step export it)")
	}
	ctx := context.Background()
	srv, err := pgxpool.New(ctx, dbServer)
	if err != nil {
		return err
	}
	defer srv.Close()
	dbName = fmt.Sprintf("queue_test_%d", time.Now().UnixNano())
	if _, err := srv.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		return fmt.Errorf("create test database: %w", err)
	}
	u, err := url.Parse(dbServer)
	if err != nil {
		return err
	}
	u.Path = "/" + dbName
	if adminDB, err = pgxpool.New(ctx, u.String()); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "*.up.sql"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no migrations found: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := adminDB.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", filepath.Base(f), err)
		}
	}
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return err
	}
	cfg.ConnConfig.Tracer = tracer
	appDB, err = pgxpool.NewWithConfig(ctx, cfg)
	return err
}

func dropTestDB() {
	if dbName == "" {
		return
	}
	appDB.Close()
	adminDB.Close()
	ctx := context.Background()
	if srv, err := pgxpool.New(ctx, dbServer); err == nil {
		_, _ = srv.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		srv.Close()
	}
}

// ---------------------------------------------------------------- Redis

// cmdHook records the commands the app sends.
type cmdHook struct {
	mu      sync.Mutex
	names   []string
	scripts []string // EVALSHA sha / "eval"
}

func (h *cmdHook) record(c goredis.Cmder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	name := strings.ToLower(c.Name())
	h.names = append(h.names, name)
	switch name {
	case "evalsha", "evalsha_ro":
		h.scripts = append(h.scripts, fmt.Sprint(c.Args()[1]))
	case "eval", "eval_ro", "fcall":
		h.scripts = append(h.scripts, name)
	}
}

func (h *cmdHook) DialHook(next goredis.DialHook) goredis.DialHook { return next }

func (h *cmdHook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, c goredis.Cmder) error {
		h.record(c)
		return next(ctx, c)
	}
}

func (h *cmdHook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []goredis.Cmder) error {
		for _, c := range cmds {
			h.record(c)
		}
		return next(ctx, cmds)
	}
}

func (h *cmdHook) snapshot() (names, scripts []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.names...), append([]string(nil), h.scripts...)
}

// ---------------------------------------------------------------- world

type response struct {
	status   int
	header   http.Header
	body     []byte
	took     time.Duration
	deviceID string
}

type world struct {
	integration bool
	mr          *miniredis.Miniredis
	rdb         goredis.UniversalClient // test's own client (seeding, assertions)
	appRdb      goredis.UniversalClient // the app's client
	hook        *cmdHook
	usedIPs     map[string]bool
	ipMu        sync.Mutex

	cfg      usecase.Config
	opt      queuehttp.Options
	clock    func() time.Time
	captcha  outbound.Captcha
	fcaptcha *fakeCaptcha
	limiter  *countingLimiter
	store    *probeStore
	bookings outbound.BookingReader
	fbook    *fakeBookings
	metrics  *fakeMetrics
	issuer   *token.Issuer
	handler  http.Handler
	redisBad bool

	restoreClock func()
	logs         *bytes.Buffer
	prevLogger   *slog.Logger

	// request knobs for the next join
	bearer string

	resp     *response
	resps    []*response
	stand    *standIn
	presentQ string // a queue_token seeded "earlier"
	gateFail string
	accounts map[string]string // account -> access token
	captured map[string]any    // misc values between steps
}

func newWorld(integration bool) *world {
	cfg := usecase.DefaultConfig()
	cfg.DevicePepper = []byte(testPepper)
	issuer, _ := token.NewIssuer(testJWTSecret, "rusuh-user", time.Hour)
	return &world{
		integration: integration,
		hook:        &cmdHook{},
		usedIPs:     map[string]bool{defaultIP: true},
		cfg:         cfg,
		opt:         queuehttp.Options{CookieMaxAge: cfg.SaleWindow, SSEBaseURL: testSSEBase},
		clock:       time.Now,
		fcaptcha:    &fakeCaptcha{accept: map[string]bool{defaultCaptcha: true}, reject: map[string]bool{"cap-bad": true}},
		metrics:     newFakeMetrics(),
		issuer:      issuer,
		fbook:       &fakeBookings{rows: map[string]domain.Booking{}},
		accounts:    map[string]string{},
		captured:    map[string]any{},
	}
}

func initScenario(sc *godog.ScenarioContext) {
	var w *world
	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		integration := hasTag(s, "@integration")
		w = newWorld(integration)
		if err := w.start(); err != nil {
			return ctx, err
		}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.stop()
		return ctx, err
	})
	registerSetupSteps(sc, func() *world { return w })
	registerJoinSteps(sc, func() *world { return w })
	registerAssertSteps(sc, func() *world { return w })
	registerTurnstileSteps(sc, func() *world { return w })
	registerBindingSteps(sc, func() *world { return w })
}

func hasTag(s *godog.Scenario, tag string) bool {
	for _, t := range s.Tags {
		if t.Name == tag {
			return true
		}
	}
	return false
}

// testEvents are the event ids the features use; cleaned before each
// integration scenario.
var testEvents = []string{"E1", "E2"}

func (w *world) start() error {
	w.logs = &bytes.Buffer{}
	w.prevLogger = slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&lockedWriter{w: w.logs}, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if w.integration {
		w.rdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{specsuite.RedisAddr()}})
		w.appRdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{specsuite.RedisAddr()}})
		if err := w.rdb.Ping(context.Background()).Err(); err != nil {
			return fmt.Errorf("redis %s: %w (make deps-up)", specsuite.RedisAddr(), err)
		}
		if err := w.cleanRedis(); err != nil {
			return err
		}
		if specsuite.DatabaseURL() != "" {
			if err := testDB(); err != nil {
				return err
			}
			if _, err := adminDB.Exec(context.Background(), "TRUNCATE bookings"); err != nil {
				return err
			}
			tracer.reset()
			w.bookings = queuepg.NewBookings(appDB)
		}
	} else {
		w.mr = miniredis.NewMiniRedis()
		if err := w.mr.Start(); err != nil {
			return err
		}
		w.rdb = goredis.NewClient(&goredis.Options{Addr: w.mr.Addr()})
		w.appRdb = goredis.NewClient(&goredis.Options{Addr: w.mr.Addr()})
		w.bookings = w.fbook
	}
	w.appRdb.AddHook(w.hook)
	return nil
}

func (w *world) stop() {
	if w.stand != nil {
		w.stand.stop()
	}
	if w.restoreClock != nil {
		w.restoreClock()
	}
	slog.SetDefault(w.prevLogger)
	if w.integration && w.rdb != nil {
		_ = w.cleanRedis()
	}
	if w.rdb != nil {
		_ = w.rdb.Close()
	}
	if w.appRdb != nil {
		_ = w.appRdb.Close()
	}
	if w.mr != nil {
		w.mr.Close()
	}
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func ev(id string) rediskeys.Event {
	e, err := rediskeys.NewEvent(id)
	if err != nil {
		panic(err)
	}
	return e
}

// cleanRedis deletes every key the features can create for the test events,
// plus their tokens, account pointers and the rate-limit keys used.
func (w *world) cleanRedis() error {
	ctx := context.Background()
	for _, id := range testEvents {
		e := ev(id)
		members, _ := w.rdb.ZRange(ctx, e.ByTicket(), 0, -1).Result()
		for _, m := range members {
			_ = w.rdb.Del(ctx, rediskeys.Token(m)).Err()
		}
		for _, k := range []string{e.QueueHash(), e.QueueState(), e.QueueIssued(), e.ByTicket(), e.SoldOutAt(), e.Expiries()} {
			if err := w.rdb.Del(ctx, k).Err(); err != nil {
				return err
			}
		}
		if err := w.scanDel(ctx, "acct:{"+id+"}:*"); err != nil {
			return err
		}
	}
	for _, qt := range []string{"qt_A", "qt_G", "qt_X", "qt_B", "qt_GONE", "qt_UNKNOWN", "qt_NOHASH"} {
		_ = w.rdb.Del(ctx, rediskeys.Token(qt)).Err()
	}
	for ip := range w.usedIPs {
		_ = w.rdb.Del(ctx, rediskeys.JoinRateLimit(ip)).Err()
	}
	for _, a := range []string{"acc-1", "acc-2"} {
		_ = w.rdb.Del(ctx, rediskeys.JoinAcctLimit(a)).Err()
	}
	return nil
}

func (w *world) scanDel(ctx context.Context, pattern string) error {
	keys, err := w.scan(ctx, pattern)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := w.rdb.Del(ctx, k).Err(); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) scan(ctx context.Context, pattern string) ([]string, error) {
	var out []string
	var mu sync.Mutex
	scanOne := func(ctx context.Context, c goredis.Cmdable) error {
		it := c.Scan(ctx, 0, pattern, 1000).Iterator()
		for it.Next(ctx) {
			mu.Lock()
			out = append(out, it.Val())
			mu.Unlock()
		}
		return it.Err()
	}
	if cc, ok := w.rdb.(*goredis.ClusterClient); ok {
		err := cc.ForEachMaster(ctx, func(ctx context.Context, c *goredis.Client) error { return scanOne(ctx, c) })
		return out, err
	}
	return out, scanOne(ctx, w.rdb)
}

// app builds (once per scenario, lazily, after the Given steps) the handler
// exactly as delivery wires it, but with probes and fakes around the ports.
func (w *world) app() http.Handler {
	if w.handler != nil {
		return w.handler
	}
	rdb := w.appRdb
	if w.redisBad {
		rdb = goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	}
	w.store = &probeStore{QueueStore: queueredis.NewStore(rdb)}
	if w.limiter == nil {
		w.limiter = &countingLimiter{}
	}
	w.limiter.next = ratelimit.New(rdb)
	captcha := w.captcha
	if captcha == nil {
		captcha = w.fcaptcha
	}
	join := usecase.NewJoin(w.cfg, usecase.Deps{
		Store:    w.store,
		Bookings: w.bookings,
		Captcha:  captcha,
		Limiter:  w.limiter,
		Tokens:   w.issuer,
		Metrics:  w.metrics,
		Clock:    w.clock,
		NewToken: func() string { return idgen.NewPrefixed("qt_") },
	})
	mux := http.NewServeMux()
	queuehttp.RegisterRoutes(mux, queuehttp.NewHandler(join, w.opt))
	w.handler = mux
	return mux
}

// freeze sets the use case clock and the envelope's server_time clock.
func (w *world) freeze(at time.Time) {
	w.clock = func() time.Time { return at }
	if w.restoreClock != nil {
		w.restoreClock()
	}
	w.restoreClock = apinizer.SetClock(w.clock)
}

// joinReq describes one join call.
type joinReq struct {
	eventID    string
	captcha    *string // nil = default
	body       *string // raw body, overrides captcha
	deviceID   string
	noDevice   bool
	bearer     string
	queueToken string
	carrier    string // "cookie" (default) or "header"
	ip         string
	header     map[string]string
	ctx        context.Context
}

func (w *world) buildRequest(r joinReq) *http.Request {
	body := ""
	switch {
	case r.body != nil:
		body = *r.body
	default:
		c := defaultCaptcha
		if r.captcha != nil {
			c = *r.captcha
		}
		body = fmt.Sprintf(`{"captcha_token": %q}`, c)
	}
	path := "/events/" + url.PathEscape(r.eventID) + "/queue/join"
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if r.ctx != nil {
		req = req.WithContext(r.ctx)
	}
	req.Header.Set("Content-Type", "application/json")
	if !r.noDevice {
		d := r.deviceID
		if d == "" {
			d = defaultDevice
		}
		req.Header.Set("X-Device-Id", d)
	}
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.queueToken != "" {
		if r.carrier == "header" {
			req.Header.Set(queuehttp.HeaderQueueToken, r.queueToken)
		} else {
			req.AddCookie(&http.Cookie{Name: queuehttp.CookieName, Value: r.queueToken})
		}
	}
	for k, v := range r.header {
		req.Header.Set(k, v)
	}
	ip := r.ip
	if ip == "" {
		ip = defaultIP
	}
	w.ipMu.Lock()
	w.usedIPs[ip] = true
	w.ipMu.Unlock()
	req.RemoteAddr = net.JoinHostPort(ip, "40000")
	return req
}

// do sends one join and records the response.
func (w *world) do(r joinReq) *response {
	h := w.app()
	req := w.buildRequest(r)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	resp := &response{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes(), took: time.Since(start), deviceID: req.Header.Get("X-Device-Id")}
	return resp
}

func (w *world) record(resp *response) {
	w.resp = resp
	w.resps = append(w.resps, resp)
}

// parallel sends n joins from `workers` goroutines; mk builds the i-th request.
func (w *world) parallel(n, workers int, mk func(i int) joinReq) []*response {
	w.app()
	out := make([]*response, n)
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				out[i] = w.do(mk(i))
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
	w.resps = append(w.resps, out...)
	if n > 0 {
		w.resp = out[n-1]
	}
	return out
}
