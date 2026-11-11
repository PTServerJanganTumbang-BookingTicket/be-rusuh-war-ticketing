package postgres

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// Real Postgres: the expected stock counts exactly the statuses that take seats.

// withItems seeds a booking in `status` with one booking_items row per category listed (one seat each).
func withItems(t *testing.T, pool *pgxpool.Pool, event, qt, status string, categories ...string) {
	t.Helper()
	id := "id-" + qt
	if _, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, booking_expires_at, payment_expires_at)
		VALUES ($1, $2, $3, $4, $5, 'a@example.com', $6, $6)`, id, event, qt, status, "gt-"+qt, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i, c := range categories {
		if _, err := pool.Exec(ctx, `INSERT INTO booking_items (id, booking_id, event_id, category_id, category_name, unit_price, nik_hmac, nik_masked)
			VALUES ($1, $2, $3, $4, $4, 1000, $5, 'm')`, fmt.Sprintf("%s-%d", id, i), id, event, c, fmt.Sprintf("%s-n%d", qt, i)); err != nil {
			t.Fatal(err)
		}
	}
}

func seedCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO events (id, name, event_date, venue, image_url) VALUES ('E1', 'A', '2026-12-25', 'v', 'u'), ('E2', 'B', '2026-12-26', 'v', 'u')`,
		`INSERT INTO ticket_categories (id, event_id, name, price, capacity, available, sort_order) VALUES
		   ('FEST', 'E1', 'Festival', 850000, 20, 20, 2),
		   ('VIP',  'E1', 'VIP', 1500000, 10, 10, 1),
		   ('OTHER', 'E2', 'Other', 1000, 5, 5, 1)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExpectedStockCountsOnlyTheStatusesThatTakeSeats(t *testing.T) {
	pool := pgtest.New(t)
	seedCatalog(t, pool)
	withItems(t, pool, "E1", "held", "HELD", "VIP", "VIP")
	withItems(t, pool, "E1", "paying", "AWAITING_PAYMENT", "VIP")
	withItems(t, pool, "E1", "grace", "PENDING_CANCELLATION", "FEST")
	withItems(t, pool, "E1", "paid", "CONFIRMED", "FEST", "FEST")
	// seats of bookings that no longer hold them, and of another event: never counted
	withItems(t, pool, "E1", "gone", "EXPIRED", "VIP", "VIP", "VIP")
	withItems(t, pool, "E1", "lapsed", "LAPSED", "FEST")
	withItems(t, pool, "E1", "choosing", "SELECTING", "VIP")
	withItems(t, pool, "E2", "elsewhere", "CONFIRMED", "OTHER", "OTHER")

	got, err := NewBookings(pool).StockExpectations(ctx, "E1")
	want := []domain.StockExpectation{{CategoryID: "VIP", Capacity: 10, Taken: 3}, {CategoryID: "FEST", Capacity: 20, Taken: 3}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v (%v), want %+v (VIP before FEST: display order; PENDING_CANCELLATION counts, EXPIRED does not)", got, err, want)
	}
	if got[0].Expected() != 7 || got[1].Expected() != 17 {
		t.Fatalf("expected %d / %d, want 7 / 17", got[0].Expected(), got[1].Expected())
	}
}

func TestACategoryWithNoBookingsIsStillListedWithNothingTaken(t *testing.T) {
	pool := pgtest.New(t)
	seedCatalog(t, pool)
	got, err := NewBookings(pool).StockExpectations(ctx, "E1")
	if err != nil || len(got) != 2 || got[0].Taken != 0 || got[1].Taken != 0 {
		t.Fatalf("got %+v (%v), want both categories with 0 taken (the list comes from ticket_categories)", got, err)
	}
	if none, err := NewBookings(pool).StockExpectations(ctx, "E_NONE"); err != nil || len(none) != 0 {
		t.Fatalf("an event without categories: %+v (%v)", none, err)
	}
}

func TestClaimedNIKsOfOneEvent(t *testing.T) {
	pool := pgtest.New(t)
	seedCatalog(t, pool)
	seedHeld(t, pool, "E1", "a", t0.Add(time.Hour), heldSeat{"VIP", "n1"}, heldSeat{"VIP", "n2"})
	seedHeld(t, pool, "E2", "b", t0.Add(time.Hour), heldSeat{"OTHER", "n9"})
	got, err := NewBookings(pool).ClaimedNIKs(ctx, "E1")
	sort.Strings(got)
	if err != nil || !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("got %v (%v), want the claims of E1 only", got, err)
	}
}
