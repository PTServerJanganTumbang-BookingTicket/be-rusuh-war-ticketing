package usecase_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	queueredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/redis"
)

type scriptedLimiter struct {
	mu    sync.Mutex
	deny  map[string]time.Duration // key prefix → retry-after
	err   error
	calls []string
}

func (l *scriptedLimiter) Allow(_ context.Context, key string, _ ratelimit.Limit) (ratelimit.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, key)
	if l.err != nil {
		return ratelimit.Result{}, l.err
	}
	if d, ok := l.deny[key]; ok {
		return ratelimit.Result{RetryAfter: d}, nil
	}
	return ratelimit.Result{Allowed: true}, nil
}

type scriptedCaptcha struct{ result domain.CaptchaResult }

func (c scriptedCaptcha) Verify(context.Context, string, string) domain.CaptchaResult {
	return c.result
}

type recordingMetrics struct {
	mu   sync.Mutex
	seen map[string]int
}

func (m *recordingMetrics) Incr(n string)              { m.record(n) }
func (m *recordingMetrics) Record(n string, _ float64) { m.record(n) }
func (m *recordingMetrics) record(n string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen == nil {
		m.seen = map[string]int{}
	}
	m.seen[n]++
}

type gateEnv struct {
	join    *usecase.Join
	rdb     *goredis.Client
	limiter *scriptedLimiter
	metrics *recordingMetrics
	bearer  string
}

func gates(t *testing.T, cfgMut func(*usecase.Config), hash ...any) gateEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, _ := rediskeys.NewEvent("E1")
	if len(hash) == 0 {
		hash = []any{"state", "OPEN", "now_serving", 10, "issued", 20, "name", "Konser"}
	}
	rdb.HSet(context.Background(), ev.QueueHash(), hash...)

	issuer, _ := token.NewIssuer("secret", "rusuh-user", time.Hour)
	bearer, _, _ := issuer.Issue("acc-1", "a@example.com", true)
	cfg := usecase.DefaultConfig()
	if cfgMut != nil {
		cfgMut(&cfg)
	}
	env := gateEnv{rdb: rdb, limiter: &scriptedLimiter{deny: map[string]time.Duration{}}, metrics: &recordingMetrics{}, bearer: bearer}
	env.join = usecase.NewJoin(cfg, usecase.Deps{
		Store:    queueredis.NewStore(rdb),
		Captcha:  scriptedCaptcha{domain.CaptchaValid},
		Limiter:  env.limiter,
		Tokens:   issuer,
		Metrics:  env.metrics,
		NewToken: func() string { return "qt_GATE" },
	})
	return env
}

func (e gateEnv) tryJoin(in usecase.JoinInput) (usecase.JoinResult, *apperror.AppError) {
	if in.EventID == "" {
		in.EventID = "E1"
	}
	if in.DeviceID == "" {
		in.DeviceID = "dev-1"
	}
	if in.ClientIP == "" {
		in.ClientIP = "1.2.3.4"
	}
	res, err := e.join.Join(context.Background(), in)
	var ae *apperror.AppError
	if err != nil && !errors.As(err, &ae) {
		panic(err)
	}
	return res, ae
}

func (e gateEnv) issued(t *testing.T) int64 {
	t.Helper()
	ev, _ := rediskeys.NewEvent("E1")
	n, _ := e.rdb.HGet(context.Background(), ev.QueueHash(), "issued").Int64()
	return n
}

func TestJoinGateRejectionsCarryTheirStatusAndCodeAndNeverIssueATicket(t *testing.T) {
	cases := []struct {
		name   string
		hash   []any
		mut    func(*usecase.Config)
		in     usecase.JoinInput
		status int
		code   string
	}{
		{"an unknown-shaped event id is 404", nil, nil, usecase.JoinInput{EventID: "{x}", CaptchaToken: "c"}, 404, domain.CodeNotFound},
		{"a missing captcha token is 400", nil, nil, usecase.JoinInput{CaptchaToken: ""}, 400, domain.CodeCaptchaRequired},
		{"a scheduled sale is 409", []any{"state", "SCHEDULED", "sale_starts_at", "2026-12-01T10:00:00Z"}, nil, usecase.JoinInput{CaptchaToken: "c"}, 409, domain.CodeSaleNotOpen},
		{"a closed sale is 410", []any{"state", "CLOSED"}, nil, usecase.JoinInput{CaptchaToken: "c"}, 410, domain.CodeSaleClosed},
		{"an event without a state is 503 not ready", []any{"name", "Konser"}, nil, usecase.JoinInput{CaptchaToken: "c"}, 503, domain.CodeQueueNotReady},
		{"a queue at capacity is 503 queue full", nil, func(c *usecase.Config) { c.Capacity = 10 }, usecase.JoinInput{CaptchaToken: "c"}, 503, domain.CodeQueueFull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := gates(t, c.mut, c.hash...)
			before := e.issued(t)
			_, ae := e.tryJoin(c.in)
			if ae == nil || ae.HTTPStatus != c.status || ae.ErrorCode != c.code {
				t.Fatalf("error = %+v, want %d %s", ae, c.status, c.code)
			}
			if e.issued(t) != before {
				t.Fatal("a rejected join burned a ticket number")
			}
		})
	}
}

func TestAnIPOverTheLimitIs429WithTheRetryRoundedUpToWholeSeconds(t *testing.T) {
	e := gates(t, nil)
	e.limiter.deny[rediskeys.JoinRateLimit("1.2.3.4")] = 1500 * time.Millisecond
	_, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"})
	if ae == nil || ae.HTTPStatus != 429 || ae.ErrorCode != domain.CodeTooManyRequests || ae.RetryAfter != 2*time.Second {
		t.Fatalf("error = %+v", ae)
	}
	e.limiter.deny[rediskeys.JoinRateLimit("1.2.3.4")] = 0
	if _, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"}); ae == nil || ae.RetryAfter != time.Second {
		t.Fatalf("a zero retry-after must still say at least one second: %+v", ae)
	}
}

