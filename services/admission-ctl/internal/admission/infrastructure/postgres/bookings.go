// Package postgres is the admission-ctl bookings adapter on Aurora PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// Querier is the slice of *pgxpool.Pool the store uses. It must be the WRITER pool:
// a place freed a moment ago must be seen by the next count (no replica lag).
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Bookings reads and writes the bookings table.
type Bookings struct{ db Querier }

// NewBookings builds the store on the writer pool.
func NewBookings(db Querier) *Bookings { return &Bookings{db: db} }

// CountSQL counts who is inside one event. The status list matches the predicate of
// the partial index bookings_event_active_idx (migration 0006), so the planner can use it.
// open: every active status. browsing: only GRANTED and SELECTING (ADR-006 §3.2).
const CountSQL = `SELECT
    count(*),
    count(*) FILTER (WHERE status IN ('GRANTED', 'SELECTING'))
FROM bookings
WHERE event_id = $1
  AND status IN ('GRANTED', 'SELECTING', 'HELD', 'AWAITING_PAYMENT', 'PENDING_CANCELLATION')`

// CountAdmission counts who is inside event eventID.
func (b *Bookings) CountAdmission(ctx context.Context, eventID string) (domain.Admission, error) {
	var a domain.Admission
	if err := b.db.QueryRow(ctx, CountSQL, eventID).Scan(&a.Open, &a.Browsing); err != nil {
		return domain.Admission{}, fmt.Errorf("count admission for %s: %w", eventID, err)
	}
	return a, nil
}

const insertGrantedSQL = `INSERT INTO bookings
    (id, event_id, queue_token, account_id, status, grant_token, granted_at, booking_expires_at)
VALUES ($1, $2, $3, NULLIF($4, ''), 'GRANTED', $5, $6, $7)
ON CONFLICT (event_id, queue_token) DO NOTHING`

const existingSQL = `SELECT id, COALESCE(grant_token, '') FROM bookings WHERE event_id = $1 AND queue_token = $2`

// InsertGranted saves a GRANTED row once per (event_id, queue_token). Saving the same
// customer again changes nothing and returns the first row's booking_id and grant_token,
// so a retry after a crash hands out the same grant. A grant_token already used by
// another customer is an error (unique violation), never silently reused.
func (b *Bookings) InsertGranted(ctx context.Context, g domain.Grant) (domain.Booking, error) {
	tag, err := b.db.Exec(ctx, insertGrantedSQL,
		g.BookingID, g.EventID, g.QueueToken, g.AccountID, g.GrantToken, g.GrantedAt, g.ExpiresAt)
	if err != nil {
		return domain.Booking{}, fmt.Errorf("insert granted booking: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return domain.Booking{ID: g.BookingID, GrantToken: g.GrantToken}, nil
	}
	var first domain.Booking
	err = b.db.QueryRow(ctx, existingSQL, g.EventID, g.QueueToken).Scan(&first.ID, &first.GrantToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Booking{}, fmt.Errorf("insert granted booking: conflict row vanished for %s/%s", g.EventID, g.QueueToken)
	}
	if err != nil {
		return domain.Booking{}, fmt.Errorf("read existing booking: %w", err)
	}
	return first, nil
}
