package usecase

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// ---- fakes

type fakeRecBookings struct {
	seq      [][]domain.StockExpectation // answer of the n-th StockExpectations call; the last one repeats
	err      error
	calls    int
	claims   []string
	claimErr error
}

func (f *fakeRecBookings) StockExpectations(context.Context, string) ([]domain.StockExpectation, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	i := min(f.calls-1, len(f.seq)-1)
	return f.seq[i], nil
}

func (f *fakeRecBookings) ClaimedNIKs(context.Context, string) ([]string, error) {
	return f.claims, f.claimErr
}

type adjustCall struct {
	cat             string
	observed, delta int64
}

type fakeRepair struct {
	levelSeq    []map[string]int64 // answer of the n-th Levels call; the last one repeats
	missingCats []string
	levelsErr   error
	levelCalls  int

	restored        map[string]int64
	restoreNoop     bool // Restore finds the key already back
	adjusts         []adjustCall
	adjustResult    domain.AdjustResult
	adjustErr       error
	missingLocks    [][]string // answer of the n-th MissingLocks call; the last one repeats
	lockCalls       int
	locksErr        error
	restoredLocks   []string
	lockTTL         time.Duration
	orphans         int
	orphansErr      error
	orphanClaimed   map[string]struct{}
	orphanCallCount int
}

func (f *fakeRepair) Levels(_ context.Context, _ string, cats []string) (map[string]int64, []string, error) {
	f.levelCalls++
	if f.levelsErr != nil {
		return nil, nil, f.levelsErr
	}
	i := min(f.levelCalls-1, len(f.levelSeq)-1)
	out := map[string]int64{}
	for k, v := range f.levelSeq[i] {
		out[k] = v
	}
	return out, f.missingCats, nil
}

func (f *fakeRepair) Restore(_ context.Context, _, cat string, v int64) (bool, error) {
	if f.restored == nil {
		f.restored = map[string]int64{}
	}
	if f.restoreNoop {
		return false, nil
	}
	f.restored[cat] = v
	return true, nil
}

func (f *fakeRepair) Adjust(_ context.Context, _, cat string, observed, delta int64) (domain.AdjustResult, error) {
	f.adjusts = append(f.adjusts, adjustCall{cat, observed, delta})
	if f.adjustErr != nil {
		return domain.AdjustResult{}, f.adjustErr
	}
	if f.adjustResult.Code == "" {
		return domain.AdjustResult{Code: domain.AdjustAdjusted, Value: observed + delta}, nil
	}
	return f.adjustResult, nil
}

func (f *fakeRepair) MissingLocks(_ context.Context, _ string, hmacs []string) ([]string, error) {
	f.lockCalls++
	if f.locksErr != nil {
		return nil, f.locksErr
	}
	if len(f.missingLocks) == 0 {
		return nil, nil
	}
	i := min(f.lockCalls-1, len(f.missingLocks)-1)
	var out []string
	for _, h := range f.missingLocks[i] { // only those that are claimed
		for _, c := range hmacs {
			if c == h {
				out = append(out, h)
			}
		}
	}
	return out, nil
}

func (f *fakeRepair) RestoreLock(_ context.Context, _, hmac string, ttl time.Duration) (bool, error) {
	f.restoredLocks = append(f.restoredLocks, hmac)
	f.lockTTL = ttl
	return true, nil
}

func (f *fakeRepair) CountOrphans(_ context.Context, _ string, claimed map[string]struct{}) (int, error) {
	f.orphanCallCount++
	f.orphanClaimed = claimed
	return f.orphans, f.orphansErr
}

// ---- rig

type recRig struct {
	b *fakeRecBookings
	r *fakeRepair
	m *metrics
	c *Reconciler
}

// vip: capacity 10, 5 seats taken, so Redis should hold 5.
func newRecRig(redisVIP int64) *recRig {
	rig := &recRig{
		b: &fakeRecBookings{seq: [][]domain.StockExpectation{{{CategoryID: "VIP", Capacity: 10, Taken: 5}}}},
		r: &fakeRepair{levelSeq: []map[string]int64{{"VIP": redisVIP}}},
		m: newMetrics(),
	}
	rig.c = NewReconciler(DefaultReconcileConfig(), rig.b, rig.r, rig.m)
	return rig
}

func (g *recRig) run() { g.c.Run(context.Background(), "E1") }

// ---- expected stock

