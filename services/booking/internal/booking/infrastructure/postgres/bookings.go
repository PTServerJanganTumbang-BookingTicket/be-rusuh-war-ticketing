// Package postgres is booking's Aurora adapter (writer pool).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

// Store implements outbound.Bookings and outbound.Catalog.
type Store struct{ db *pgxpool.Pool }

// NewStore wraps the writer pool.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// ClaimSQL is the claim (GRANTED → SELECTING), returning the row. A compare-and-set: a reload or a second tab
// matches nothing. Only before the deadline, on Aurora's clock (the same clock as the sweeps): a GRANTED booking whose
// 5 minutes have run out stays GRANTED and lapses, instead of turning into an "opened, did not pay" EXPIRED. The
// deadline itself is not touched; it was fixed at GRANT (BR-03). Bound to id AND grant_token (ADR-005 §12).
const ClaimSQL = `UPDATE bookings SET status = 'SELECTING', slot_claimed_at = now()
WHERE id = $1 AND grant_token = $2 AND status = 'GRANTED' AND booking_expires_at > now()
RETURNING ` + bookingColumns

// readSQL reads the row when the claim matched nothing: already claimed (reload), later, finished, or past its
// deadline. A separate statement, so a concurrent claim that won is seen as SELECTING.
const readSQL = `SELECT ` + bookingColumns + ` FROM bookings WHERE id = $1 AND grant_token = $2`

const bookingColumns = `id, event_id, status, booking_expires_at, payment_expires_at`

// ClaimForSelection moves GRANTED → SELECTING once and returns the row: one round trip on the first load.
func (s *Store) ClaimForSelection(ctx context.Context, bookingID, grantToken string) (domain.Booking, bool, error) {
	b, err := scanBooking(s.db.QueryRow(ctx, ClaimSQL, bookingID, grantToken))
	if err == nil {
		return b, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Booking{}, false, fmt.Errorf("claim booking: %w", err)
	}
	b, err = scanBooking(s.db.QueryRow(ctx, readSQL, bookingID, grantToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Booking{}, false, nil
	}
	if err != nil {
		return domain.Booking{}, false, fmt.Errorf("read booking: %w", err)
	}
	return b, true, nil
}

func scanBooking(row pgx.Row) (domain.Booking, error) {
	var (
		b                domain.Booking
		status           string
		booking, payment *time.Time
	)
	if err := row.Scan(&b.ID, &b.EventID, &status, &booking, &payment); err != nil {
		return domain.Booking{}, err
	}
	st, err := bookingstate.Parse(status)
	if err != nil {
		return domain.Booking{}, err
	}
	b.Status = st
	if booking != nil {
		b.BookingExpiresAt = booking.UTC()
	}
	if payment != nil {
		b.PaymentExpiresAt = payment.UTC()
	}
	return b, nil
}

// Page reads the event and its categories in display order, sent as one batch (one round trip).
func (s *Store) Page(ctx context.Context, eventID string) (domain.Event, []domain.Category, bool, error) {
	batch := &pgx.Batch{}
	batch.Queue(`SELECT id, name, event_date, venue, image_url FROM events WHERE id = $1`, eventID)
	batch.Queue(`SELECT id, name, description, price, max_per_order
FROM ticket_categories WHERE event_id = $1 ORDER BY sort_order, id`, eventID)
	res := s.db.SendBatch(ctx, batch)
	defer func() { _ = res.Close() }()

	var (
		e    domain.Event
		date time.Time
	)
	err := res.QueryRow().Scan(&e.ID, &e.Name, &date, &e.Venue, &e.ImageURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Event{}, nil, false, nil
	}
	if err != nil {
		return domain.Event{}, nil, false, fmt.Errorf("read event: %w", err)
	}
	e.Date = date.Format(time.DateOnly)

	rows, err := res.Query()
	if err != nil {
		return domain.Event{}, nil, false, fmt.Errorf("read categories: %w", err)
	}
	defer rows.Close()
	var cats []domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Price, &c.MaxPerOrder); err != nil {
			return domain.Event{}, nil, false, fmt.Errorf("read category: %w", err)
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return domain.Event{}, nil, false, fmt.Errorf("read categories: %w", err)
	}
	return e, cats, true, nil
}
