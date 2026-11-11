package postgres

import (
	"context"
	"fmt"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

var _ outbound.ReconcileBookings = (*Bookings)(nil)

// StockExpectationsSQL lists the categories of an event from ticket_categories (not from Redis: a lost key must not hide its
// own category) with the seats taken by bookings that still hold them. PENDING_CANCELLATION counts: during its 30 s grace
// the booking still holds its seats (ADR-005 §6 lists three statuses and leaves it out). EXPIRED, LAPSED, ABANDONED,
// SELECTING and GRANTED hold nothing. Writer pool: a replica's lag would read a fresh booking as missing.
const StockExpectationsSQL = `SELECT c.id, c.capacity, COALESCE(t.taken, 0)
FROM ticket_categories c
LEFT JOIN (
    SELECT bi.category_id, count(*) AS taken
    FROM booking_items bi JOIN bookings b ON b.id = bi.booking_id
    WHERE bi.event_id = $1 AND b.status IN ('HELD', 'AWAITING_PAYMENT', 'PENDING_CANCELLATION', 'CONFIRMED')
    GROUP BY bi.category_id
) t ON t.category_id = c.id
WHERE c.event_id = $1
ORDER BY c.sort_order, c.id`

// StockExpectations returns, per category of the event, its capacity and the seats taken.
func (b *Bookings) StockExpectations(ctx context.Context, eventID string) ([]domain.StockExpectation, error) {
	rows, err := b.db.Query(ctx, StockExpectationsSQL, eventID)
	if err != nil {
		return nil, fmt.Errorf("read expected stock of %s: %w", eventID, err)
	}
	defer rows.Close()
	var out []domain.StockExpectation
	for rows.Next() {
		var e domain.StockExpectation
		if err := rows.Scan(&e.CategoryID, &e.Capacity, &e.Taken); err != nil {
			return nil, fmt.Errorf("read expected stock of %s: %w", eventID, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read expected stock of %s: %w", eventID, err)
	}
	return out, nil
}

// ClaimedNIKs lists the nik_hmac of every nik_claims row of the event: what nik:used should hold in Redis.
func (b *Bookings) ClaimedNIKs(ctx context.Context, eventID string) ([]string, error) {
	rows, err := b.db.Query(ctx, `SELECT nik_hmac FROM nik_claims WHERE event_id = $1`, eventID)
	if err != nil {
		return nil, fmt.Errorf("read NIK claims of %s: %w", eventID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("read NIK claims of %s: %w", eventID, err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read NIK claims of %s: %w", eventID, err)
	}
	return out, nil
}
