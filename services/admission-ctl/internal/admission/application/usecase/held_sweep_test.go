package usecase

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

type fakeHeld struct {
	due       []domain.EndedHold
	err       error
	calls     int
	order     *[]string // shared log of calls across fakes
	ctxErr    error     // ctx.Err() as ExpireHeldDue saw it
	afterCall func()    // runs once the "commit" is done (a test cancels the parent context here)
}

func (f *fakeHeld) ExpireHeldDue(ctx context.Context, _ time.Time) ([]domain.EndedHold, error) {
	f.calls++
	f.ctxErr = ctx.Err()
	if f.afterCall != nil {
		defer f.afterCall()
	}
	if f.order != nil {
		*f.order = append(*f.order, "ExpireHeldDue")
	}
	return f.due, f.err
}

type fakeHolds struct {
	ctxErrs    []error // ctx.Err() of every Release
	released   []domain.EndedHold
	missing    map[string]int // booking id -> stock keys that were gone
	failFor    map[string]error
	order      *[]string
	releaseErr error
}

func (f *fakeHolds) Release(ctx context.Context, h domain.EndedHold) (int, error) {
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	f.released = append(f.released, h)
	if f.order != nil {
		*f.order = append(*f.order, "Release:"+h.BookingID)
	}
	if err := f.failFor[h.BookingID]; err != nil {
		return 0, err
	}
	return f.missing[h.BookingID], f.releaseErr
}

func hold(id, gt string, seats []domain.SeatCount, niks ...string) domain.EndedHold {
	return domain.EndedHold{Ended: domain.Ended{BookingID: id, EventID: "E1", QueueToken: "qt-" + id, GrantToken: gt}, Seats: seats, NIKs: niks}
}

var oneVIP = []domain.SeatCount{{CategoryID: "VIP", Qty: 1}}

func heldTick(b *fakeBookings, q *fakeQueue, h *fakeHeld, hs *fakeHolds) (*Tick, *metrics) {
	tick, _, m := newTick(b, q)
	return tick.WithHeldSweep(h, hs), m
}

func TestSweepExpiresDueHeldBookingsGivesTheirHoldBackAndKillsTheirGrants(t *testing.T) {
	h := &fakeHeld{due: []domain.EndedHold{
		hold("B1", "G1", []domain.SeatCount{{CategoryID: "VIP", Qty: 2}, {CategoryID: "FEST", Qty: 1}}, "n1", "n2", "n3"),
		hold("B2", "G2", oneVIP, "n4"),
	}}
	hs, q := &fakeHolds{}, &fakeQueue{}
	tick, m := heldTick(&fakeBookings{}, q, h, hs)
	if n := tick.Sweep(context.Background()); n != 2 {
		t.Fatalf("swept %d, want the 2 held bookings", n)
	}
	if !reflect.DeepEqual(hs.released, h.due) {
		t.Fatalf("released %+v, want each booking's own seats and NIKs", hs.released)
	}
	if !reflect.DeepEqual(q.dropped, []string{"G1", "G2"}) || m.counts[MetricHeldExpired] != 2 {
		t.Fatalf("dropped %v metrics %v", q.dropped, m.counts)
	}
	for _, op := range q.ops {
		if op != "DropGrant" {
			t.Fatalf("sweep also called %s: token and acct stay until the sale window ends", op)
		}
	}
}

// The database commit has already happened when ExpireHeldDue returns: the stock is released after it, never before.
func TestTheHoldIsGivenBackOnlyAfterTheDatabaseCommit(t *testing.T) {
	var order []string
	h := &fakeHeld{due: []domain.EndedHold{hold("B1", "G1", oneVIP, "n1")}, order: &order}
	hs := &fakeHolds{order: &order}
	tick, _ := heldTick(&fakeBookings{}, &fakeQueue{}, h, hs)
	tick.Sweep(context.Background())
	if !reflect.DeepEqual(order, []string{"ExpireHeldDue", "Release:B1"}) {
		t.Fatalf("order = %v", order)
	}
}

