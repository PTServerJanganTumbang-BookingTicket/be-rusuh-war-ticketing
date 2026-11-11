package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// reconcileTimeout bounds one event's reconciliation so it cannot stretch a tick for long: a handful of Aurora and Redis
// reads, a few milliseconds each.
const reconcileTimeout = 2 * time.Second

// orphanEvery is how often an event's orphan NIK locks are counted: a SCAN walks a whole node's keyspace (tokens included),
// so it runs on the first run of an event and then once in this many runs (about every 10 minutes), not every minute.
const orphanEvery = 10

// ReconcileConfig is the tuning of stock reconciliation (docs/05-booking/04-stock-reconciliation.md §6).
type ReconcileConfig struct {
	Every         time.Duration // minimum time between two runs for one event; 0 turns the job off
	Threshold     int64         // a drift below this is left alone
	MaxCorrection int64         // a larger drift is alarmed, not corrected: a changed capacity, a flush or a bug
	LockTTL       time.Duration // TTL of a restored NIK lock: the sale window, as booking sets it
}

// DefaultReconcileConfig is every 60 s, threshold 1, at most 20 seats, a 6 h lock.
func DefaultReconcileConfig() ReconcileConfig {
	return ReconcileConfig{Every: 60 * time.Second, Threshold: 1, MaxCorrection: 20, LockTTL: 6 * time.Hour}
}

// seen is what one run saw of a category: its drift and the Redis value it was measured on.
type seen struct{ drift, level int64 }

// Reconciler repairs Redis from Aurora, which is the truth: the stock counters and the NIK locks. It never guesses.
//
// A shortfall in Redis is also what a hold in flight looks like (Redis is lowered before Aurora commits), and during a flash
// sale holds are in flight all the time, so no number of readings can tell the two apart. What can: a hold in flight MOVES
// the Redis counter. Seats are therefore given back only if the counter held the same value on two ticks in a row and on both
// sides of a fresh Aurora read, and then only by the smallest shortfall seen. Under steady load a real leak waits until the
// sale goes quiet. Every choice prefers undersell to oversell.
//
// Its history lives in memory like the rest of the controller's caches: a restart only delays a correction by a run.
type Reconciler struct {
	cfg     ReconcileConfig
	src     outbound.ReconcileBookings
	repair  outbound.StockRepair
	metrics outbound.Metrics

	last     map[string]time.Time           // event -> start of its last run
	runs     map[string]int                 // event -> number of runs, to space the orphan scan
	drift    map[string]map[string]seen     // event -> category -> what the previous run saw
	lockMiss map[string]map[string]struct{} // event -> NIK hashes whose lock was missing on the previous run
}

// NewReconciler builds the job.
func NewReconciler(cfg ReconcileConfig, src outbound.ReconcileBookings, repair outbound.StockRepair, m outbound.Metrics) *Reconciler {
	return &Reconciler{cfg: cfg, src: src, repair: repair, metrics: m,
		last: map[string]time.Time{}, runs: map[string]int{}, drift: map[string]map[string]seen{}, lockMiss: map[string]map[string]struct{}{}}
}

// WithReconcile turns on stock reconciliation: one open event per tick, no more often than cfg.Every.
func (t *Tick) WithReconcile(r *Reconciler) *Tick {
	t.recon = r
	return t
}

