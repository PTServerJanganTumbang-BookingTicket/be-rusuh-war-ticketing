// Package postgres is the events service's Aurora adapter. It reads through the reader endpoint and writes nothing.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

// db is the part of the pool the store uses. *pgxpool.Pool satisfies it; store_unit_test.go fakes it so the Go side
// (scanning, binding, error handling) is covered where there is no database (CI). store_test.go proves the SQL.
type db interface {
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Store implements outbound.Events.
type Store struct{ db db }

// NewStore wraps the pool (the reader endpoint).
func NewStore(pool *pgxpool.Pool) *Store { return &Store{db: pool} }

const eventColumns = `id, name, category, event_date, event_time, venue, location_city, image_url, description, sale_starts_at`

// ListSQL reads the home page order: newest event date first, then id so pages never overlap. CountSQL gives the total.
const (
	ListSQL  = `SELECT ` + eventColumns + ` FROM events ORDER BY event_date DESC, id LIMIT $1 OFFSET $2`
	CountSQL = `SELECT count(*) FROM events`
)

// GetEventSQL and CategoriesSQL read one event and its categories in the listed order.
const (
	GetEventSQL   = `SELECT ` + eventColumns + ` FROM events WHERE id = $1`
	CategoriesSQL = `SELECT id, name, description, price, available FROM ticket_categories WHERE event_id = $1 ORDER BY sort_order, id`
)

func scanEvent(r pgx.Row) (domain.Event, error) {
	var e domain.Event
	err := r.Scan(&e.ID, &e.Name, &e.Category, &e.Date, &e.Time, &e.Venue, &e.City, &e.ImageURL, &e.Description, &e.SaleStartsAt)
	return e, err
}

// List implements outbound.Events: the page and the total in one batch (one round trip).
func (s *Store) List(ctx context.Context, limit, offset int) ([]domain.Event, int, error) {
	var b pgx.Batch
	b.Queue(ListSQL, limit, offset)
	b.Queue(CountSQL)
	br := s.db.SendBatch(ctx, &b)
	defer func() { _ = br.Close() }()

	rows, err := br.Query()
	if err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}
	var out []domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("scan event: %w", err)
		}
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}
	var total int
	if err := br.QueryRow().Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}
	return out, total, nil
}

// Get implements outbound.Events: the event and its categories in one batch (one round trip).
func (s *Store) Get(ctx context.Context, id string) (domain.Event, []domain.Category, bool, error) {
	var b pgx.Batch
	b.Queue(GetEventSQL, id)
	b.Queue(CategoriesSQL, id)
	br := s.db.SendBatch(ctx, &b)
	defer func() { _ = br.Close() }()

	ev, err := scanEvent(br.QueryRow())
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Event{}, nil, false, nil
	}
	if err != nil {
		return domain.Event{}, nil, false, fmt.Errorf("get event: %w", err)
	}
	rows, err := br.Query()
	if err != nil {
		return domain.Event{}, nil, false, fmt.Errorf("get categories: %w", err)
	}
	defer rows.Close()
	var cats []domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Price, &c.Available); err != nil {
			return domain.Event{}, nil, false, fmt.Errorf("scan category: %w", err)
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return domain.Event{}, nil, false, fmt.Errorf("get categories: %w", err)
	}
	return ev, cats, true, nil
}