// A release that fails leaves stock and NIK keys held without a booking until reconciliation (B5): counted, never fatal,
// and the other bookings and the grant are still handled.
func TestAFailedReleaseIsCountedAndTheRestOfTheSweepContinues(t *testing.T) {
	h := &fakeHeld{due: []domain.EndedHold{hold("B1", "G1", oneVIP, "n1"), hold("B2", "G2", oneVIP, "n2")}}
	hs := &fakeHolds{failFor: map[string]error{"B1": errDown}}
	q := &fakeQueue{}
	tick, m := heldTick(&fakeBookings{}, q, h, hs)
	if n := tick.Sweep(context.Background()); n != 2 {
		t.Fatalf("swept %d, want both (the rows are already EXPIRED)", n)
	}
	if m.counts[MetricReleaseFailed] != 1 || len(hs.released) != 2 || !reflect.DeepEqual(q.dropped, []string{"G1", "G2"}) {
		t.Fatalf("metrics %v released %d dropped %v", m.counts, len(hs.released), q.dropped)
	}
}

func TestAStockKeyThatWasGoneIsCountedPerKey(t *testing.T) {
	h := &fakeHeld{due: []domain.EndedHold{hold("B1", "G1", oneVIP, "n1")}}
	hs := &fakeHolds{missing: map[string]int{"B1": 2}}
	tick, m := heldTick(&fakeBookings{}, &fakeQueue{}, h, hs)
	tick.Sweep(context.Background())
	if m.counts[MetricReleaseStockMissing] != 2 || m.counts[MetricReleaseFailed] != 0 {
		t.Fatalf("metrics %v", m.counts)
	}
}

func TestABookingWithoutSeatsHasNothingToReleaseButStillLosesItsGrant(t *testing.T) {
	h := &fakeHeld{due: []domain.EndedHold{hold("B1", "G1", nil), hold("B2", "", oneVIP, "n2")}}
	hs, q := &fakeHolds{}, &fakeQueue{}
	tick, _ := heldTick(&fakeBookings{}, q, h, hs)
	tick.Sweep(context.Background())
	if len(hs.released) != 1 || hs.released[0].BookingID != "B2" {
		t.Fatalf("released %+v, want only the booking that holds seats", hs.released)
	}
	if !reflect.DeepEqual(q.dropped, []string{"G1"}) {
		t.Fatalf("dropped %v, want the grant of B1 only (B2 has none)", q.dropped)
	}
}

func TestAFailingHeldQueryIsCountedAndDoesNotStopTheOtherSweeps(t *testing.T) {
	b := &fakeBookings{lapsed: []domain.Ended{{BookingID: "B0", GrantToken: "G0"}}}
	q := &fakeQueue{}
	tick, m := heldTick(b, q, &fakeHeld{err: errDown}, &fakeHolds{})
	if n := tick.Sweep(context.Background()); n != 1 || m.counts[MetricSweepFailures] != 1 || !reflect.DeepEqual(q.dropped, []string{"G0"}) {
		t.Fatalf("n=%d dropped %v metrics %v", n, q.dropped, m.counts)
	}
}

func TestAFailingLapseDoesNotStopTheHeldSweep(t *testing.T) {
	h := &fakeHeld{due: []domain.EndedHold{hold("B1", "G1", oneVIP, "n1")}}
	tick, m := heldTick(&fakeBookings{lapseErr: errDown}, &fakeQueue{}, h, &fakeHolds{})
	if n := tick.Sweep(context.Background()); n != 1 || m.counts[MetricSweepFailures] != 1 || h.calls != 1 {
		t.Fatalf("n=%d metrics %v held calls %d", n, m.counts, h.calls)
	}
}

// Without the HELD sweep wired (the Gherkin harness, an older worker) Sweep behaves exactly as before.
func TestWithoutTheHeldSweepWiredSweepOnlyDoesTheTwoBookingDeadlines(t *testing.T) {
	b := &fakeBookings{lapsed: []domain.Ended{{BookingID: "B1", GrantToken: "G1"}}}
	tick, _, _ := newTick(b, &fakeQueue{})
	if n := tick.Sweep(context.Background()); n != 1 {
		t.Fatalf("n = %d", n)
	}
}

