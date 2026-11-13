// Package usecase holds the admission tick: who gets in, in what order, and what everyone hears.
package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/idgen"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// Metric and alert names (05-admission-controller.md §5).
const (
	MetricCountFailures          = "CountFailures"
	MetricAdvanceStale           = "AdvanceStale"
	MetricStockUninitialized     = "StockUninitialized"
	MetricEventStateMissing      = "EventStateMissing"
	MetricGrantInsertFailures    = "GrantInsertFailures"
	MetricGrantTokenMissing      = "GrantTokenMissing"
	MetricPendingGrantsRecovery  = "PendingGrantsRecovered"
	AlertPendingGrantsStuck      = "PendingGrantsStuck"
	MetricTickOverrun            = "TickOverrun"
	MetricTickFailures           = "TickFailures"
	MetricSweepFailures          = "SweepFailures"
	MetricEventDiscoveryFailures = "EventDiscoveryFailures"
	MetricLapsed                 = "BookingsLapsed"
	MetricSelectingExpired       = "BookingsSelectingExpired"
	MetricHeldExpired            = "BookingsHeldExpired"
	// MetricReleaseFailed counts a hold that could not be given back after its booking expired (Redis down): the stock
	// and NIK keys stay held without a booking until stock reconciliation (B5) repairs them.
	MetricReleaseFailed = "ReleaseFailed"
	// MetricReleaseStockMissing counts a stock key that was gone when a hold was released (not recreated; B5 restores it).
	MetricReleaseStockMissing = "ReleaseStockMissing"
	// Stock reconciliation (docs/05-booking/04-stock-reconciliation.md).
	MetricStockKeyRestored   = "StockKeyRestored"
	MetricStockAdjusted      = "StockAdjusted"
	MetricStockAdjustRefused = "StockAdjustRefused"
	MetricStockDriftTooLarge = "StockDriftTooLarge"
	MetricNikClaimRestored   = "NikClaimRestored"
	MetricReconcileFailures  = "ReconcileFailures"
	GaugeStockDrift          = "StockDrift" // the largest absolute drift of the last reconciliation run
	GaugeNikOrphans          = "NikOrphans" // nik:used keys with no nik_claims row (never deleted)
	// Backstop sweep for AWAITING_PAYMENT bookings (awaiting-payment-sweep-plan.md).
	MetricAwaitingSweptExpired   = "AwaitingSweptExpired"
	MetricAwaitingSweptSettled   = "AwaitingSweptSettled" // non-zero means the webhook is broken
	MetricAwaitingSweepSkipped   = "AwaitingSweepSkipped"
	MetricAwaitingSweepDuplicate = "AwaitingSweepDuplicate"
	MetricAwaitingSweepFailed    = "AwaitingSweepFailed"

	// Per event (docs/metrics.md §5). The gauges are written at most once per Heartbeat per event.
	MetricGranted   = "Granted" // a new GRANTED booking: the customer is let in
	GaugeQueueDepth = "QueueDepth"
	GaugeNowServing = "NowServing"
	GaugeIssued     = "Issued"
	GaugeRatePerMin = "RatePerMin"
	// GaugeTickDuration is the slowest whole tick (every event) of the last Heartbeat; "Seconds" sets the EMF unit.
	GaugeTickDuration = "TickDurationSeconds"
)

// Config holds the tunables of the tick (config.go in cmd/worker reads them from the environment).
type Config struct {
	Interval        time.Duration // 500 ms
	MaxSlots        int64         // MAX_CONCURRENT_SLOTS
	BookingTTL      time.Duration // 5 min to start choosing seats
	GrantTTL        time.Duration // GRANT_TOKEN_TTL_SECONDS: the grant must outlive the payment
	Heartbeat       time.Duration // 10 s
	CloseAfter      time.Duration // 930 s fully booked closes the sale
	EtaSeedMinutes  int64         // ETA_SEED_SLOT_MINUTES
	InsertAttempts  int           // 3
	StuckAfterTicks int           // PendingGrantsStuck is raised on the tick after this many
	Events          []domain.Event
}

