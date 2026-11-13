package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// perEventQueue answers SNAPSHOT with each event's own counters.
type perEventQueue struct {
	fakeQueue
	snap map[string][2]int64 // event -> {now_serving, issued}
}

func (q *perEventQueue) Snapshot(_ context.Context, ev string) (int64, int64, error) {
	s := q.snap[ev]
	return s[0], s[1], nil
}

func lastGauge(m *metrics, name, ev string) (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.eventGauges[name+"/"+ev]
	if len(v) == 0 {
		return 0, false
	}
	return v[len(v)-1], true
}

func TestQueueDepthIsReportedPerEventSoTwoSalesGiveTwoSeparateValues(t *testing.T) {
	q := &perEventQueue{
		fakeQueue: fakeQueue{view: domain.EventView{State: "OPEN"}},
		snap:      map[string][2]int64{"EA": {200, 1000}, "EB": {10, 60}},
	}
	tick, _, m := newTick(&fakeBookings{}, q)
	for _, ev := range []string{"EA", "EB"} {
		if err := tick.RunEvent(context.Background(), domain.Event{ID: ev}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []struct {
		name, ev string
		value    float64
	}{
		{GaugeQueueDepth, "EA", 800}, {GaugeNowServing, "EA", 200}, {GaugeIssued, "EA", 1000},
		{GaugeQueueDepth, "EB", 50}, {GaugeNowServing, "EB", 10}, {GaugeIssued, "EB", 60},
	} {
		if got, ok := lastGauge(m, want.name, want.ev); !ok || got != want.value {
			t.Errorf("%s{EventId=%s} = %v (written %v), want %v", want.name, want.ev, got, ok, want.value)
		}
	}
}

func TestQueueDepthIsIssuedMinusNowServingAndNeverBelowZero(t *testing.T) {
	// now_serving can pass issued for a moment (a ticket number burned by a rejected join); depth is then 0, not −5.
	q := &fakeQueue{ns: 30, issued: 25, view: domain.EventView{State: "OPEN"}}
	tick, _, m := newTick(&fakeBookings{}, q)
	if err := tick.RunEvent(context.Background(), testConfig().Events[0]); err != nil {
		t.Fatal(err)
	}
	if got, ok := lastGauge(m, GaugeQueueDepth, "E1"); !ok || got != 0 {
		t.Fatalf("QueueDepth = %v (written %v), want 0", got, ok)
	}
}

func TestQueueGaugesAreWrittenAtMostOncePerHeartbeat(t *testing.T) {
	q := &fakeQueue{ns: 5, issued: 25, view: domain.EventView{State: "OPEN"}}
	tick, clk, m := newTick(&fakeBookings{}, q)
	ev := testConfig().Events[0]
	for i := 0; i < 3; i++ { // three 500 ms ticks
		_ = tick.RunEvent(context.Background(), ev)
		clk.add(500 * time.Millisecond)
	}
	if n := len(m.eventGauges[GaugeQueueDepth+"/E1"]); n != 1 {
		t.Fatalf("QueueDepth written %d times in 1.5 s, want once", n)
	}
	clk.add(10 * time.Second)
	_ = tick.RunEvent(context.Background(), ev)
	if n := len(m.eventGauges[GaugeQueueDepth+"/E1"]); n != 2 {
		t.Fatalf("QueueDepth written %d times after the heartbeat, want 2", n)
	}
}

func TestAGrantedPlaceCountsGrantedOnceForItsEvent(t *testing.T) {
	q := &fakeQueue{tokens: waiting("c1", "c2"), advance: admitted("c1", "c2"), view: domain.EventView{State: "OPEN"}}
	tick, _, m := newTick(&fakeBookings{}, q)
	if err := tick.RunEvent(context.Background(), testConfig().Events[0]); err != nil {
		t.Fatal(err)
	}
	if got := m.eventCounts[MetricGranted+"/E1"]; got != 2 {
		t.Fatalf("Granted{EventId=E1} = %d, want 2", got)
	}
}

func TestAGrantWhoseRowAnEarlierTickSavedIsNotCountedAgain(t *testing.T) {
	// RECOVER finishes a token whose row an earlier, interrupted finish already saved: the row comes back under that
	// call's booking id, so this call did not let anyone new in.
	q := &fakeQueue{tokens: waiting("c1"), advance: admitted("c1"), view: domain.EventView{State: "OPEN"}}
	tick, _, m := newTick(&oldRowBookings{age: 30 * time.Second}, q)
	if err := tick.RunEvent(context.Background(), testConfig().Events[0]); err != nil {
		t.Fatal(err)
	}
	if got := m.counts[MetricGranted]; got != 0 {
		t.Fatalf("Granted = %d, want 0 for a row saved earlier", got)
	}
}

func TestAFailedGrantInsertCountsTheFailureAndNotGranted(t *testing.T) {
	q := &fakeQueue{tokens: waiting("c1"), advance: admitted("c1"), view: domain.EventView{State: "OPEN"}}
	tick, _, m := newTick(&fakeBookings{insertErr: errDown}, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	if m.counts[MetricGrantInsertFailures] != 1 || m.counts[MetricGranted] != 0 {
		t.Fatalf("metrics %v, want one GrantInsertFailures and no Granted", m.counts)
	}
}

func TestEachSweepCountsItsEndedBookingsUnderTheirOwnEvent(t *testing.T) {
	b := &fakeBookings{
		lapsed:  []domain.Ended{{BookingID: "B1", EventID: "EA"}, {BookingID: "B2", EventID: "EB"}},
		expired: []domain.Ended{{BookingID: "B3", EventID: "EA"}},
	}
	h := &fakeHeld{due: []domain.EndedHold{{Ended: domain.Ended{BookingID: "B4", EventID: "EB"}}}}
	tick, m := heldTick(b, &fakeQueue{}, h, &fakeHolds{})
	tick.Sweep(context.Background())

	want := map[string]int{
		MetricLapsed + "/EA": 1, MetricLapsed + "/EB": 1,
		MetricSelectingExpired + "/EA": 1,
		MetricHeldExpired + "/EB":      1,
	}
	for k, n := range want {
		if m.eventCounts[k] != n {
			t.Errorf("%s = %d, want %d (all: %v)", k, m.eventCounts[k], n, m.eventCounts)
		}
	}
	if len(m.eventCounts) != len(want) {
		t.Errorf("unexpected per-event counts: %v", m.eventCounts)
	}
}

func TestTickDurationIsTheSlowestTickOfEachWindow(t *testing.T) {
	w := tickTimer{every: 10 * time.Second}
	start := time.Date(2026, 11, 13, 19, 0, 0, 0, time.UTC)
	for i, took := range []time.Duration{100, 700, 200} {
		if _, due := w.observe(took*time.Millisecond, start.Add(time.Duration(i)*500*time.Millisecond)); due {
			t.Fatalf("reported after %d ticks, before the window ended", i+1)
		}
	}
	worst, due := w.observe(50*time.Millisecond, start.Add(10*time.Second))
	if !due || worst != 700*time.Millisecond {
		t.Fatalf("got %s (due %v), want the slowest tick 700ms", worst, due)
	}
	if _, due := w.observe(50*time.Millisecond, start.Add(10500*time.Millisecond)); due {
		t.Fatal("a new window reported at once")
	}
}
