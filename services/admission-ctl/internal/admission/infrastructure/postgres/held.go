package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

var _ outbound.HeldBookings = (*Bookings)(nil)

// ExpireHeldSQL ends every HELD booking whose 10-minute payment window has passed without anyone starting to pay. Same
// compare-and-set shape as the other two sweeps: a row the payment service already moved to AWAITING_PAYMENT, or another
// sweeper already expired, no longer matches, so exactly one caller gets it back. The partial index
// bookings_held_deadline_idx (migration 0012) finds the due rows. AWAITING_PAYMENT and PENDING_CANCELLATION (a payment
// is in flight) belong to 07-payment and are never touched.
const ExpireHeldSQL = `UPDATE bookings SET status = 'EXPIRED'
WHERE status = 'HELD' AND payment_expires_at <= $1
RETURNING id, event_id, COALESCE(queue_token, ''), COALESCE(grant_token, '')`

// releaseClaimsSQL frees the NIKs of the expired bookings: UNIQUE (event_id, nik_hmac) would otherwise block each of
// them for this event for good.
const releaseClaimsSQL = `DELETE FROM nik_claims WHERE booking_id = ANY($1) RETURNING booking_id, nik_hmac`

// heldSeatsSQL counts the seats per category the expired bookings took, from booking_items (kept as history).
const heldSeatsSQL = `SELECT booking_id, category_id, count(*) FROM booking_items
WHERE booking_id = ANY($1) GROUP BY booking_id, category_id ORDER BY booking_id, category_id`

// ExpireHeldDue ends the due HELD bookings, deletes their NIK claims and counts what each held, all in ONE transaction:
// if the status became EXPIRED but the claims stayed, those NIKs would be blocked for this event for good. Redis is given
// back by the caller after this returns, i.e. after the commit; a crash in between leaves stock held without a booking
// until stock reconciliation (B5) repairs it. Nothing is returned when the transaction does not commit.
func (b *Bookings) ExpireHeldDue(ctx context.Context, now time.Time) ([]domain.EndedHold, error) {
	tx, err := b.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("expire held bookings: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	var out []domain.EndedHold
	index := map[string]int{} // booking id -> position in out
	rows, err := tx.Query(ctx, ExpireHeldSQL, now)
	if err != nil {
		return nil, fmt.Errorf("expire held bookings: %w", err)
	}
	for rows.Next() {
		var h domain.EndedHold
		if err := rows.Scan(&h.BookingID, &h.EventID, &h.QueueToken, &h.GrantToken); err != nil {
			rows.Close()
			return nil, fmt.Errorf("expire held bookings: read row: %w", err)
		}
		index[h.BookingID] = len(out)
		out = append(out, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("expire held bookings: %w", err)
	}

	if len(out) > 0 {
		ids := make([]string, len(out))
		for i, h := range out {
			ids[i] = h.BookingID
		}
		if err := collect(ctx, tx, releaseClaimsSQL, ids, func(scan func(...any) error) error {
			var id, hmac string
			if err := scan(&id, &hmac); err != nil {
				return err
			}
			if i, ok := index[id]; ok {
				out[i].NIKs = append(out[i].NIKs, hmac)
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("expire held bookings: release NIK claims: %w", err)
		}
		if err := collect(ctx, tx, heldSeatsSQL, ids, func(scan func(...any) error) error {
			var id, cat string
			var n int64
			if err := scan(&id, &cat, &n); err != nil {
				return err
			}
			if i, ok := index[id]; ok {
				out[i].Seats = append(out[i].Seats, domain.SeatCount{CategoryID: cat, Qty: n})
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("expire held bookings: count seats: %w", err)
		}
	}

	committed = true
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("expire held bookings: commit: %w", err)
	}
	return out, nil
}

// collect runs a statement bound to the booking ids and hands every row to each. The cursor is closed when it returns,
// before the next statement: a transaction runs one statement at a time.
func collect(ctx context.Context, tx pgx.Tx, sql string, ids []string, each func(scan func(...any) error) error) error {
	rows, err := tx.Query(ctx, sql, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}
