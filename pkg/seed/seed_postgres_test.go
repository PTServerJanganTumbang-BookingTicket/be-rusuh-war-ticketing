package seed_test

import (
	"context"
	"testing"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/seed"
)

// Real Postgres (DATABASE_URL, make verify): the SQL, the constraints and the catalog columns.

func TestPostgresSeedLandsInTheCatalogAndAReSeedKeepsTheLiveStockSnapshot(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	evs := seed.JakartaEvents()
	if err := seed.Postgres(ctx, pool, evs); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE location_city = 'Jakarta' AND image_url LIKE 'https://%'`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("catalog rows = %d (%v)", n, err)
	}
	var cats int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ticket_categories`).Scan(&cats); err != nil || cats != 11 {
		t.Fatalf("categories = %d (%v)", cats, err)
	}
	var first string
	if err := pool.QueryRow(ctx, `SELECT id FROM ticket_categories WHERE event_id = 'evt_jkt_istora_nightfall' ORDER BY sort_order LIMIT 1`).Scan(&first); err != nil || first != "cat_istora_vip" {
		t.Fatalf("first category = %q (%v)", first, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE ticket_categories SET available = 42 WHERE id = 'cat_istora_vip'`); err != nil {
		t.Fatal(err)
	}
	evs[0].Name = "Nightfall Live (renamed)"
	evs[0].Categories[0].Price = 1_600_000
	if err := seed.Postgres(ctx, pool, evs); err != nil {
		t.Fatal(err)
	}
	var name string
	var price, avail int64
	if err := pool.QueryRow(ctx, `SELECT e.name, c.price, c.available FROM events e JOIN ticket_categories c ON c.event_id = e.id WHERE c.id = 'cat_istora_vip'`).Scan(&name, &price, &avail); err != nil {
		t.Fatal(err)
	}
	if name != "Nightfall Live (renamed)" || price != 1_600_000 {
		t.Fatalf("the re-seed did not update the row: %q %d", name, price)
	}
	if avail != 42 {
		t.Fatalf("available = %d, want the live 42 kept", avail)
	}
}
