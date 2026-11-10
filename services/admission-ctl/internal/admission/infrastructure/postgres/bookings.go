// Package postgres is the admission-ctl bookings adapter on Aurora PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// Querier is the slice of *pgxpool.Pool the store uses. It must be the WRITER pool:
// a place freed a moment ago must be seen by the next count (no replica lag).
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Bookings reads and writes the bookings table.
type Bookings struct{ db Querier }

// NewBookings builds the store on the writer pool.
func NewBookings(db Querier) *Bookings { return &Bookings{db: db} }

// CountSQL counts who is inside one event. The status list matches the predicate of
// the partial index bookings_event_active_idx (migration 0007), so the planner can use it.
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

const existingSQL = `SELECT id, COALESCE(grant_token, ''), granted_at, booking_expires_at, status FROM bookings WHERE event_id = $1 AND queue_token = $2`

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
		return domain.Booking{ID: g.BookingID, GrantToken: g.GrantToken, GrantedAt: g.GrantedAt, ExpiresAt: g.ExpiresAt, Status: "GRANTED"}, nil
	}
	var first domain.Booking
	err = b.db.QueryRow(ctx, existingSQL, g.EventID, g.QueueToken).Scan(&first.ID, &first.GrantToken, &first.GrantedAt, &first.ExpiresAt, &first.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Booking{}, fmt.Errorf("insert granted booking: conflict row vanished for %s/%s", g.EventID, g.QueueToken)
	}
	if err != nil {
		return domain.Booking{}, fmt.Errorf("read existing booking: %w", err)
	}
	return first, nil
}

// SweepSQL lapses every GRANTED booking past its deadline in one compare-and-set UPDATE. A row another sweeper or
// the booking service already moved no longer matches status = 'GRANTED', so exactly one caller gets it back.
// The partial index bookings_granted_deadline_idx (migration 0007) finds the due rows without scanning the table.
const SweepSQL = `UPDATE bookings SET status = 'LAPSED'
WHERE status = 'GRANTED' AND booking_expires_at <= $1
RETURNING id, event_id, COALESCE(queue_token, ''), COALESCE(grant_token, '')`

// LapseDue moves due GRANTED bookings to LAPSED and returns them.
func (b *Bookings) LapseDue(ctx context.Context, now time.Time) ([]domain.Ended, error) {
	return b.endDue(ctx, SweepSQL, now, "lapse due bookings")
}

// ExpireSelectingSQL ends every SELECTING booking past its 5-minute deadline: the customer opened the ticket page but
// did not press Pay in time. Same compare-and-set shape as SweepSQL; the partial index bookings_selecting_deadline_idx
// (migration 0009) finds the due rows. A SELECTING row holds no stock and no NIK, so nothing else is released.
const ExpireSelectingSQL = `UPDATE bookings SET status = 'EXPIRED'
WHERE status = 'SELECTING' AND booking_expires_at <= $1
RETURNING id, event_id, COALESCE(queue_token, ''), COALESCE(grant_token, '')`

// ExpireSelectingDue moves due SELECTING bookings to EXPIRED and returns them.
func (b *Bookings) ExpireSelectingDue(ctx context.Context, now time.Time) ([]domain.Ended, error) {
	return b.endDue(ctx, ExpireSelectingSQL, now, "expire due selecting bookings")
}

// endDue runs one of the compare-and-set sweep statements and reads back the rows it ended.
func (b *Bookings) endDue(ctx context.Context, sql string, now time.Time, what string) ([]domain.Ended, error) {
	rows, err := b.db.Query(ctx, sql, now)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer rows.Close()
	var out []domain.Ended
	for rows.Next() {
		var l domain.Ended
		if err := rows.Scan(&l.BookingID, &l.EventID, &l.QueueToken, &l.GrantToken); err != nil {
			return nil, fmt.Errorf("%s: read row: %w", what, err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