// DefaultConfig is the production configuration (docs/04-queue/05-admission-controller.md).
func DefaultConfig() Config {
	return Config{
		Interval: 500 * time.Millisecond, MaxSlots: 100, BookingTTL: 5 * time.Minute,
		GrantTTL: 1080 * time.Second, Heartbeat: 10 * time.Second, CloseAfter: 930 * time.Second,
		EtaSeedMinutes: 5, InsertAttempts: 3, StuckAfterTicks: 10,
	}
}

// Tick lets customers in. One Tick serves every configured event; a failure on one event never holds up another.
type Tick struct {
	cfg      Config
	bookings outbound.Bookings
	queue    outbound.Queue
	clock    outbound.Clock
	metrics  outbound.Metrics
	events   map[string]*eventState

	recon *Reconciler // nil: reconciliation is off (WithReconcile)

	held  outbound.HeldBookings // nil: the HELD sweep is off (WithHeldSweep)
	holds outbound.Holds

	// paymentReconciler invokes payment-api to sweep AWAITING_PAYMENT bookings.
	// nil: the sweep is off (WithAwaitingPaymentSweep).
	paymentReconciler outbound.PaymentReconciler
	awaitingInterval  time.Duration // how often to invoke the sweep
	lastAwaitingSweep time.Time     // when the sweep last ran

	source       outbound.Events // nil: tick cfg.Events
	refreshEvery time.Duration
	found        []domain.Event
	lastRefresh  time.Time
	discovered   bool
}

// NewTick builds the use case. Create a new one to simulate a controller restart: all caches start empty.
func NewTick(cfg Config, b outbound.Bookings, q outbound.Queue, c outbound.Clock, m outbound.Metrics) *Tick {
	return &Tick{cfg: cfg, bookings: b, queue: q, clock: c, metrics: m, events: map[string]*eventState{}}
}

// eventState is what the controller remembers about one event between ticks. It holds only caches: losing it
// (a restart) costs a repeated news item, never a customer.
type eventState struct {
	nowServing    int64 // from the last SNAPSHOT/ADVANCE; lets RECOVER be one Redis call
	known         bool
	stuckTicks    int
	published     bool
	lastNS        int64
	lastBroadcast time.Time
	lastState     string
	rate          rateTracker
	writtenRate   int64
	rateWritten   bool
	gaugesAt      time.Time // when the per-event gauges were last written
}

func (t *Tick) state(id string) *eventState {
	st := t.events[id]
	if st == nil {
		st = &eventState{rate: newRateTracker(t.cfg.MaxSlots, t.cfg.EtaSeedMinutes, t.cfg.Interval)}
		t.events[id] = st
	}
	return st
}

// Run ticks every event once. The sweep goes first, so a place freed by a lapse is used by this very tick.
func (t *Tick) Run(ctx context.Context) {
	t.Sweep(ctx)
	events := t.currentEvents(ctx)
	for _, ev := range events {
		if err := t.RunEvent(ctx, ev); err != nil {
			t.metrics.Inc(MetricTickFailures)
			slog.Error("admission tick failed", slog.String("event", ev.ID), slog.Any("err", err))
		}
	}
	t.reconcileOne(ctx, events)
}

// Loop ticks until ctx ends. A tick that takes longer than the interval delays the next one (never overlaps it)
// and counts a TickOverrun.
func (t *Tick) Loop(ctx context.Context) {
	ticker := time.NewTicker(t.cfg.Interval)
	defer ticker.Stop()
	timer := tickTimer{every: t.cfg.Heartbeat}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			start := time.Now()
			t.Run(ctx)
			took := time.Since(start)
			if took > t.cfg.Interval {
				t.metrics.Inc(MetricTickOverrun)
			}
			if worst, due := timer.observe(took, time.Now()); due {
				t.metrics.Gauge(GaugeTickDuration, worst.Seconds())
			}
		}
	}
}

// tickTimer keeps the slowest tick of each window, so TickDuration is written once per window, not twice a second.
type tickTimer struct {
	every time.Duration
	start time.Time
	worst time.Duration
}