func TestExpectedStockIsCapacityMinusTheSeatsTakenAndNeverNegative(t *testing.T) {
	if got := (domain.StockExpectation{Capacity: 10, Taken: 3}).Expected(); got != 7 {
		t.Fatalf("expected = %d, want 7", got)
	}
	if got := (domain.StockExpectation{Capacity: 10, Taken: 12}).Expected(); got != 0 {
		t.Fatalf("expected = %d, want 0: never drive a counter below zero", got)
	}
}

// ---- a missing key

func TestAMissingStockKeyIsRestoredAtOnceToTheExpectedCount(t *testing.T) {
	g := newRecRig(0)
	g.r.missingCats = []string{"VIP"}
	g.run()
	if g.r.restored["VIP"] != 5 || g.m.counts[MetricStockKeyRestored] != 1 || len(g.r.adjusts) != 0 {
		t.Fatalf("restored %v adjusts %v metrics %v, want VIP restored to 5 with no adjust", g.r.restored, g.r.adjusts, g.m.counts)
	}
}

func TestAKeyThatCameBackBeforeTheRestoreIsNotCountedAsRestored(t *testing.T) {
	g := newRecRig(0)
	g.r.missingCats = []string{"VIP"}
	g.r.restoreNoop = true
	g.run()
	if g.m.counts[MetricStockKeyRestored] != 0 {
		t.Fatalf("metrics %v", g.m.counts)
	}
}

// admission-ctl discovers categories from the stock keys that exist: a lost key's category is only known from Aurora.
func TestTheCategoryListComesFromAuroraSoALostKeyOfAnUnlistedCategoryIsRestored(t *testing.T) {
	g := newRecRig(5)
	g.b.seq = [][]domain.StockExpectation{{{CategoryID: "VIP", Capacity: 10, Taken: 5}, {CategoryID: "FEST", Capacity: 20, Taken: 2}}}
	g.r.levelSeq = []map[string]int64{{"VIP": 5}}
	g.r.missingCats = []string{"FEST"}
	g.run()
	if g.r.restored["FEST"] != 18 {
		t.Fatalf("restored %v, want FEST restored to 18", g.r.restored)
	}
}

// ---- drift

func TestNoDriftOrADriftBelowTheThresholdChangesNothing(t *testing.T) {
	g := newRecRig(5) // exactly expected
	g.run()
	g.run()
	if len(g.r.adjusts) != 0 {
		t.Fatalf("adjusts %v", g.r.adjusts)
	}
	g = newRecRig(4) // one short, but the threshold is 2
	cfg := DefaultReconcileConfig()
	cfg.Threshold = 2
	g.c = NewReconciler(cfg, g.b, g.r, g.m)
	g.run()
	g.run()
	if len(g.r.adjusts) != 0 {
		t.Fatalf("a drift below the threshold must be left alone: %v", g.r.adjusts)
	}
}

func TestADriftBeyondTheMaximumIsAlarmedAndNeverCorrected(t *testing.T) {
	g := newRecRig(5 - 21) // 21 seats missing, the maximum is 20
	for i := 0; i < 3; i++ {
		g.run()
	}
	if len(g.r.adjusts) != 0 || g.m.counts[MetricStockDriftTooLarge] != 3 {
		t.Fatalf("adjusts %v metrics %v, want an alarm every run and no correction", g.r.adjusts, g.m.counts)
	}
}

// A shortfall can be a hold in flight (Redis is lowered before Aurora commits), so a single sighting proves nothing.
func TestAShortfallSeenOnceIsNotCorrected(t *testing.T) {
	g := newRecRig(3) // 2 seats short
	g.run()
	if len(g.r.adjusts) != 0 || g.b.calls != 1 {
		t.Fatalf("adjusts %v expectation reads %d, want no correction and no confirm yet", g.r.adjusts, g.b.calls)
	}
}

func TestAShortfallSeenOnTwoTicksIsConfirmedFreshAndThenGivenBack(t *testing.T) {
	g := newRecRig(3)
	g.run()
	g.run()
	if !reflect.DeepEqual(g.r.adjusts, []adjustCall{{"VIP", 3, 2}}) {
		t.Fatalf("adjusts %v, want VIP given back 2 seats, observed 3", g.r.adjusts)
	}
	if g.b.calls != 3 || g.r.levelCalls != 4 {
		t.Fatalf("expectation reads %d, Redis reads %d, want a fresh confirm (Aurora once, Redis before and after it) before the correction", g.b.calls, g.r.levelCalls)
	}
	if g.m.counts[MetricStockAdjusted] != 1 {
		t.Fatalf("metrics %v", g.m.counts)
	}
}

