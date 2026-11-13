package usecase

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

func TestSweepKillsOnlyTheGrantOfEachLapsedBooking(t *testing.T) {
	b := &fakeBookings{lapsed: []domain.Ended{{BookingID: "B1", GrantToken: "G1"}, {BookingID: "B2"}}}
	q := &fakeQueue{}
	tick, _, m := newTick(b, q)
	if n := tick.Sweep(context.Background()); n != 2 {
		t.Fatalf("lapsed %d, want 2", n)
	}
	if !reflect.DeepEqual(q.dropped, []string{"G1"}) || m.counts[MetricLapsed] != 2 {
		t.Fatalf("dropped %v metrics %v", q.dropped, m.counts)
	}
	for _, op := range q.ops {
		if op != "DropGrant" {
			t.Fatalf("sweep also called %s: the queue token and acct must stay", op)
		}
	}
}

func TestSweepFailureIsCountedNotFatal(t *testing.T) {
	tick, _, m := newTick(&fakeBookings{lapseErr: errDown}, &fakeQueue{})
	if n := tick.Sweep(context.Background()); n != 0 || m.counts[MetricSweepFailures] != 1 {
		t.Fatalf("n=%d metrics %v", n, m.counts)
	}
}

func TestRunSweepsBeforeTheEvents(t *testing.T) {
	b := &fakeBookings{}
	q := &fakeQueue{view: domain.EventView{State: "OPEN"}}
	tick, _, _ := newTick(b, q)
	tick.Run(context.Background())
	if b.lapseCalls != 1 {
		t.Fatalf("sweep ran %d times in one tick", b.lapseCalls)
	}
}

func TestSweepExpiresDueSelectingBookingsAndKillsOnlyTheirGrants(t *testing.T) {
	b := &fakeBookings{expired: []domain.Ended{{BookingID: "B3", GrantToken: "G3"}, {BookingID: "B4", GrantToken: "G4"}}}
	q := &fakeQueue{}
	tick, _, m := newTick(b, q)
	if n := tick.Sweep(context.Background()); n != 2 {
		t.Fatalf("swept %d, want the 2 expired bookings", n)
	}
	if b.expireCalls != 1 || !reflect.DeepEqual(q.dropped, []string{"G3", "G4"}) || m.counts[MetricSelectingExpired] != 2 {
		t.Fatalf("expire calls %d, dropped %v, metrics %v", b.expireCalls, q.dropped, m.counts)
	}
	for _, op := range q.ops {
		if op != "DropGrant" {
			t.Fatalf("sweep also called %s: a SELECTING booking holds no stock or NIK, and token/acct stay", op)
		}
	}
}

func TestOneFailingSweepQueryDoesNotStopTheOther(t *testing.T) {
	b := &fakeBookings{lapseErr: errDown, expired: []domain.Ended{{BookingID: "B3", GrantToken: "G3"}}}
	q := &fakeQueue{}
	tick, _, m := newTick(b, q)
	if n := tick.Sweep(context.Background()); n != 1 || m.counts[MetricSweepFailures] != 1 || !reflect.DeepEqual(q.dropped, []string{"G3"}) {
		t.Fatalf("lapse failing: n=%d dropped %v metrics %v", n, q.dropped, m.counts)
	}

	b = &fakeBookings{expireErr: errDown, lapsed: []domain.Ended{{BookingID: "B1", GrantToken: "G1"}}}
	q = &fakeQueue{}
	tick, _, m = newTick(b, q)
	if n := tick.Sweep(context.Background()); n != 1 || m.counts[MetricSweepFailures] != 1 || !reflect.DeepEqual(q.dropped, []string{"G1"}) {
		t.Fatalf("expire failing: n=%d dropped %v metrics %v", n, q.dropped, m.counts)
	}
}

// ── Awaiting payment sweep tests (F6) ─────────────────────────────────────────

// fakePaymentReconciler implements outbound.PaymentReconciler for tests.
type fakePaymentReconciler struct {
	calls  int
	result domain.SweepResult
	err    error
}

func (f *fakePaymentReconciler) SweepAwaitingPayment(ctx context.Context) (domain.SweepResult, error) {
	f.calls++
	return f.result, f.err
}

