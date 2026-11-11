package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// Sweep ends every booking whose 5-minute deadline has passed and kills each one's grant hash, so the next tick's
// COUNT gives the place to the next customer:
//
//   - GRANTED → LAPSED: the customer never opened the ticket page (final, no second chance).
//   - SELECTING → EXPIRED: the customer opened it but did not press Pay in time (docs/05-booking, B2). A SELECTING
//     booking holds no stock and no NIK, so nothing else is released.
//   - HELD → EXPIRED (when WithHeldSweep is wired): the customer pressed Pay but nobody started paying within 10
//     minutes. This one does hold stock and NIK locks, so they are given back too (docs/05-booking/02 §3.4).
//
// The row is the only record: if the controller dies between an UPDATE and the DEL, the booking has already ended,
// so every credential check on that grant is rejected by the status check, and the hash expires by itself. The
// queue token and acct key are kept until the sale window ends (a rejoin finds the old engagement). One failing
// statement does not stop the other. Returns the number of bookings ended.
func (t *Tick) Sweep(ctx context.Context) int {
	now := t.clock.Now()
	n := t.end(ctx, "lapse", MetricLapsed, now, t.bookings.LapseDue)
	n += t.end(ctx, "expire selecting", MetricSelectingExpired, now, t.bookings.ExpireSelectingDue)
	return n + t.expireHeld(ctx, now)
}

// heldSweepTimeout bounds the HELD sweep: the transaction and the release of every booking it expired. It runs on a
// context the controller's shutdown cannot cancel (see expireHeld), so it needs a deadline of its own, short enough to
// fit a container's stop grace period.
const heldSweepTimeout = 15 * time.Second

// WithHeldSweep turns on the HELD → EXPIRED sweep: a held booking whose 10-minute payment window passed without anyone
// starting to pay is ended, and its seats and NIK locks go back (docs/05-booking/02-create-booking.md §3.4). Without
// it, held seats would stay held for the whole sale. Both ports are required: a half-wired sweep would end bookings
// and leave their stock held, so it fails here, at start.
func (t *Tick) WithHeldSweep(b outbound.HeldBookings, h outbound.Holds) *Tick {
	if b == nil || h == nil {
		panic("usecase: WithHeldSweep needs both the bookings and the holds")
	}
	t.held, t.holds = b, h
	return t
}

// expireHeld ends the due HELD bookings. The database transaction (status, claims) has committed when ExpireHeldDue
// returns; only then is the Redis side given back, once per booking. A release that fails is counted, not retried: the
// row is already EXPIRED, so retrying here would find nothing, and stock reconciliation (B5) repairs the keys. Bookings
// in AWAITING_PAYMENT or PENDING_CANCELLATION belong to the payment service and are never touched.
func (t *Tick) expireHeld(ctx context.Context, now time.Time) int {
	if t.held == nil {
		return 0
	}
	// A SIGTERM (a rolling deploy during the sale) cancels the controller's context. The transaction may have committed
	// by then, and the rows are EXPIRED: nothing retries them, so a cancelled release would leak their stock and NIK locks
	// until reconciliation. The whole HELD sweep therefore runs on a context the shutdown cannot cancel, with its own
	// deadline; the tick that follows sees the cancelled context and stops.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), heldSweepTimeout)
	defer cancel()
	ended, err := t.held.ExpireHeldDue(ctx, now)
	if err != nil {
		t.metrics.Inc(MetricSweepFailures)
		slog.Warn("sweep failed", slog.String("step", "expire held"), slog.Any("err", err))
		return 0
	}
	for _, h := range ended {
		t.metrics.Inc(MetricHeldExpired)
		if len(h.Seats) == 0 && len(h.NIKs) > 0 {
			// Claims without seats: the release needs the categories, so these NIK locks would stay until their TTL.
			// The hold transaction never writes this; count it rather than skip it silently.
			t.metrics.Inc(MetricReleaseFailed)
			slog.Error("sweep: expired booking has NIK claims but no seats; its NIK locks cannot be released",
				slog.String("booking", h.BookingID), slog.String("event", h.EventID))
		}
		if len(h.Seats) > 0 {
			missing, err := t.holds.Release(ctx, h)
			if err != nil {
				t.metrics.Inc(MetricReleaseFailed)
				slog.Error("sweep: cannot give a hold back; stock and NIK locks stay held until reconciliation",
					slog.String("booking", h.BookingID), slog.String("event", h.EventID), slog.Any("err", err))
			}
			for i := 0; i < missing; i++ {
				t.metrics.Inc(MetricReleaseStockMissing)
			}
		}
		t.dropGrant(ctx, "expire held", h.Ended)
	}
	return len(ended)
}

// dropGrant deletes the grant hash of an ended booking. A failed DEL is logged: the hash expires by itself and every use
// is already rejected by the status check.
func (t *Tick) dropGrant(ctx context.Context, what string, b domain.Ended) {
	if b.GrantToken == "" {
		return
	}
	if err := t.queue.DropGrant(ctx, b.GrantToken); err != nil {
		slog.Warn("sweep: cannot delete the grant hash; it expires by itself", slog.String("step", what),
			slog.String("booking", b.BookingID), slog.Any("err", err))
	}
}

func (t *Tick) end(ctx context.Context, what, metric string, now time.Time,
	due func(context.Context, time.Time) ([]domain.Ended, error)) int {
	ended, err := due(ctx, now)
	if err != nil {
		t.metrics.Inc(MetricSweepFailures)
		slog.Warn("sweep failed", slog.String("step", what), slog.Any("err", err))
		return 0
	}
	for _, b := range ended {
		t.metrics.Inc(metric)
		t.dropGrant(ctx, what, b)
	}
	return len(ended)
}