func TestWithHeldSweepRefusesNilPorts(t *testing.T) {
	tick, _, _ := newTick(&fakeBookings{}, &fakeQueue{})
	defer func() {
		if recover() == nil {
			t.Fatal("a half-wired HELD sweep must fail at start, not on the first expired hold")
		}
	}()
	tick.WithHeldSweep(&fakeHeld{}, nil)
}

func TestRunSweepsHeldBookingsToo(t *testing.T) {
	h := &fakeHeld{}
	tick, _ := heldTick(&fakeBookings{}, &fakeQueue{view: domain.EventView{State: "OPEN"}}, h, &fakeHolds{})
	tick.Run(context.Background())
	if h.calls != 1 {
		t.Fatalf("the HELD sweep ran %d times in one tick", h.calls)
	}
}

// A graceful shutdown (SIGTERM in a rolling deploy) cancels the controller's context. Once the transaction has committed
// the bookings are EXPIRED and nothing will retry them: the release must still happen, or their stock and NIK locks leak
// until reconciliation.
func TestAShutdownDuringTheSweepStillGivesEveryCommittedHoldBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &fakeHeld{due: []domain.EndedHold{hold("B1", "G1", oneVIP, "n1"), hold("B2", "G2", oneVIP, "n2")}, afterCall: cancel}
	hs, q := &fakeHolds{}, &fakeQueue{}
	tick, m := heldTick(&fakeBookings{}, q, h, hs)
	if n := tick.Sweep(ctx); n != 2 {
		t.Fatalf("swept %d, want 2", n)
	}
	if len(hs.released) != 2 || m.counts[MetricReleaseFailed] != 0 {
		t.Fatalf("released %d, metrics %v, want both holds given back although the parent context was cancelled", len(hs.released), m.counts)
	}
	for i, err := range hs.ctxErrs {
		if err != nil {
			t.Fatalf("release %d ran on a cancelled context: %v", i, err)
		}
	}
}

func TestTheHeldSweepRunsOnAContextTheShutdownCannotCancelAndHasItsOwnDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the controller is already shutting down when the tick starts
	h := &fakeHeld{}
	probe := &deadlineHeld{fakeHeld: h}
	tick, _ := heldTick(&fakeBookings{}, &fakeQueue{}, h, &fakeHolds{})
	tick.held = probe
	tick.Sweep(ctx)
	if h.ctxErr != nil || !probe.hadDeadline {
		t.Fatalf("ctx error %v, deadline %v: the HELD sweep must not be cancelled by the shutdown, and must be bounded", h.ctxErr, probe.hadDeadline)
	}
}

type deadlineHeld struct {
	*fakeHeld
	hadDeadline bool
}

func (d *deadlineHeld) ExpireHeldDue(ctx context.Context, now time.Time) ([]domain.EndedHold, error) {
	_, d.hadDeadline = ctx.Deadline()
	return d.fakeHeld.ExpireHeldDue(ctx, now)
}

// A booking whose claims exist but whose seats could not be read cannot be released (the script needs the categories):
// the NIK locks would stay until their TTL, so it is counted, never skipped silently.
func TestNIKLocksWithoutSeatsAreCountedAsAFailedRelease(t *testing.T) {
	h := &fakeHeld{due: []domain.EndedHold{hold("B1", "G1", nil, "n1")}}
	hs, q := &fakeHolds{}, &fakeQueue{}
	tick, m := heldTick(&fakeBookings{}, q, h, hs)
	tick.Sweep(context.Background())
	if len(hs.released) != 0 || m.counts[MetricReleaseFailed] != 1 || !reflect.DeepEqual(q.dropped, []string{"G1"}) {
		t.Fatalf("released %d metrics %v dropped %v, want ReleaseFailed counted and the grant still dropped", len(hs.released), m.counts, q.dropped)
	}
}
