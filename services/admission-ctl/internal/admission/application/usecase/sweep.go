package usecase

import (
	"context"
	"log/slog"
)

// Sweep lapses every GRANTED booking past its 5-minute deadline (GRANTED → LAPSED, final, no second chance) and
// kills each one's grant hash. The row is the only record: if the controller dies between the UPDATE and the DEL,
// the booking is already LAPSED, so every credential check on that grant is rejected by the status check, and the
// hash expires by itself. The queue token and acct key are kept until the sale window ends (join answers
// rejoined: true with state LAPSED). SELECTING and later belong to 05-booking.
func (t *Tick) Sweep(ctx context.Context) int {
	lapsed, err := t.bookings.LapseDue(ctx, t.clock.Now())
	if err != nil {
		t.metrics.Inc(MetricSweepFailures)
		slog.Warn("sweep failed", slog.Any("err", err))
		return 0
	}
	for _, l := range lapsed {
		t.metrics.Inc(MetricLapsed)
		if l.GrantToken == "" {
			continue
		}
		if err := t.queue.DropGrant(ctx, l.GrantToken); err != nil {
			slog.Warn("sweep: cannot delete the grant hash; it expires by itself", slog.String("booking", l.BookingID), slog.Any("err", err))
		}
	}
	return len(lapsed)
}
