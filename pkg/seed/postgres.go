package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the slice of a pgx pool or transaction the seed uses.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const upsertEventSQL = `INSERT INTO events (id, name, category, venue, location_city, event_date, event_time, image_url, description, sale_starts_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name, category = EXCLUDED.category, venue = EXCLUDED.venue, location_city = EXCLUDED.location_city,
    event_date = EXCLUDED.event_date, event_time = EXCLUDED.event_time, image_url = EXCLUDED.image_url,
    description = EXCLUDED.description, sale_starts_at = EXCLUDED.sale_starts_at, updated_at = now()`

// available is only set on INSERT: it is a live snapshot of the stock and a re-seed must not reset it.
const upsertCategorySQL = `INSERT INTO ticket_categories (id, event_id, name, description, price, capacity, available, max_per_order, sort_order)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
    event_id = EXCLUDED.event_id, name = EXCLUDED.name, description = EXCLUDED.description, price = EXCLUDED.price,
    capacity = EXCLUDED.capacity, max_per_order = EXCLUDED.max_per_order, sort_order = EXCLUDED.sort_order, updated_at = now()`

// Postgres upserts the events and their categories by id. Run it inside one transaction.
func Postgres(ctx context.Context, db Execer, events []Event) error {
	for _, e := range events {
		date, err := time.Parse("2006-01-02", e.Date)
		if err != nil {
			return fmt.Errorf("event %s: date %q: %w", e.ID, e.Date, err)
		}
		var starts *time.Time
		if !e.SaleStarts.IsZero() {
			starts = &e.SaleStarts
		}
		if _, err := db.Exec(ctx, upsertEventSQL, e.ID, e.Name, e.Category, e.Venue, e.City, date, e.Time, e.ImageURL, e.Description, starts); err != nil {
			return fmt.Errorf("event %s: %w", e.ID, err)
		}
		for i, c := range e.Categories {
			if _, err := db.Exec(ctx, upsertCategorySQL, c.ID, e.ID, c.Name, c.Description, c.Price, c.Capacity, c.Available, c.MaxPerOrder, i+1); err != nil {
				return fmt.Errorf("category %s: %w", c.ID, err)
			}
		}
	}
	return nil
}
