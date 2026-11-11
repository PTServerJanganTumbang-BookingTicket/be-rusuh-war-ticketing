package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
)

// These run on real Postgres (make verify sets DATABASE_URL); they prove the SQL, the order and the scanning.

func seed(t *testing.T) *Store {
	t.Helper()
	pool := pgtest.New(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO events (id, name, event_date, event_time, venue, location_city, category, description, sale_starts_at)
		  VALUES ('old', 'Old', '2026-01-10', '19:00', 'GBK', 'Jakarta', 'LIVE MUSIC', 'd', '2025-12-01T02:00:00Z')`,
		`INSERT INTO events (id, name, event_date) VALUES ('b', 'B', '2026-12-25'), ('a', 'A', '2026-12-25')`,
		`INSERT INTO ticket_categories (id, event_id, name, price, capacity, available, sort_order)
		  VALUES ('vip', 'a', 'VIP', 1500000, 100, 12, 0), ('fest', 'a', 'Festival', 850000, 500, 240, 1), ('trib', 'a', 'Tribune', 500000, 500, 0, 2)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return NewStore(pool)
}

func TestListIsNewestFirstThenByIDAndPagesInSQLWithTheTotal(t *testing.T) {
	s := seed(t)
	got, total, err := s.List(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "old" {
		t.Fatalf("total %d order = %+v", total, got)
	}
	if got[2].City != "Jakarta" || got[2].Category != "LIVE MUSIC" || got[2].Time != "19:00" ||
		got[2].Date != time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("old = %+v", got[2])
	}
	if got[2].SaleStartsAt == nil || got[0].SaleStartsAt != nil {
		t.Fatalf("sale_starts_at: old %v, a %v (must be nil until scheduled)", got[2].SaleStartsAt, got[0].SaleStartsAt)
	}
	page, total, _ := s.List(context.Background(), 1, 1)
	if len(page) != 1 || page[0].ID != "b" || total != 3 {
		t.Fatalf("limit 1 offset 1 = %+v total %d", page, total)
	}
	page, total, _ = s.List(context.Background(), 10, 5)
	if len(page) != 0 || total != 3 {
		t.Fatalf("past the end = %+v total %d", page, total)
	}
}

func TestGetReturnsTheEventWithItsCategoriesInSortOrder(t *testing.T) {
	s := seed(t)
	ev, cats, ok, err := s.Get(context.Background(), "a")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ev.ID != "a" || len(cats) != 3 || cats[0].ID != "vip" || cats[1].ID != "fest" || cats[2].ID != "trib" ||
		cats[0].Price != 1500000 || cats[2].Available != 0 {
		t.Fatalf("event %+v categories %+v", ev, cats)
	}
}

func TestGetOfAnUnknownEventIsNotAnError(t *testing.T) {
	s := seed(t)
	_, _, ok, err := s.Get(context.Background(), "nope")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestStoreReturnsErrorsWhenTheDatabaseIsGone(t *testing.T) {
	s := seed(t)
	s.pool.Close()
	if _, _, err := s.List(context.Background(), 1, 0); err == nil {
		t.Fatal("List must fail on a closed pool")
	}
	if _, _, _, err := s.Get(context.Background(), "a"); err == nil {
		t.Fatal("Get must fail on a closed pool")
	}
}