// observe records one tick and reports the slowest tick of the window when the window is over.
func (w *tickTimer) observe(took time.Duration, now time.Time) (time.Duration, bool) {
	if w.start.IsZero() {
		w.start = now
	}
	w.worst = max(w.worst, took)
	if now.Sub(w.start) < w.every {
		return 0, false
	}
	worst := w.worst
	w.start, w.worst = now, 0
	return worst, true
}

// RunEvent is one tick for one event: RECOVER, SNAPSHOT, COUNT, ADVANCE, FINISH, then the news.
func (t *Tick) RunEvent(ctx context.Context, ev domain.Event) error {
	st := t.state(ev.ID)
	stuck := t.Recover(ctx, ev, st)

	ns, issued, err := t.queue.Snapshot(ctx, ev.ID)
	if err != nil {
		return fmt.Errorf("snapshot now_serving: %w", err)
	}
	t.writeGauges(ev.ID, st, ns, issued)
	pending, err := t.queue.PendingCount(ctx, ev.ID, ns)
	if err != nil {
		return fmt.Errorf("snapshot pending: %w", err)
	}
	if ns > st.nowServing {
		// Another controller moved the queue since the last tick: finish its admissions now, before ADVANCE.
		st.nowServing = ns
		if t.Recover(ctx, ev, st) {
			stuck = true
		}
	}
	st.nowServing, st.known = ns, true

	adm, err := t.bookings.CountAdmission(ctx, ev.ID)
	if err != nil {
		// The count and the Lua are one decision: without the count nobody is let in and nobody is lost.
		t.metrics.Inc(MetricCountFailures)
		slog.Warn("admission count failed; skipping ADVANCE", slog.String("event", ev.ID), slog.Any("err", err))
		t.stuck(st, stuck)
		return t.news(ctx, ev, st, ns)
	}

	res, err := t.queue.Advance(ctx, ev, redisscript.Args{
		Max: t.cfg.MaxSlots, Open: adm.Open + pending, Browsing: adm.Browsing + pending,
		NowServing: ns, Now: t.clock.Now().Unix(),
	})
	if err != nil {
		return fmt.Errorf("advance: %w", err)
	}
	switch res.Code {
	case redisscript.ResultAdmitted:
		st.nowServing = res.To
		ns = res.To
		for _, tok := range res.Tokens {
			if !t.finish(ctx, ev.ID, tok) {
				stuck = true
			}
		}
	case redisscript.ResultStale:
		t.metrics.Inc(MetricAdvanceStale)
	case redisscript.ResultStockUninitialized:
		t.metrics.Inc(MetricStockUninitialized)
	case redisscript.ResultMissing:
		t.metrics.Inc(MetricEventStateMissing)
	}
	t.stuck(st, stuck)
	return t.news(ctx, ev, st, ns)
}

// Recover finishes admissions a crashed or failed tick left half done: tokens still in by_ticket whose ticket
// number is <= now_serving. It uses the now_serving remembered from the last tick (one Redis call when nothing is
// pending); a fresh controller reads it once. It reports whether anything is still pending afterwards.
func (t *Tick) Recover(ctx context.Context, ev domain.Event, st *eventState) bool {
	if st == nil {
		st = t.state(ev.ID)
	}
	if !st.known {
		ns, _, err := t.queue.Snapshot(ctx, ev.ID)
		if err != nil {
			slog.Warn("recover: cannot read now_serving", slog.String("event", ev.ID), slog.Any("err", err))
			return false
		}
		st.nowServing, st.known = ns, true
	}
	tokens, err := t.queue.Pending(ctx, ev.ID, st.nowServing)
	if err != nil {
		slog.Warn("recover: cannot list pending grants", slog.String("event", ev.ID), slog.Any("err", err))
		return false
	}
	still := false
	for _, tok := range tokens {
		if t.finish(ctx, ev.ID, tok) {
			t.metrics.Inc(MetricPendingGrantsRecovery)
		} else {
			still = true
		}
	}
	return still
}

