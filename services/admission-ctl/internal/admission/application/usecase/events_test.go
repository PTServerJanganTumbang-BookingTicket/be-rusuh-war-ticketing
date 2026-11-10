package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

type fakeSource struct {
	events []domain.Event
	err    error
	calls  int
}

func (s *fakeSource) Discover(context.Context) ([]domain.Event, error) {
	s.calls++
	return s.events, s.err
}

func ticked(q *fakeQueue) map[string]bool {
	out := map[string]bool{}
	for _, c := range q.snapshotEvents {
		out[c] = true
	}
	return out
}

func newDiscovering(src *fakeSource) (*Tick, *clock, *metrics, *fakeQueue) {
	q := &fakeQueue{view: domain.EventView{State: "OPEN"}}
	cfg := testConfig()
	cfg.Events = nil
	clk, m := newClock(), newMetrics()
	tick := NewTick(cfg, &fakeBookings{}, q, clk, m)
	tick.UseEvents(src, 30*time.Second)
	return tick, clk, m, q
}

func TestEventsAreDiscoveredOnTheFirstTick(t *testing.T) {
	src := &fakeSource{events: []domain.Event{{ID: "evt_a"}, {ID: "evt_b"}}}
	tick, _, _, q := newDiscovering(src)
	tick.Run(context.Background())
	if got := ticked(q); !got["evt_a"] || !got["evt_b"] || src.calls != 1 {
		t.Fatalf("ticked %v after %d discoveries", got, src.calls)
	}
}

func TestEventsAreRefreshedOnlyAfterTheInterval(t *testing.T) {
	src := &fakeSource{events: []domain.Event{{ID: "evt_a"}}}
	tick, clk, _, q := newDiscovering(src)
	tick.Run(context.Background())
	clk.add(29 * time.Second)
	tick.Run(context.Background())
	if src.calls != 1 {
		t.Fatalf("discovered %d times within 29 s", src.calls)
	}
	src.events = append(src.events, domain.Event{ID: "evt_new"})
	clk.add(time.Second)
	tick.Run(context.Background())
	if src.calls != 2 || !ticked(q)["evt_new"] {
		t.Fatalf("after 30 s: %d discoveries, ticked %v", src.calls, ticked(q))
	}
}

func TestAClosedSaleDropsOutAtTheNextRefresh(t *testing.T) {
	src := &fakeSource{events: []domain.Event{{ID: "evt_a"}, {ID: "evt_old"}}}
	tick, clk, _, q := newDiscovering(src)
	tick.Run(context.Background())
	src.events = []domain.Event{{ID: "evt_a"}}
	q.snapshotEvents = nil
	clk.add(30 * time.Second)
	tick.Run(context.Background())
	if ticked(q)["evt_old"] {
		t.Fatal("a sale that left the list was still ticked")
	}
}

func TestDiscoveryFailureKeepsThePreviousListAndCounts(t *testing.T) {
	src := &fakeSource{events: []domain.Event{{ID: "evt_a"}}}
	tick, clk, m, q := newDiscovering(src)
	tick.Run(context.Background())
	src.err = errors.New("scan failed")
	q.snapshotEvents = nil
	clk.add(30 * time.Second)
	tick.Run(context.Background())
	if !ticked(q)["evt_a"] || m.counts[MetricEventDiscoveryFailures] != 1 {
		t.Fatalf("ticked %v, metrics %v", ticked(q), m.counts)
	}
}

func TestFirstDiscoveryFailureTicksNothingAndRetriesNextTick(t *testing.T) {
	src := &fakeSource{err: errors.New("scan failed")}
	tick, clk, m, q := newDiscovering(src)
	tick.Run(context.Background())
	if len(q.snapshotEvents) != 0 || m.counts[MetricEventDiscoveryFailures] != 1 {
		t.Fatalf("ticked %v, metrics %v", q.snapshotEvents, m.counts)
	}
	src.err, src.events = nil, []domain.Event{{ID: "evt_a"}}
	clk.add(500 * time.Millisecond) // not 30 s: a failed discovery must be retried at once
	tick.Run(context.Background())
	if !ticked(q)["evt_a"] {
		t.Fatal("the tick did not retry discovery after a failure")
	}
}

func TestWithoutASourceTheConfiguredEventsAreTicked(t *testing.T) {
	q := &fakeQueue{view: domain.EventView{State: "OPEN"}}
	tick, _, _ := newTick(&fakeBookings{}, q)
	tick.Run(context.Background())
	if !ticked(q)["E1"] {
		t.Fatalf("ticked %v", ticked(q))
	}
}
