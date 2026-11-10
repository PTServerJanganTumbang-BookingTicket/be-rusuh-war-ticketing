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

// db is the part of the pool the store uses. *pgxpool.Pool satisfies it; the unit tests (bookings_unit_test.go) fake it
// to cover the Go side (scanning, status parsing, error handling) without a database. The SQL itself is proven by the
// integration tests in bookings_test.go against real Postgres.
type db interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Store implements outbound.Bookings and outbound.Catalog.
type Store struct{ db db }

// NewStore wraps the writer pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{db: pool} }

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

// detailSQL reads the booking and its event, bound to id AND grant_token (ADR-005 §12).
const detailSQL = `SELECT b.id, b.event_id, b.status, b.payment_expires_at, COALESCE(b.payment_method, ''),
       e.id, e.name, e.event_date, e.venue, e.image_url
FROM bookings b JOIN events e ON e.id = b.event_id
WHERE b.id = $1 AND b.grant_token = $2`

// itemsSQL reads the seats, each with its ticket once issued. Bound to the grant too, so a wrong or foreign id
// reads nothing in either statement. Order: created_at, then id; POST /bookings (B4) writes the seats of one order
// with increasing created_at (clock_timestamp()), so this is the order they were chosen in.
const itemsSQL = `SELECT i.id, i.category_name, i.unit_price, i.nik_masked, COALESCE(t.id, ''), COALESCE(t.status, '')
FROM booking_items i
JOIN bookings b ON b.id = i.booking_id AND b.grant_token = $2
LEFT JOIN tickets t ON t.booking_item_id = i.id
WHERE i.booking_id = $1 ORDER BY i.created_at, i.id`

// Detail reads a booking for GET /bookings/{id}: booking + event, then the seats, as one batch.
func (s *Store) Detail(ctx context.Context, bookingID, grantToken string) (domain.BookingDetail, bool, error) {
	batch := &pgx.Batch{}
	batch.Queue(detailSQL, bookingID, grantToken)
	batch.Queue(itemsSQL, bookingID, grantToken)
	res := s.db.SendBatch(ctx, batch)
	defer func() { _ = res.Close() }()

	var (
		d       domain.BookingDetail
		status  string
		payment *time.Time
		date    time.Time
	)
	err := res.QueryRow().Scan(&d.ID, &d.EventID, &status, &payment, &d.PaymentMethod,
		&d.Event.ID, &d.Event.Name, &date, &d.Event.Venue, &d.Event.ImageURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BookingDetail{}, false, nil
	}
	if err != nil {
		return domain.BookingDetail{}, false, fmt.Errorf("read booking detail: %w", err)
	}
	if d.Status, err = bookingstate.Parse(status); err != nil {
		return domain.BookingDetail{}, false, err
	}
	if payment != nil {
		d.PaymentExpiresAt = payment.UTC()
	}
	d.Event.Date = date.Format(time.DateOnly)

	rows, err := res.Query()
	if err != nil {
		return domain.BookingDetail{}, false, fmt.Errorf("read booking items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it domain.Item
		if err := rows.Scan(&it.ID, &it.CategoryName, &it.UnitPrice, &it.NIKMasked, &it.TicketID, &it.TicketStatus); err != nil {
			return domain.BookingDetail{}, false, fmt.Errorf("read booking item: %w", err)
		}
		d.Items = append(d.Items, it)
	}
	if err := rows.Err(); err != nil {
		return domain.BookingDetail{}, false, fmt.Errorf("read booking items: %w", err)
	}
	return d, true, nil
}
