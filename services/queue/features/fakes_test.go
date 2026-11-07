package features

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

// fakeMetrics records every metric.
type fakeMetrics struct {
	mu     sync.Mutex
	counts map[string]int
	values map[string][]float64
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{counts: map[string]int{}, values: map[string][]float64{}}
}

func (m *fakeMetrics) Incr(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[name]++
}

func (m *fakeMetrics) Record(name string, v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[name] = append(m.values[name], v)
}

func (m *fakeMetrics) count(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[name]
}

func (m *fakeMetrics) recorded(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.values[name])
}

// fakeCaptcha accepts / rejects listed tokens, or is "unavailable".
type fakeCaptcha struct {
	accept      map[string]bool
	reject      map[string]bool
	unavailable bool
	calls       atomic.Int64
}

func (c *fakeCaptcha) Verify(_ context.Context, tok, _ string) domain.CaptchaResult {
	c.calls.Add(1)
	switch {
	case c.unavailable:
		return domain.CaptchaUnavailable
	case c.reject[tok]:
		return domain.CaptchaInvalid
	case c.accept == nil || c.accept[tok]:
		return domain.CaptchaValid
	}
	return domain.CaptchaInvalid
}

// countingLimiter wraps the real limiter: counts calls, records keys, and can
// refuse or error on demand.
type countingLimiter struct {
	next   outbound.RateLimiter
	fail   bool // return an error
	refuse bool // always refuse
	calls  atomic.Int64
	mu     sync.Mutex
	keys   []string
}

func (l *countingLimiter) Allow(ctx context.Context, key string, lim ratelimit.Limit) (ratelimit.Result, error) {
	l.calls.Add(1)
	l.mu.Lock()
	l.keys = append(l.keys, key)
	l.mu.Unlock()
	switch {
	case l.fail:
		return ratelimit.Result{}, errors.New("limiter: connection refused")
	case l.refuse:
		return ratelimit.Result{RetryAfter: 15 * time.Second}, nil
	}
	return l.next.Allow(ctx, key, lim)
}

func (l *countingLimiter) charged() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.keys...)
}

// probeStore wraps the real store: counts the gate probes and can "crash"
// (fail) SaveEngagement once, to simulate a join dying after INCR.
type probeStore struct {
	outbound.QueueStore
	snapshots   atomic.Int64
	issues      atomic.Int64
	crashOnSave atomic.Bool
}

func (s *probeStore) Snapshot(ctx context.Context, ev rediskeys.Event) (domain.EventSnapshot, error) {
	s.snapshots.Add(1)
	return s.QueueStore.Snapshot(ctx, ev)
}

func (s *probeStore) IssueTicket(ctx context.Context, ev rediskeys.Event) (int64, error) {
	s.issues.Add(1)
	return s.QueueStore.IssueTicket(ctx, ev)
}

func (s *probeStore) SaveEngagement(ctx context.Context, e domain.Engagement, ttl time.Duration) error {
	if s.crashOnSave.CompareAndSwap(true, false) {
		return errors.New("simulated crash after INCR, before HSET")
	}
	return s.QueueStore.SaveEngagement(ctx, e, ttl)
}

// fakeBookings is the unit-level BookingReader: never finds a row, counts reads.
type fakeBookings struct {
	mu    sync.Mutex
	reads []time.Time
	rows  map[string]domain.Booking // by queue_token
}

func (b *fakeBookings) ByQueueToken(_ context.Context, _, qt string) (domain.Booking, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads = append(b.reads, time.Now())
	r, ok := b.rows[qt]
	return r, ok, nil
}

func (b *fakeBookings) lastRead() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.reads) == 0 {
		return time.Time{}
	}
	return b.reads[len(b.reads)-1]
}

// failingBookings errors on every query ("Postgres fails every query").
type failingBookings struct{}

func (failingBookings) ByQueueToken(context.Context, string, string) (domain.Booking, bool, error) {
	return domain.Booking{}, false, errors.New("pq: connection refused")
}

// sqlTracer records every statement the app's pool sends.
type sqlTracer struct {
	mu   sync.Mutex
	sqls []string
}

func (t *sqlTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	t.mu.Lock()
	t.sqls = append(t.sqls, d.SQL)
	t.mu.Unlock()
	return ctx
}

func (t *sqlTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *sqlTracer) reset() {
	t.mu.Lock()
	t.sqls = nil
	t.mu.Unlock()
}

func (t *sqlTracer) statements() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.sqls...)
}

func isSelect(sql string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "SELECT")
}