// The seats of a hold in flight come back by themselves: if the fresh read shows no shortfall, nothing is given back.
func TestAShortfallThatIsGoneAtTheFreshConfirmIsNotCorrected(t *testing.T) {
	g := newRecRig(3)
	g.r.levelSeq = []map[string]int64{{"VIP": 3}, {"VIP": 3}, {"VIP": 5}} // the third read is back to normal
	g.run()
	g.run()
	if len(g.r.adjusts) != 0 {
		t.Fatalf("adjusts %v, want none: the shortfall was a hold in flight", g.r.adjusts)
	}
}

// Aurora's side may differ between the readings (a booking committed); the counter did not move, so the smallest shortfall
// of the three readings is what gets given back.
func TestTheCorrectionIsTheSmallestShortfallOfTheThreeReadings(t *testing.T) {
	g := newRecRig(0)
	g.b.seq = [][]domain.StockExpectation{
		{{CategoryID: "VIP", Capacity: 10, Taken: 4}}, // expected 6, Redis 3 -> -3
		{{CategoryID: "VIP", Capacity: 10, Taken: 3}}, // expected 7, Redis 3 -> -4
		{{CategoryID: "VIP", Capacity: 10, Taken: 2}}, // expected 8, Redis 3 -> -5
	}
	g.r.levelSeq = []map[string]int64{{"VIP": 3}}
	g.run()
	g.run()
	if !reflect.DeepEqual(g.r.adjusts, []adjustCall{{"VIP", 3, 3}}) {
		t.Fatalf("adjusts %v, want the smallest shortfall (3) applied to the observed 3", g.r.adjusts)
	}
}

// The central rule. During a flash sale holds are in flight all the time (rate x hold time, say 15 at once), so readings of
// -15, -14, -15 are the sale, not a leak, and giving 14 seats back would oversell when those holds commit. A hold in flight
// moves the Redis counter; only a counter that did not move at all can be a leak.
func TestAShortfallWhileTheCounterKeepsMovingIsNeverCorrected(t *testing.T) {
	g := newRecRig(0)
	g.b.seq = [][]domain.StockExpectation{{{CategoryID: "VIP", Capacity: 100, Taken: 10}}} // expected 90
	for i := 0; i < 8; i++ {                                                               // one reading per run (the counter alternates for as long as the sale runs)
		g.r.levelSeq = append(g.r.levelSeq, map[string]int64{"VIP": 75 + int64(i%2)})
	}
	for i := 0; i < 8; i++ {
		g.run()
	}
	if len(g.r.adjusts) != 0 {
		t.Fatalf("adjusts %v, want none: the counter moved on every reading, so holds were in flight", g.r.adjusts)
	}
}

func TestACounterThatMovesBetweenTheConfirmReadsIsNotCorrected(t *testing.T) {
	g := newRecRig(0)
	g.r.levelSeq = []map[string]int64{{"VIP": 3}, {"VIP": 3}, {"VIP": 3}, {"VIP": 4}} // before and after the Aurora read differ
	g.run()
	g.run()
	if len(g.r.adjusts) != 0 {
		t.Fatalf("adjusts %v, want none: a hold landed while Aurora was being read", g.r.adjusts)
	}
}

func TestACounterThatMovedBetweenTheTwoTicksStartsOverAndIsCorrectedOnceItHoldsStill(t *testing.T) {
	g := newRecRig(0)
	g.r.levelSeq = []map[string]int64{{"VIP": 3}, {"VIP": 4}, {"VIP": 4}, {"VIP": 4}, {"VIP": 4}} // moved once, then still
	g.run()                                                                                       // -2, first sighting at 3
	g.run()                                                                                       // 4: not the value seen before, start over (-1)
	if len(g.r.adjusts) != 0 {
		t.Fatalf("adjusts %v after the counter moved between ticks", g.r.adjusts)
	}
	g.run() // still 4, and the fresh confirm agrees: now it can be a leak
	if !reflect.DeepEqual(g.r.adjusts, []adjustCall{{"VIP", 4, 1}}) {
		t.Fatalf("adjusts %v, want the one missing seat given back", g.r.adjusts)
	}
}