// reconcileOne runs the next event that is due, if any: one per tick, so a tick never carries more than one event's
// repair and stays near its budget. Only an OPEN or FULL_BOOKED sale is reconciled: a missing key before the sale
// (SCHEDULED) is a seeding mistake to see, not to hide, and a CLOSED sale has nothing to repair.
func (t *Tick) reconcileOne(ctx context.Context, events []domain.Event) {
	r := t.recon
	if r == nil || r.cfg.Every <= 0 {
		return
	}
	r.forget(events)
	now := t.clock.Now()
	for _, ev := range events {
		if last, ok := r.last[ev.ID]; ok && now.Sub(last) < r.cfg.Every {
			continue
		}
		r.last[ev.ID] = now // marked first: an event that is skipped or fails waits for the next interval
		view, err := t.queue.View(ctx, ev.ID)
		if err != nil || (view.State != "OPEN" && view.State != "FULL_BOOKED") {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
		r.Run(rctx, ev.ID)
		cancel()
		return
	}
}

// forget drops what is remembered about events that are no longer ticked, so the history of a finished sale does not stay
// for the life of the process.
func (r *Reconciler) forget(active []domain.Event) {
	keep := make(map[string]struct{}, len(active))
	for _, ev := range active {
		keep[ev.ID] = struct{}{}
	}
	for id := range r.last {
		if _, ok := keep[id]; !ok {
			delete(r.last, id)
			delete(r.runs, id)
			delete(r.drift, id)
			delete(r.lockMiss, id)
		}
	}
}

// Run reconciles one event: its stock counters, then its NIK locks. A failing step is counted and skipped.
func (r *Reconciler) Run(ctx context.Context, eventID string) {
	r.stock(ctx, eventID)
	r.locks(ctx, eventID)
}

func (r *Reconciler) fail(step, eventID string, err error) {
	r.metrics.Inc(MetricReconcileFailures)
	slog.Warn("reconcile failed", slog.String("step", step), slog.String("event", eventID), slog.Any("err", err))
}

func (r *Reconciler) stock(ctx context.Context, eventID string) {
	exps, err := r.src.StockExpectations(ctx, eventID)
	if err != nil {
		r.fail("read expected stock", eventID, err)
		return
	}
	if len(exps) == 0 {
		return
	}
	cats := make([]string, len(exps))
	for i, e := range exps {
		cats[i] = e.CategoryID
	}
	levels, missing, err := r.repair.Levels(ctx, eventID, cats)
	if err != nil {
		r.fail("read stock", eventID, err)
		return
	}
	gone := make(map[string]bool, len(missing))
	for _, c := range missing {
		gone[c] = true
	}
	hist := r.history(eventID)
	var worst int64
	for _, e := range exps {
		if gone[e.CategoryID] {
			r.restoreKey(ctx, eventID, e, hist)
			continue
		}
		d := levels[e.CategoryID] - e.Expected()
		worst = max(worst, abs(d))
		switch {
		case abs(d) < r.cfg.Threshold:
			delete(hist, e.CategoryID)
		case abs(d) > r.cfg.MaxCorrection:
			r.metrics.Inc(MetricStockDriftTooLarge)
			slog.Error("stock drift is too large to correct automatically", slog.String("event", eventID),
				slog.String("category", e.CategoryID), slog.Int64("drift", d))
			delete(hist, e.CategoryID)
		default:
			r.settle(ctx, eventID, e, levels[e.CategoryID], d, hist)
		}
	}
	r.metrics.Gauge(GaugeStockDrift, float64(worst))
}

// restoreKey puts a lost (or unreadable) stock key back at once. It is not drift: it refuses every hold of the category
// until it is back. The one risk is a hold in flight at the moment the key was lost; it is accepted and written down in
// the plan (§8), because waiting would leave the whole category dead.
func (r *Reconciler) restoreKey(ctx context.Context, eventID string, e domain.StockExpectation, hist map[string]seen) {
	delete(hist, e.CategoryID)
	restored, err := r.repair.Restore(ctx, eventID, e.CategoryID, e.Expected())
	if err != nil {
		r.fail("restore stock key", eventID, err)
		return
	}
	if restored {
		r.metrics.Inc(MetricStockKeyRestored)
		slog.Warn("stock key restored from Aurora", slog.String("event", eventID), slog.String("category", e.CategoryID),
			slog.Int64("value", e.Expected()))
	}
}

// settle decides what to do with a drift that is large enough to matter and small enough to correct. d is signed:
// negative is seats missing in Redis, positive is too many. observed is the Redis value d was measured on.
func (r *Reconciler) settle(ctx context.Context, eventID string, e domain.StockExpectation, observed, d int64, hist map[string]seen) {
	prev, ok := hist[e.CategoryID]
	if !ok || (prev.drift < 0) != (d < 0) {
		hist[e.CategoryID] = seen{d, observed} // a first sighting, or the other direction: start over
		return
	}
	smallest := smaller(prev.drift, d)
	if d < 0 {
		// Missing seats are what a hold in flight looks like, and holds are in flight all the time during a sale: giving
		// them back would oversell. A hold in flight moves the counter, so the counter must not have moved at all, on the
		// previous tick and on both sides of a fresh Aurora read.
		if prev.level != observed {
			hist[e.CategoryID] = seen{d, observed}
			return
		}
		d2, status := r.confirm(ctx, eventID, e.CategoryID, observed)
		switch status {
		case confirmFailed:
			return // counted; the history stays, the next run asks again
		case confirmMoved:
			delete(hist, e.CategoryID) // holds were in flight: start over
			return
		}
		if d2 >= 0 || -d2 < r.cfg.Threshold {
			delete(hist, e.CategoryID) // it resolved by itself
			return
		}
		smallest = smaller(smallest, d2)
	}
	delete(hist, e.CategoryID)
	res, err := r.repair.Adjust(ctx, eventID, e.CategoryID, observed, -smallest)
	if err != nil {
		r.fail("adjust stock", eventID, err)
		return
	}
	switch res.Code {
	case domain.AdjustAdjusted:
		r.metrics.Inc(MetricStockAdjusted)
		slog.Warn("stock corrected from Aurora", slog.String("event", eventID), slog.String("category", e.CategoryID),
			slog.Int64("delta", -smallest), slog.Int64("now", res.Value))
	case domain.AdjustMissing:
		r.restoreKey(ctx, eventID, e, hist)
	default: // CHANGED, BELOW_ZERO: harmless, the next run decides
		r.metrics.Inc(MetricStockAdjustRefused)
	}
}

type confirmStatus int

const (
	confirmOK     confirmStatus = iota // the counter held still on both sides of the Aurora read: the drift is real
	confirmMoved                       // the counter moved (or the key vanished): a hold was in flight
	confirmFailed                      // a read failed (counted)
)

// confirm reads Redis, then Aurora, then Redis again for one category. The drift counts only if the counter still holds the
// value the previous ticks saw on both sides of the Aurora read: a hold that landed while Aurora was being read would
// otherwise look like a leak.
func (r *Reconciler) confirm(ctx context.Context, eventID, category string, observed int64) (int64, confirmStatus) {
	if status := r.stillAt(ctx, eventID, category, observed); status != confirmOK {
		return 0, status
	}
	exps, err := r.src.StockExpectations(ctx, eventID)
	if err != nil {
		r.fail("confirm expected stock", eventID, err)
		return 0, confirmFailed
	}
	var exp *domain.StockExpectation
	for i := range exps {
		if exps[i].CategoryID == category {
			exp = &exps[i]
		}
	}
	if exp == nil {
		return 0, confirmMoved
	}
	if status := r.stillAt(ctx, eventID, category, observed); status != confirmOK {
		return 0, status
	}
	return observed - exp.Expected(), confirmOK
}

// stillAt reads one category's counter and reports whether it still equals want.
func (r *Reconciler) stillAt(ctx context.Context, eventID, category string, want int64) confirmStatus {
	levels, missing, err := r.repair.Levels(ctx, eventID, []string{category})
	if err != nil {
		r.fail("confirm stock", eventID, err)
		return confirmFailed
	}
	if len(missing) > 0 || levels[category] != want {
		return confirmMoved
	}
	return confirmOK
}

// locks restores NIK locks in one direction only: a claim in Aurora with no lock in Redis, missing on two runs in a row.
// Two, because the HELD sweep commits (the claim is gone) before it deletes the Redis key, so a single reading could bring
// back the lock of a booking that has just expired and block that NIK for the whole sale. A lock with no claim is never
// deleted (it may belong to a hold that has not committed): it dies on its own TTL and is counted as an orphan.
func (r *Reconciler) locks(ctx context.Context, eventID string) {
	claims, err := r.src.ClaimedNIKs(ctx, eventID)
	if err != nil {
		r.fail("read NIK claims", eventID, err)
		return
	}
	claimed := make(map[string]struct{}, len(claims))
	for _, h := range claims {
		claimed[h] = struct{}{}
	}
	var missing []string
	if len(claims) > 0 {
		if missing, err = r.repair.MissingLocks(ctx, eventID, claims); err != nil {
			r.fail("read NIK locks", eventID, err)
			return
		}
	}
	prev := r.lockMiss[eventID]
	next := make(map[string]struct{}, len(missing))
	for _, h := range missing {
		if _, again := prev[h]; !again {
			next[h] = struct{}{}
			continue
		}
		restored, err := r.repair.RestoreLock(ctx, eventID, h, r.cfg.LockTTL)
		if err != nil {
			r.fail("restore NIK lock", eventID, err)
			next[h] = struct{}{}
			continue
		}
		if restored {
			r.metrics.Inc(MetricNikClaimRestored)
		}
	}
	r.lockMiss[eventID] = next

	r.runs[eventID]++
	if r.runs[eventID]%orphanEvery != 1 {
		return // the orphan scan is expensive: the first run of the event, then every orphanEvery runs
	}
	orphans, err := r.repair.CountOrphans(ctx, eventID, claimed)
	if err != nil {
		r.fail("count orphan NIK locks", eventID, err)
		return
	}
	r.metrics.Gauge(GaugeNikOrphans, float64(orphans))
}

func (r *Reconciler) history(eventID string) map[string]seen {
	h := r.drift[eventID]
	if h == nil {
		h = map[string]seen{}
		r.drift[eventID] = h
	}
	return h
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// smaller returns the drift of the smaller magnitude.
func smaller(a, b int64) int64 {
	if abs(a) <= abs(b) {
		return a
	}
	return b
}