func TestAnAccountOverItsLimitIs429ButAGuestIsOnlyCheckedPerIP(t *testing.T) {
	e := gates(t, nil)
	e.limiter.deny[rediskeys.JoinAcctLimit("acc-1")] = 30 * time.Second
	if _, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c", Bearer: e.bearer}); ae == nil || ae.HTTPStatus != 429 || ae.RetryAfter != 30*time.Second {
		t.Fatalf("account limit: %+v", ae)
	}
	e.limiter.calls = nil
	if _, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"}); ae != nil {
		t.Fatalf("guest: %+v", ae)
	}
	if len(e.limiter.calls) != 1 || e.limiter.calls[0] != rediskeys.JoinRateLimit("1.2.3.4") {
		t.Fatalf("limiter calls = %v, want only the IP bucket", e.limiter.calls)
	}
}

func TestALimiterOutageFailsOpenAndIsCounted(t *testing.T) {
	e := gates(t, nil)
	e.limiter.err = errBoom
	if _, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"}); ae != nil {
		t.Fatalf("a limiter outage blocked the join: %+v", ae)
	}
	if e.metrics.seen[domain.MetricRateLimitFailOpen] == 0 {
		t.Fatal("the fail-open was not counted")
	}
}

func TestSwitchingRateLimitsOffSkipsTheLimiterEntirely(t *testing.T) {
	e := gates(t, func(c *usecase.Config) { c.RateLimits = false })
	if _, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c", Bearer: e.bearer}); ae != nil {
		t.Fatal(ae)
	}
	if len(e.limiter.calls) != 0 {
		t.Fatalf("limiter calls = %v", e.limiter.calls)
	}
}

func TestACaptchaTheProviderRejectsIs403AndAnOutageFailsOpenAndIsCounted(t *testing.T) {
	e := gates(t, nil)
	e.join = rebuildWithCaptcha(t, e, domain.CaptchaInvalid)
	if _, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"}); ae == nil || ae.HTTPStatus != 403 || ae.ErrorCode != domain.CodeCaptchaFailed {
		t.Fatalf("invalid captcha: %+v", ae)
	}
	e.join = rebuildWithCaptcha(t, e, domain.CaptchaUnavailable)
	if _, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"}); ae != nil {
		t.Fatalf("a provider outage blocked the join: %+v", ae)
	}
	if e.metrics.seen[domain.MetricCaptchaFailOpen] != 1 {
		t.Fatalf("metrics = %v", e.metrics.seen)
	}
}

func rebuildWithCaptcha(t *testing.T, e gateEnv, r domain.CaptchaResult) *usecase.Join {
	t.Helper()
	issuer, _ := token.NewIssuer("secret", "rusuh-user", time.Hour)
	return usecase.NewJoin(usecase.DefaultConfig(), usecase.Deps{
		Store: queueredis.NewStore(e.rdb), Captcha: scriptedCaptcha{r}, Limiter: e.limiter, Tokens: issuer,
		Metrics: e.metrics, NewToken: func() string { return "qt_GATE" },
	})
}

func TestAnUnreadableEventHashIs503NotAnOpenGate(t *testing.T) {
	e := gates(t, nil)
	ev, _ := rediskeys.NewEvent("E1")
	e.rdb.Del(context.Background(), ev.QueueHash())
	e.rdb.Set(context.Background(), ev.QueueHash(), "oops", 0)
	if _, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"}); ae == nil || ae.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("error = %+v, want 503", ae)
	}
}

func TestAJoinedCallerSeesTheirPositionAsTicketMinusNowServing(t *testing.T) {
	e := gates(t, nil)
	res, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"})
	if ae != nil {
		t.Fatal(ae)
	}
	if res.Engagement.TicketNo != 21 || res.NowServing != 10 || res.Position() != 11 {
		t.Fatalf("ticket %d now_serving %d position %d", res.Engagement.TicketNo, res.NowServing, res.Position())
	}
}

func TestJoin_whenFullyBookedANewCustomerStillGetsAPlace(t *testing.T) {
	e := gates(t, nil, "state", "FULL_BOOKED", "now_serving", 10, "issued", 20, "full_booked_at", 1792847380)
	before := e.issued(t)
	res, ae := e.tryJoin(usecase.JoinInput{CaptchaToken: "c"})
	if ae != nil {
		t.Fatalf("join refused: %+v", ae)
	}
	if res.Engagement.TicketNo != before+1 {
		t.Fatalf("ticket_no = %d, want %d", res.Engagement.TicketNo, before+1)
	}
}

func TestJoin_errorDataUsesTheHomePageStateNames(t *testing.T) {
	_, ae := gates(t, nil, "state", "SCHEDULED", "sale_starts_at", "2026-12-01T10:00:00Z").tryJoin(usecase.JoinInput{CaptchaToken: "c"})
	if ae == nil || ae.Data.(map[string]any)["queue_state"] != "SCHEDULED" {
		t.Fatalf("scheduled data = %+v", ae)
	}
	_, ae = gates(t, func(c *usecase.Config) { c.Capacity = 10 }).tryJoin(usecase.JoinInput{CaptchaToken: "c"})
	if ae == nil || ae.Data.(map[string]any)["queue_state"] != "FULL_QUEUE" {
		t.Fatalf("full queue data = %+v", ae)
	}
}
