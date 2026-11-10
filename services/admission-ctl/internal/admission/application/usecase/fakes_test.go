package usecase

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// Plain in-memory fakes for the unit tests of this package. The behaviour specs (Gherkin) use the real
// Redis adapter; these only isolate the use-case decisions.

type clock struct{ t time.Time }

func (c *clock) Now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{t: time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)} }

type metrics struct {
	mu     sync.Mutex
	counts map[string]int
	gauges map[string]float64
}

func newMetrics() *metrics { return &metrics{counts: map[string]int{}, gauges: map[string]float64{}} }
func (m *metrics) Inc(n string) {
	m.mu.Lock()
	m.counts[n]++
	m.mu.Unlock()
}
func (m *metrics) Gauge(n string, v float64) {
	m.mu.Lock()
	m.gauges[n] = v
	m.mu.Unlock()
}

type fakeBookings struct {
	count       domain.Admission
	countErr    error
	insertErr   error
	status      string // status returned for an existing row ("" = GRANTED)
	inserted    []domain.Grant
	lapsed      []domain.Ended
	lapseErr    error
	lapseCalls  int
	expired     []domain.Ended
	expireErr   error
	expireCalls int
}

func (b *fakeBookings) ExpireSelectingDue(context.Context, time.Time) ([]domain.Ended, error) {
	b.expireCalls++
	return b.expired, b.expireErr
}

func (b *fakeBookings) CountAdmission(context.Context, string) (domain.Admission, error) {
	return b.count, b.countErr
}

func (b *fakeBookings) InsertGranted(_ context.Context, g domain.Grant) (domain.Booking, error) {
	if b.insertErr != nil {
		return domain.Booking{}, b.insertErr
	}
	b.inserted = append(b.inserted, g)
	return domain.Booking{ID: g.BookingID, GrantToken: g.GrantToken, GrantedAt: g.GrantedAt, ExpiresAt: g.ExpiresAt, Status: b.status}, nil
}

func (b *fakeBookings) LapseDue(context.Context, time.Time) ([]domain.Ended, error) {
	b.lapseCalls++
	return b.lapsed, b.lapseErr
}

type fakeQueue struct {
	ns, issued int64
	pending    []string
	tokens     map[string]domain.TokenInfo // missing = gone
	advance    redisscript.Result
	advanceErr error
	view       domain.EventView

	ops       []string
	dequeued  []string
	grants    []string
	attached  []string
	dropped   []string
	broadcast [][]byte
	status    [][]byte
	rates     []int64
	closedAt  []int64
	putTTL    time.Duration

	snapshotEvents []string // event of every Snapshot call, to see which events were ticked
}

func (q *fakeQueue) log(op string) { q.ops = append(q.ops, op) }

func (q *fakeQueue) Snapshot(_ context.Context, ev string) (int64, int64, error) {
	q.log("Snapshot")
	q.snapshotEvents = append(q.snapshotEvents, ev)
	return q.ns, q.issued, nil
}
func (q *fakeQueue) Pending(_ context.Context, _ string, upTo int64) ([]string, error) {
	q.log("Pending")
	_ = upTo
	return append([]string(nil), q.pending...), nil
}
func (q *fakeQueue) PendingCount(context.Context, string, int64) (int64, error) {
	q.log("PendingCount")
	return int64(len(q.pending)), nil
}
func (q *fakeQueue) Advance(context.Context, domain.Event, redisscript.Args) (redisscript.Result, error) {
	q.log("Advance")
	return q.advance, q.advanceErr
}
func (q *fakeQueue) Token(_ context.Context, qt string) (domain.TokenInfo, bool, error) {
	q.log("Token")
	info, ok := q.tokens[qt]
	return info, ok, nil
}
func (q *fakeQueue) PutGrant(_ context.Context, _, qt string, _ domain.Booking, _ domain.TokenInfo, ttl time.Duration) error {
	q.log("PutGrant")
	q.grants, q.putTTL = append(q.grants, qt), ttl
	return nil
}
func (q *fakeQueue) AttachGrant(_ context.Context, qt string, _ domain.Booking) error {
	q.log("AttachGrant")
	q.attached = append(q.attached, qt)
	return nil
}
func (q *fakeQueue) Dequeue(_ context.Context, _, qt string) error {
	q.log("Dequeue")
	q.dequeued = append(q.dequeued, qt)
	var rest []string
	for _, p := range q.pending {
		if p != qt {
			rest = append(rest, p)
		}
	}
	q.pending = rest
	return nil
}
func (q *fakeQueue) View(context.Context, string) (domain.EventView, error) {
	q.log("View")
	return q.view, nil
}
func (q *fakeQueue) SetRate(_ context.Context, _ string, r int64) error {
	q.log("SetRate")
	q.rates = append(q.rates, r)
	return nil
}
func (q *fakeQueue) Close(_ context.Context, _ string, at int64) error {
	q.log("Close")
	q.closedAt = append(q.closedAt, at)
	return nil
}
func (q *fakeQueue) DropGrant(_ context.Context, gt string) error {
	q.log("DropGrant")
	q.dropped = append(q.dropped, gt)
	return nil
}
func (q *fakeQueue) PublishBroadcast(_ context.Context, p []byte) error {
	q.log("PublishBroadcast")
	q.broadcast = append(q.broadcast, p)
	return nil
}
func (q *fakeQueue) PublishStatus(_ context.Context, p []byte) error {
	q.log("PublishStatus")
	q.status = append(q.status, p)
	return nil
}

var errDown = errors.New("connection refused")

func testConfig() Config {
	c := DefaultConfig()
	c.Events = []domain.Event{{ID: "E1", Categories: []string{"VIP"}}}
	return c
}

func newTick(b outbound.Bookings, q outbound.Queue) (*Tick, *clock, *metrics) {
	clk, m := newClock(), newMetrics()
	return NewTick(testConfig(), b, q, clk, m), clk, m
}
