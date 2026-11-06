// Package postgres is the BookingReader adapter on Aurora PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

// Querier is the slice of *pgxpool.Pool the reader uses.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Bookings reads bookings. Join only ever SELECTs, on the writer pool, so a
// GRANTED row written a moment ago by admission-ctl is visible (no replica lag).
type Bookings struct{ db Querier }

// NewBookings builds the reader on the writer pool.
func NewBookings(db Querier) *Bookings { return &Bookings{db: db} }

const byQueueTokenSQL = `SELECT status, grant_token, booking_expires_at
FROM bookings WHERE event_id = $1 AND queue_token = $2`

// ByQueueToken returns the booking for (event_id, queue_token), if any.
func (b *Bookings) ByQueueToken(ctx context.Context, eventID, queueToken string) (domain.Booking, bool, error) {
	var (
		status  string
		grant   sql.NullString
		expires sql.NullTime
	)
	err := b.db.QueryRow(ctx, byQueueTokenSQL, eventID, queueToken).Scan(&status, &grant, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Booking{}, false, nil
	}
	if err != nil {
		return domain.Booking{}, false, fmt.Errorf("bookings by queue_token: %w", err)
	}
	out := domain.Booking{Status: status, GrantToken: grant.String}
	if expires.Valid {
		out.BookingExpiresAt = expires.Time.UTC()
	}
	return out, true, nil
}

// NewPool opens the writer pool.
func NewPool(ctx context.Context, dsn string, maxConns int32, lifetime time.Duration) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	if lifetime > 0 {
		cfg.MaxConnLifetime = lifetime
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}
