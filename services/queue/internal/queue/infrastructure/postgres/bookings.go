// Package postgres is the BookingReader adapter on Aurora PostgreSQL.
package postgres

import (
	"context"
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

const byQueueTokenSQL = `SELECT status
FROM bookings WHERE event_id = $1 AND queue_token = $2`

// ByQueueToken returns the booking for (event_id, queue_token), if any.
func (b *Bookings) ByQueueToken(ctx context.Context, eventID, queueToken string) (domain.Booking, bool, error) {
	var status string
	err := b.db.QueryRow(ctx, byQueueTokenSQL, eventID, queueToken).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Booking{}, false, nil
	}
	if err != nil {
		return domain.Booking{}, false, fmt.Errorf("bookings by queue_token: %w", err)
	}
	return domain.Booking{Status: status}, true, nil
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

const detailSQL = `SELECT id, status, COALESCE(grant_token, ''), booking_expires_at, payment_expires_at, COALESCE(payment_ref, '')
FROM bookings WHERE event_id = $1 AND queue_token = $2`

// Detail reads the booking of a customer for /queue/me. Missing deadlines read as the zero time.
func (b *Bookings) Detail(ctx context.Context, eventID, queueToken string) (domain.BookingDetail, bool, error) {
	var (
		d              domain.BookingDetail
		booking, payBy *time.Time
	)
	err := b.db.QueryRow(ctx, detailSQL, eventID, queueToken).Scan(&d.ID, &d.Status, &d.GrantToken, &booking, &payBy, &d.PaymentRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BookingDetail{}, false, nil
	}
	if err != nil {
		return domain.BookingDetail{}, false, fmt.Errorf("booking detail: %w", err)
	}
	if booking != nil {
		d.BookingExpiresAt = booking.UTC()
	}
	if payBy != nil {
		d.PaymentExpiresAt = payBy.UTC()
	}
	return d, true, nil
}
