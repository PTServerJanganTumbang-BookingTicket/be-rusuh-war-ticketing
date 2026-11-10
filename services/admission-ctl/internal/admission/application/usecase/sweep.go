package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// Sweep ends every booking whose 5-minute deadline has passed and kills each one's grant hash, so the next tick's
// COUNT gives the place to the next customer:
//
//   - GRANTED → LAPSED: the customer never opened the ticket page (final, no second chance).
//   - SELECTING → EXPIRED: the customer opened it but did not press Pay in time (docs/05-booking, B2). A SELECTING
//     booking holds no stock and no NIK, so nothing else is released.
//
// The row is the only record: if the controller dies between an UPDATE and the DEL, the booking has already ended,
// so every credential check on that grant is rejected by the status check, and the hash expires by itself. The
// queue token and acct key are kept until the sale window ends (a rejoin finds the old engagement). One failing
// statement does not stop the other. Returns the number of bookings ended.
func (t *Tick) Sweep(ctx context.Context) int {
	now := t.clock.Now()
	n := t.end(ctx, "lapse", MetricLapsed, now, t.bookings.LapseDue)
	return n + t.end(ctx, "expire selecting", MetricSelectingExpired, now, t.bookings.ExpireSelectingDue)
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
		if b.GrantToken == "" {
			continue
		}
		if err := t.queue.DropGrant(ctx, b.GrantToken); err != nil {
			slog.Warn("sweep: cannot delete the grant hash; it expires by itself", slog.String("step", what),
				slog.String("booking", b.BookingID), slog.Any("err", err))
		}
	}
	return len(ended)
}