// Too many seats in Redis cannot come from a sale in flight, so it needs only the two ticks, and it takes seats away.
func TestAnExcessSeenOnTwoTicksIsTakenAwayWithoutAFreshConfirm(t *testing.T) {
	g := newRecRig(8) // 3 too many
	g.run()
	if len(g.r.adjusts) != 0 {
		t.Fatalf("an excess seen once must wait: %v", g.r.adjusts)
	}
	g.run()
	if !reflect.DeepEqual(g.r.adjusts, []adjustCall{{"VIP", 8, -3}}) || g.b.calls != 2 {
		t.Fatalf("adjusts %v expectation reads %d, want -3 on the second tick and no extra confirm", g.r.adjusts, g.b.calls)
	}
}

func TestADriftThatSwitchesDirectionStartsOver(t *testing.T) {
	g := newRecRig(3)
	g.r.levelSeq = []map[string]int64{{"VIP": 3}, {"VIP": 8}, {"VIP": 8}} // -2, then +3, then +3
	g.run()
	g.run()
	if len(g.r.adjusts) != 0 {
		t.Fatalf("a shortfall followed by an excess is not one drift: %v", g.r.adjusts)
	}
	g.run()
	if !reflect.DeepEqual(g.r.adjusts, []adjustCall{{"VIP", 8, -3}}) {
		t.Fatalf("adjusts %v, want the excess corrected once it was seen twice", g.r.adjusts)
	}
}

func TestADriftThatDisappearsForgetsItsHistory(t *testing.T) {
	g := newRecRig(3)
	g.r.levelSeq = []map[string]int64{{"VIP": 3}, {"VIP": 5}, {"VIP": 3}} // short, fine, short again
	g.run()
	g.run()
	g.run()
	if len(g.r.adjusts) != 0 {
		t.Fatalf("adjusts %v, want none: the two shortfalls were not on consecutive ticks", g.r.adjusts)
	}
}

func TestTheAnswersOfTheAdjustScriptAreAllHandled(t *testing.T) {
	cases := []struct {
		name   string
		result domain.AdjustResult
		metric string
	}{
		{"applied", domain.AdjustResult{Code: domain.AdjustAdjusted, Value: 5}, MetricStockAdjusted},
		{"the counter moved", domain.AdjustResult{Code: domain.AdjustChanged, Value: 4}, MetricStockAdjustRefused},
		{"below zero", domain.AdjustResult{Code: domain.AdjustBelowZero, Value: 3}, MetricStockAdjustRefused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newRecRig(8)
			g.r.adjustResult = tc.result
			g.run()
			g.run()
			if g.m.counts[tc.metric] != 1 {
				t.Fatalf("metrics %v, want %s", g.m.counts, tc.metric)
			}
		})
	}
	g := newRecRig(8)
	g.r.adjustResult = domain.AdjustResult{Code: domain.AdjustMissing, Value: -1}
	g.run()
	g.run()
	if g.r.restored["VIP"] != 5 {
		t.Fatalf("restored %v, want the key restored by SET NX when the script finds it gone", g.r.restored)
	}
}

func TestEachCategoryIsJudgedOnItsOwn(t *testing.T) {
	g := newRecRig(0)
	g.b.seq = [][]domain.StockExpectation{{{CategoryID: "VIP", Capacity: 10, Taken: 5}, {CategoryID: "FEST", Capacity: 20, Taken: 0}}}
	g.r.levelSeq = []map[string]int64{{"VIP": 5, "FEST": 25}} // VIP fine, FEST 5 too many
	g.run()
	g.run()
	if !reflect.DeepEqual(g.r.adjusts, []adjustCall{{"FEST", 25, -5}}) {
		t.Fatalf("adjusts %v", g.r.adjusts)
	}
}

func TestTheLargestDriftIsReportedAsAGauge(t *testing.T) {
	g := newRecRig(3)
	g.run()
	if g.m.gauges[GaugeStockDrift] != 2 {
		t.Fatalf("gauges %v, want the largest absolute drift", g.m.gauges)
	}
}

// ---- failures are counted and skipped

func TestFailuresAreCountedAndTheOtherStepsStillRun(t *testing.T) {
	g := newRecRig(5)
	g.b.err = errDown // the Aurora stock read fails
	g.b.claims = []string{"n1"}
	g.run()
	if g.m.counts[MetricReconcileFailures] != 1 || g.r.lockCalls != 1 {
		t.Fatalf("metrics %v lock reads %d, want the failure counted and the NIK step still run", g.m.counts, g.r.lockCalls)
	}

	g = newRecRig(5)
	g.r.levelsErr = errDown
	g.run()
	if g.m.counts[MetricReconcileFailures] != 1 {
		t.Fatalf("a Redis read failure: %v", g.m.counts)
	}

	g = newRecRig(8)
	g.r.adjustErr = errDown
	g.run()
	g.run()
	if g.m.counts[MetricReconcileFailures] != 1 {
		t.Fatalf("an adjust failure: %v", g.m.counts)
	}

	g = newRecRig(5)
	g.b.claimErr = errDown
	g.run()
	if g.m.counts[MetricReconcileFailures] != 1 || g.r.restoredLocks != nil {
		t.Fatalf("a claims read failure: %v", g.m.counts)
	}
}

