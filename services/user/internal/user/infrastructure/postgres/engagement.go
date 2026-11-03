package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// EngagementRepository reads what a customer was in the middle of (BR-09).
//
// Read-only: queue-service owns queue_entries and booking-service owns
// bookings. A write path here would let this service move state it does not
// understand.
type EngagementRepository struct {
	pool *pgxpool.Pool
}

// NewEngagementRepository returns the engagement adapter.
func NewEngagementRepository(pool *pgxpool.Pool) portout.IEngagementRepository {
	return &EngagementRepository{pool: pool}
}

// findActiveEngagement returns the account's one live involvement.
//
// A booking outranks a queue entry: once a slot is granted, the booking is
// where the customer actually is, and the queue row may still exist until the
// queue service tidies it.
//
// Non-terminal statuses are listed explicitly rather than excluding the
// terminal ones. ADR-005 §4 fixes nine statuses, and a tenth added later must
// not silently start counting as "in flight" — with a NOT IN list it would.
const findActiveEngagement = `
SELECT kind, event_id, booking_id FROM (
    SELECT 'booking' AS kind, event_id, id AS booking_id, 1 AS rank, created_at
      FROM bookings
     WHERE account_id = $1
       AND status IN ('GRANTED', 'SELECTING', 'HELD', 'AWAITING_PAYMENT', 'PENDING_CANCELLATION')
    UNION ALL
    SELECT 'queue' AS kind, event_id, '' AS booking_id, 2 AS rank, joined_at AS created_at
      FROM queue_entries
     WHERE account_id = $1
) AS engagement
ORDER BY rank, created_at DESC
LIMIT 1
`

// FindActiveEngagement returns the account's live involvement, or an inactive
// Engagement when there is none.
//
// Absence is not an error: most logins have nothing in flight, and failing a
// login because the customer is not mid-purchase would be absurd.
func (r *EngagementRepository) FindActiveEngagement(ctx context.Context, accountID string) (entity.Engagement, error) {
	var kind, eventID, bookingID string
	err := r.pool.QueryRow(ctx, findActiveEngagement, accountID).Scan(&kind, &eventID, &bookingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Engagement{Kind: entity.EngagementNone}, nil
	}
	if err != nil {
		return entity.Engagement{}, apperror.ErrInternal("failed to read engagement")
	}
	return entity.Engagement{
		Kind:      entity.EngagementKind(kind),
		EventID:   eventID,
		BookingID: bookingID,
	}, nil
}
