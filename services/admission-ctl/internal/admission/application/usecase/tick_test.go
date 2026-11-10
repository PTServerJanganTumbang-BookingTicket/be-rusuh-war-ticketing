package usecase

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

func admitted(tokens ...string) redisscript.Result {
	return redisscript.Result{From: 1, To: int64(len(tokens)), Code: redisscript.ResultAdmitted, Tokens: tokens}
}

func waiting(names ...string) map[string]domain.TokenInfo {
	m := map[string]domain.TokenInfo{}
	for _, n := range names {
		m[n] = domain.TokenInfo{AccountID: "acc-" + n}
	}
	return m
}

func TestFinishOrderIsRowThenGrantThenTokenThenZrem(t *testing.T) {
	q := &fakeQueue{tokens: waiting("c1"), advance: admitted("c1"), view: domain.EventView{State: "OPEN"}}
	tick, _, _ := newTick(&fakeBookings{}, q)
	if err := tick.RunEvent(context.Background(), testConfig().Events[0]); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, op := range q.ops {
		switch op {
		case "Token", "PutGrant", "AttachGrant", "Dequeue", "PublishBroadcast":
			got = append(got, op)
		}
	}
	want := []string{"Token", "PutGrant", "AttachGrant", "Dequeue", "PublishBroadcast"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestGrantLivesFromTheRowsGrantedAtNotFromNow(t *testing.T) {
	q := &fakeQueue{tokens: waiting("c1"), advance: admitted("c1"), view: domain.EventView{State: "OPEN"}}
	tick, _, _ := newTick(&oldRowBookings{age: 30 * time.Second}, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	if q.putTTL != 1050*time.Second {
		t.Fatalf("TTL = %s, want 1050s (1080 s from the row's granted_at, 30 s ago)", q.putTTL)
	}
}

// oldRowBookings answers every insert with a row that was granted age ago.
type oldRowBookings struct {
	fakeBookings
	age time.Duration
}

func (o *oldRowBookings) InsertGranted(_ context.Context, g domain.Grant) (domain.Booking, error) {
	return domain.Booking{ID: "B1", GrantToken: "G1", GrantedAt: g.GrantedAt.Add(-o.age), ExpiresAt: g.ExpiresAt.Add(-o.age), Status: "GRANTED"}, nil
}

func TestTokenGoneIsDroppedAndCounted(t *testing.T) {
	q := &fakeQueue{pending: []string{"c4"}, tokens: waiting(), advance: redisscript.Result{Code: redisscript.ResultEmpty}, view: domain.EventView{State: "OPEN"}}
	b := &fakeBookings{}
	tick, _, m := newTick(b, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	if len(b.inserted) != 0 || !reflect.DeepEqual(q.dequeued, []string{"c4"}) || m.counts[MetricGrantTokenMissing] != 1 {
		t.Fatalf("inserted %v dequeued %v metrics %v", b.inserted, q.dequeued, m.counts)
	}
}

func TestInsertFailureKeepsThePlaceAndCountsOnce(t *testing.T) {
	q := &fakeQueue{tokens: waiting("c2"), advance: admitted("c2"), view: domain.EventView{State: "OPEN"}}
	tick, _, m := newTick(&fakeBookings{insertErr: errDown}, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	if len(q.dequeued) != 0 || len(q.grants) != 0 || m.counts[MetricGrantInsertFailures] != 1 {
		t.Fatalf("dequeued %v grants %v metrics %v", q.dequeued, q.grants, m.counts)
	}
}

func TestRowThatMovedOnGetsNoNewGrant(t *testing.T) {
	q := &fakeQueue{tokens: waiting("c1"), advance: admitted("c1"), view: domain.EventView{State: "OPEN"}}
	tick, _, _ := newTick(&fakeBookings{status: "LAPSED"}, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	if len(q.grants) != 0 || len(q.attached) != 0 || !reflect.DeepEqual(q.dequeued, []string{"c1"}) {
		t.Fatalf("grants %v attached %v dequeued %v", q.grants, q.attached, q.dequeued)
	}
}

func TestCountFailureSkipsAdvanceButStillBroadcasts(t *testing.T) {
	q := &fakeQueue{view: domain.EventView{State: "OPEN"}}
	tick, _, m := newTick(&fakeBookings{countErr: errDown}, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	for _, op := range q.ops {
		if op == "Advance" {
			t.Fatal("ADVANCE ran without a count")
		}
	}
	if m.counts[MetricCountFailures] != 1 || len(q.broadcast) != 1 {
		t.Fatalf("metrics %v broadcasts %d", m.counts, len(q.broadcast))
	}
}

func TestAdvanceResultsBecomeMetrics(t *testing.T) {
	for code, metric := range map[string]string{
		redisscript.ResultStale: MetricAdvanceStale, redisscript.ResultStockUninitialized: MetricStockUninitialized,
		redisscript.ResultMissing: MetricEventStateMissing,
	} {
		q := &fakeQueue{advance: redisscript.Result{Code: code}, view: domain.EventView{State: "OPEN"}}
		b := &fakeBookings{}
		tick, _, m := newTick(b, q)
		_ = tick.RunEvent(context.Background(), testConfig().Events[0])
		if m.counts[metric] != 1 || len(b.inserted) != 0 {
			t.Errorf("%s: metrics %v inserts %d", code, m.counts, len(b.inserted))
		}
	}
}

func TestAdvanceGetsPendingAddedToBothCounts(t *testing.T) {
	var got redisscript.Args
	q := &advanceSpy{fakeQueue: fakeQueue{ns: 7, pending: []string{"p1", "p2"}, tokens: waiting("p1", "p2"), view: domain.EventView{State: "OPEN"}}, got: &got}
	b := &fakeBookings{count: domain.Admission{Open: 60, Browsing: 20}, insertErr: errDown}
	tick, _, _ := newTick(b, &q.fakeQueue)
	tick.queue = q
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	if got.Open != 62 || got.Browsing != 22 || got.NowServing != 7 {
		t.Fatalf("ADVANCE args %+v, want open 62 browsing 22 seen 7", got)
	}
}

type advanceSpy struct {
	fakeQueue
	got *redisscript.Args
}

func (a *advanceSpy) Advance(ctx context.Context, e domain.Event, args redisscript.Args) (redisscript.Result, error) {
	*a.got = args
	return a.fakeQueue.Advance(ctx, e, args)
}

func TestStuckAlertOnTheTickAfterTen(t *testing.T) {
	q := &fakeQueue{ns: 1, pending: []string{"c2"}, tokens: waiting("c2"), advance: redisscript.Result{Code: redisscript.ResultEmpty}, view: domain.EventView{State: "OPEN"}}
	tick, _, m := newTick(&fakeBookings{insertErr: errDown}, q)
	for i := 1; i <= 11; i++ {
		_ = tick.RunEvent(context.Background(), testConfig().Events[0])
		if want := i > 10; (m.counts[AlertPendingGrantsStuck] > 0) != want {
			t.Fatalf("tick %d: alert raised=%v, want %v", i, !want, want)
		}
	}
}

func TestRecoverIsOneRedisCallWhenNothingIsPending(t *testing.T) {
	q := &fakeQueue{view: domain.EventView{State: "OPEN"}, advance: redisscript.Result{Code: redisscript.ResultEmpty}}
	b := &fakeBookings{}
	tick, _, _ := newTick(b, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0]) // warms the cached now_serving
	q.ops = nil
	tick.RecoverEvent(context.Background(), testConfig().Events[0])
	if !reflect.DeepEqual(q.ops, []string{"Pending"}) || len(b.inserted) != 0 {
		t.Fatalf("RECOVER made calls %v", q.ops)
	}
}

func TestOneEventsFailureDoesNotHoldUpTheNext(t *testing.T) {
	cfg := testConfig()
	cfg.Events = append(cfg.Events, domain.Event{ID: "E2"})
	q := &fakeQueue{advanceErr: errDown, view: domain.EventView{State: "OPEN"}}
	m := newMetrics()
	tick := NewTick(cfg, &fakeBookings{}, q, newClock(), m)
	tick.Run(context.Background())
	if m.counts[MetricTickFailures] != 2 { // both fail, and both were tried
		t.Fatalf("tick failures = %d, want 2", m.counts[MetricTickFailures])
	}
}

func TestLoopDoesNotOverlapAndCountsOverruns(t *testing.T) {
	cfg := testConfig()
	cfg.Interval = 20 * time.Millisecond
	q := &slowQueue{fakeQueue: fakeQueue{view: domain.EventView{State: "OPEN"}}, delay: 50 * time.Millisecond}
	m := newMetrics()
	tick := NewTick(cfg, &fakeBookings{}, q, newClock(), m)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	tick.Loop(ctx)
	if m.counts[MetricTickOverrun] == 0 || q.maxActive > 1 {
		t.Fatalf("overruns %d, max parallel calls %d", m.counts[MetricTickOverrun], q.maxActive)
	}
}

type slowQueue struct {
	fakeQueue
	delay             time.Duration
	active, maxActive int
}

func (s *slowQueue) Snapshot(ctx context.Context, ev string) (int64, int64, error) {
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	time.Sleep(s.delay)
	defer func() { s.active-- }()
	return s.fakeQueue.Snapshot(ctx, ev)
}