// ---- NIK locks

func TestANIKLockMissingOnOneTickIsNotRestoredButOnTwoItIs(t *testing.T) {
	g := newRecRig(5)
	g.b.claims = []string{"n1", "n2", "n3"}
	g.r.missingLocks = [][]string{{"n1", "n2"}} // the same two stay missing
	g.run()
	if g.r.restoredLocks != nil {
		t.Fatalf("restored %v after one tick, want none: the HELD sweep deletes the key right after its commit", g.r.restoredLocks)
	}
	g.run()
	sort.Strings(g.r.restoredLocks)
	if !reflect.DeepEqual(g.r.restoredLocks, []string{"n1", "n2"}) || g.m.counts[MetricNikClaimRestored] != 2 {
		t.Fatalf("restored %v metrics %v", g.r.restoredLocks, g.m.counts)
	}
	if g.r.lockTTL != DefaultReconcileConfig().LockTTL {
		t.Fatalf("ttl %v, want the sale window", g.r.lockTTL)
	}
}

// A booking that expired between the ticks has no claim any more: its lock is not brought back.
func TestALockWhoseClaimIsGoneByTheSecondTickIsNotRestored(t *testing.T) {
	g := newRecRig(5)
	g.b.claims = []string{"n1"}
	g.r.missingLocks = [][]string{{"n1"}}
	g.run()
	g.b.claims = nil // the booking expired: claim deleted
	g.run()
	if g.r.restoredLocks != nil {
		t.Fatalf("restored %v, want none for a claim that no longer exists", g.r.restoredLocks)
	}
}

func TestARestoredLockIsNotRestoredAgainAndALockThatCameBackResetsItsHistory(t *testing.T) {
	g := newRecRig(5)
	g.b.claims = []string{"n1"}
	g.r.missingLocks = [][]string{{"n1"}, {"n1"}, {"n1"}, {}, {"n1"}}
	g.run() // missing (1)
	g.run() // missing (2): restored
	g.run() // missing again: counts as a first sighting, the restored key vanished once more
	g.run() // back
	g.run() // missing (1) again after it came back
	if len(g.r.restoredLocks) != 1 {
		t.Fatalf("restored %v, want exactly one restore: consecutive misses are needed every time", g.r.restoredLocks)
	}
}

func TestOrphanLocksAreOnlyCountedNeverDeleted(t *testing.T) {
	g := newRecRig(5)
	g.b.claims = []string{"n1", "n2"}
	g.r.orphans = 7
	g.run()
	if g.m.gauges[GaugeNikOrphans] != 7 || g.r.orphanCallCount != 1 {
		t.Fatalf("gauges %v", g.m.gauges)
	}
	if _, ok := g.r.orphanClaimed["n1"]; !ok || len(g.r.orphanClaimed) != 2 {
		t.Fatalf("orphans were judged against %v, want the event's claims", g.r.orphanClaimed)
	}
	g = newRecRig(5)
	g.r.orphansErr = errDown
	g.run() // the first run of an event scans
	if g.m.counts[MetricReconcileFailures] != 1 {
		t.Fatalf("metrics %v", g.m.counts)
	}
}

// ---- wiring into the tick: which events, how often

func reconTick(t *testing.T, view domain.EventView, events ...domain.Event) (*Tick, *clock, *recRig, *fakeQueue) {
	t.Helper()
	g := newRecRig(5)
	q := &fakeQueue{view: view}
	cfg := testConfig()
	cfg.Events = events
	tick := NewTick(cfg, &fakeBookings{}, q, newClock(), g.m)
	clk := newClock()
	tick.clock = clk
	tick.WithReconcile(g.c)
	return tick, clk, g, q
}