func TestSweep_AwaitingUnwired_BehavesExactlyAsBefore(t *testing.T) {
	b := &fakeBookings{lapsed: []domain.Ended{{BookingID: "B1", GrantToken: "G1"}}}
	q := &fakeQueue{}
	tick, _, m := newTick(b, q)

	// Without WithAwaitingPaymentSweep called, sweep should work as before
	n := tick.Sweep(context.Background())

	if n != 1 {
		t.Fatalf("sweep returned %d, want 1 (the lapsed booking)", n)
	}
	if m.counts[MetricLapsed] != 1 {
		t.Fatalf("expected 1 lapsed, got metrics %v", m.counts)
	}
	// No awaiting sweep metrics should be recorded
	if m.counts[MetricAwaitingSweptExpired] != 0 || m.counts[MetricAwaitingSweptSettled] != 0 {
		t.Fatalf("awaiting metrics should be 0 when unwired: %v", m.counts)
	}
}

func TestSweep_AwaitingRespectsItsInterval(t *testing.T) {
	b := &fakeBookings{}
	q := &fakeQueue{view: domain.EventView{State: "OPEN"}}
	tick, clk, m := newTick(b, q)

	// Wire the awaiting sweep with a 5-second interval
	interval := 5 * time.Second
	pr := &fakePaymentReconciler{result: domain.SweepResult{Expired: 1}}
	tick.WithAwaitingPaymentSweep(pr, interval)

	// First tick: should invoke
	tick.Run(context.Background())
	if pr.calls != 1 {
		t.Fatalf("first tick: expected 1 call, got %d", pr.calls)
	}

	// Ticks 2-5: should NOT invoke (interval not elapsed)
	for i := 2; i <= 5; i++ {
		clk.add(500 * time.Millisecond) // advance less than the interval
		tick.Run(context.Background())
	}
	if pr.calls != 1 {
		t.Fatalf("after 4 more ticks: expected still 1 call, got %d", pr.calls)
	}

	// Tick 6: interval elapsed, should invoke again
	clk.add(3 * time.Second) // now past the interval
	tick.Run(context.Background())
	if pr.calls != 2 {
		t.Fatalf("after interval: expected 2 calls, got %d", pr.calls)
	}

	if m.counts[MetricAwaitingSweptExpired] != 2 {
		t.Fatalf("expected 2 expired from 2 invocations, got %v", m.counts)
	}
}

func TestSweep_AwaitingFailure_DoesNotStopTheOtherThreeSweeps(t *testing.T) {
	b := &fakeBookings{
		lapsed:  []domain.Ended{{BookingID: "B1", GrantToken: "G1"}},
		expired: []domain.Ended{{BookingID: "B2", GrantToken: "G2"}},
	}
	q := &fakeQueue{}
	tick, _, m := newTick(b, q)

	// Wire the awaiting sweep with an error; use 1ns interval so it runs every tick
	pr := &fakePaymentReconciler{err: errDown}
	tick.WithAwaitingPaymentSweep(pr, time.Nanosecond)

	n := tick.Sweep(context.Background())

	// The awaiting failure should not prevent the other sweeps
	if n != 2 {
		t.Fatalf("sweep returned %d, want 2 (lapsed + selecting expired)", n)
	}
	if m.counts[MetricLapsed] != 1 || m.counts[MetricSelectingExpired] != 1 {
		t.Fatalf("expected 1 lapsed and 1 selecting expired: %v", m.counts)
	}
	if m.counts[MetricAwaitingSweepFailed] != 1 {
		t.Fatalf("expected awaiting sweep failure to be counted: %v", m.counts)
	}
	if pr.calls != 1 {
		t.Fatalf("expected payment reconciler to be called despite error: %d", pr.calls)
	}
}

func TestSweep_AwaitingDuplicate_IsCounted(t *testing.T) {
	b := &fakeBookings{}
	q := &fakeQueue{view: domain.EventView{State: "OPEN"}}
	tick, _, m := newTick(b, q)

	pr := &fakePaymentReconciler{result: domain.SweepResult{
		Expired:   2,
		Settled:   1,
		Skipped:   3,
		Duplicate: 4,
	}}
	tick.WithAwaitingPaymentSweep(pr, time.Nanosecond) // 1ns interval so it runs

	tick.Sweep(context.Background())

	if m.counts[MetricAwaitingSweptExpired] != 2 {
		t.Fatalf("expected 2 expired: %v", m.counts)
	}
	if m.counts[MetricAwaitingSweptSettled] != 1 {
		t.Fatalf("expected 1 settled: %v", m.counts)
	}
	if m.counts[MetricAwaitingSweepSkipped] != 3 {
		t.Fatalf("expected 3 skipped: %v", m.counts)
	}
	if m.counts[MetricAwaitingSweepDuplicate] != 4 {
		t.Fatalf("expected 4 duplicate: %v", m.counts)
	}
}