// RateSamples is how many speed samples are kept for an event (a test aid).
func (t *Tick) RateSamples(eventID string) int { return len(t.state(eventID).rate.samples) }

// RecoverEvent runs only the RECOVER step (the pending-grants specs call it directly).
func (t *Tick) RecoverEvent(ctx context.Context, ev domain.Event) bool {
	return t.Recover(ctx, ev, t.state(ev.ID))
}

func (t *Tick) stuck(st *eventState, stillPending bool) {
	if !stillPending {
		st.stuckTicks = 0
		return
	}
	st.stuckTicks++
	if st.stuckTicks > t.cfg.StuckAfterTicks {
		t.metrics.Inc(AlertPendingGrantsStuck)
	}
}

// finish turns one admitted token into a customer who is let in: the GRANTED row first, then the grant hash, then the
// copy into the token hash, and only then does the token leave the line. It reports false when the token must stay
// pending (the database failed); true when it is done or was dropped.
func (t *Tick) finish(ctx context.Context, eventID, queueToken string) bool {
	info, found, err := t.queue.Token(ctx, queueToken)
	if err != nil {
		slog.Warn("finish: cannot read token", slog.String("token", queueToken), slog.Any("err", err))
		return false
	}
	if !found {
		// The queue token expired before the booking was saved: the customer has already left.
		t.metrics.Inc(MetricGrantTokenMissing)
		slog.Warn("finish: queue token is gone; dropping the place", slog.String("event", eventID), slog.String("token", queueToken))
		return t.dequeue(ctx, eventID, queueToken)
	}
	now := t.clock.Now()
	g := domain.Grant{
		EventID: eventID, QueueToken: queueToken, AccountID: info.AccountID,
		BookingID: idgen.NewPrefixed("RB-"), GrantToken: idgen.NewPrefixed("gt_"),
		GrantedAt: now, ExpiresAt: now.Add(t.cfg.BookingTTL),
	}
	var b domain.Booking
	for attempt := 0; attempt < t.cfg.InsertAttempts; attempt++ {
		if b, err = t.bookings.InsertGranted(ctx, g); err == nil {
			break
		}
	}
	if err != nil {
		t.metrics.Inc(MetricGrantInsertFailures)
		slog.Warn("finish: cannot save the booking; the place stays pending", slog.String("token", queueToken), slog.Any("err", err))
		return false
	}
	if b.ID == g.BookingID {
		// This call saved the row. A later finish of the same token (RECOVER after a Redis failure) finds the row an
		// earlier call saved, under that call's booking id, and does not count it again. The price: when every attempt of
		// a call errors although one did commit, no call counts that grant (approximate counts, docs/metrics.md §3).
		t.metrics.IncEvent(MetricGranted, eventID)
	}
	if b.Status != "" && b.Status != "GRANTED" {
		// The row already moved on (lapsed, selecting...): never give it a fresh grant.
		return t.dequeue(ctx, eventID, queueToken)
	}
	// A crash or a retry may have saved the row earlier: its own times decide the grant's life, not now.
	ttl := b.GrantedAt.Add(t.cfg.GrantTTL).Sub(now)
	if ttl > 0 {
		if err := t.queue.PutGrant(ctx, eventID, queueToken, b, info, ttl); err != nil {
			slog.Warn("finish: cannot write the grant hash", slog.String("token", queueToken), slog.Any("err", err))
			return false
		}
	}
	if err := t.queue.AttachGrant(ctx, queueToken, b); err != nil {
		slog.Warn("finish: cannot copy the grant into the token hash", slog.String("token", queueToken), slog.Any("err", err))
		return false
	}
	return t.dequeue(ctx, eventID, queueToken)
}

func (t *Tick) dequeue(ctx context.Context, eventID, queueToken string) bool {
	if err := t.queue.Dequeue(ctx, eventID, queueToken); err != nil {
		slog.Warn("finish: cannot remove the token from the line", slog.String("token", queueToken), slog.Any("err", err))
		return false
	}
	return true
}