func TestOnlyAnOpenOrFullBookedSaleIsReconciled(t *testing.T) {
	for state, want := range map[string]int{"OPEN": 1, "FULL_BOOKED": 1, "SCHEDULED": 0, "CLOSED": 0} {
		tick, _, g, _ := reconTick(t, domain.EventView{State: state}, domain.Event{ID: "E1", Categories: []string{"VIP"}})
		tick.Run(context.Background())
		if g.b.calls != want {
			t.Errorf("state %s: %d expectation reads, want %d (a missing key before the sale is a seeding mistake to see, not to hide)", state, g.b.calls, want)
		}
	}
}

func TestOneEventIsReconciledPerTickAndNoMoreOftenThanTheInterval(t *testing.T) {
	tick, clk, g, _ := reconTick(t, domain.EventView{State: "OPEN"},
		domain.Event{ID: "E1", Categories: []string{"VIP"}}, domain.Event{ID: "E2", Categories: []string{"VIP"}})
	ctx := context.Background()
	tick.Run(ctx)
	if g.b.calls != 1 {
		t.Fatalf("first tick: %d events reconciled, want one", g.b.calls)
	}
	tick.Run(ctx)
	if g.b.calls != 2 {
		t.Fatalf("second tick: %d, want the other event next", g.b.calls)
	}
	tick.Run(ctx)
	if g.b.calls != 2 {
		t.Fatalf("third tick right away: %d, want none (both ran within the interval)", g.b.calls)
	}
	clk.t = clk.t.Add(61 * time.Second)
	tick.Run(ctx)
	if g.b.calls != 3 {
		t.Fatalf("after the interval: %d, want a run again", g.b.calls)
	}
}

func TestAZeroIntervalTurnsReconciliationOff(t *testing.T) {
	tick, _, g, _ := reconTick(t, domain.EventView{State: "OPEN"}, domain.Event{ID: "E1", Categories: []string{"VIP"}})
	cfg := DefaultReconcileConfig()
	cfg.Every = 0
	tick.WithReconcile(NewReconciler(cfg, g.b, g.r, g.m))
	tick.Run(context.Background())
	if g.b.calls != 0 {
		t.Fatalf("%d reads, want none", g.b.calls)
	}
}

func TestAViewThatCannotBeReadSkipsTheEventForNow(t *testing.T) {
	tick, _, g, q := reconTick(t, domain.EventView{State: "OPEN"}, domain.Event{ID: "E1", Categories: []string{"VIP"}})
	q.viewErr = errors.New("redis down")
	tick.Run(context.Background())
	if g.b.calls != 0 {
		t.Fatalf("%d reads, want none when the sale state is unknown", g.b.calls)
	}
}

func TestWithoutReconcileWiredTheTickIsUnchanged(t *testing.T) {
	tick, _, _ := newTick(&fakeBookings{}, &fakeQueue{view: domain.EventView{State: "OPEN"}})
	tick.Run(context.Background()) // must simply not reconcile
}

// ---- the orphan scan is expensive, and state of ended events is dropped

func TestTheOrphanScanRunsOnlyEveryTenthRunOfAnEvent(t *testing.T) {
	g := newRecRig(5)
	for i := 0; i < 10; i++ {
		g.run()
	}
	if g.r.orphanCallCount != 1 {
		t.Fatalf("%d scans in 10 runs, want 1 (a SCAN walks a whole node's keyspace)", g.r.orphanCallCount)
	}
	g.run()
	if g.r.orphanCallCount != 2 {
		t.Fatalf("%d scans after 11 runs, want 2", g.r.orphanCallCount)
	}
}

func TestTheStateOfAnEventThatNoLongerExistsIsDropped(t *testing.T) {
	tick, clk, g, _ := reconTick(t, domain.EventView{State: "OPEN"},
		domain.Event{ID: "E1", Categories: []string{"VIP"}}, domain.Event{ID: "E2", Categories: []string{"VIP"}})
	ctx := context.Background()
	tick.Run(ctx)
	tick.Run(ctx)
	g.c.drift["E2"] = map[string]seen{"VIP": {drift: 1, level: 6}} // some history for the event that is about to go
	tick.cfg.Events = []domain.Event{{ID: "E1", Categories: []string{"VIP"}}}
	clk.t = clk.t.Add(61 * time.Second)
	tick.Run(ctx)
	if _, ok := g.c.last["E2"]; ok {
		t.Fatal("the last-run time of an ended event is kept for ever")
	}
	if _, ok := g.c.drift["E2"]; ok {
		t.Fatal("the drift history of an ended event is kept for ever")
	}
	if _, ok := g.c.lockMiss["E2"]; ok {
		t.Fatal("the NIK history of an ended event is kept for ever")
	}
}
