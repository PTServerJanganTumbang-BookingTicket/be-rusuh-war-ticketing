package postgres

import (
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

// The claim compares the deadline with Aurora's clock, so the seeded deadline is in the real future.
var grantDeadline = time.Now().UTC().Add(4 * time.Minute).Truncate(time.Second)

func granted(t *testing.T, pool *pgxpool.Pool, id, status string) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, granted_at, booking_expires_at)
		VALUES ($1, 'evt_a', 'qt_'||$1, $2, 'gt_'||$1, $3, $4)`, id, status, grantDeadline.Add(-5*time.Minute), grantDeadline)
	if err != nil {
		t.Fatal(err)
	}
}

func TestClaimMovesGrantedToSelectingOnceAndKeepsTheDeadline(t *testing.T) {
	pool := pgtest.New(t)
	granted(t, pool, "RB-1", "GRANTED")
	s := NewStore(pool)

	b, found, err := s.ClaimForSelection(ctx, "RB-1", "gt_RB-1")
	if err != nil || !found || b.Status != bookingstate.Selecting || !b.BookingExpiresAt.Equal(grantDeadline) || b.EventID != "evt_a" {
		t.Fatalf("claim = %+v found=%v err=%v", b, found, err)
	}
	var claimedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT slot_claimed_at FROM bookings WHERE id='RB-1'`).Scan(&claimedAt); err != nil || claimedAt == nil {
		t.Fatalf("slot_claimed_at = %v (%v), want it recorded", claimedAt, err)
	}
	first := *claimedAt

	if b, _, err := s.ClaimForSelection(ctx, "RB-1", "gt_RB-1"); err != nil || b.Status != bookingstate.Selecting {
		t.Fatalf("reload = %+v %v", b, err)
	}
	if err := pool.QueryRow(ctx, `SELECT slot_claimed_at FROM bookings WHERE id='RB-1'`).Scan(&claimedAt); err != nil || !claimedAt.Equal(first) {
		t.Fatalf("a reload moved slot_claimed_at from %v to %v", first, claimedAt)
	}
}

func TestClaimLeavesALaterStatusAsItIs(t *testing.T) {
	pool := pgtest.New(t)
	granted(t, pool, "RB-1", "LAPSED")
	b, found, err := NewStore(pool).ClaimForSelection(ctx, "RB-1", "gt_RB-1")
	if err != nil || !found || b.Status != bookingstate.Lapsed {
		t.Fatalf("claim of a lapsed booking = %+v found=%v err=%v, want it read back unchanged", b, found, err)
	}
}

func TestClaimNeedsTheMatchingGrantToken(t *testing.T) {
	pool := pgtest.New(t)
	granted(t, pool, "RB-1", "GRANTED")
	if _, found, err := NewStore(pool).ClaimForSelection(ctx, "RB-1", "gt_someone_else"); err != nil || found {
		t.Fatalf("found=%v err=%v, want no row for another grant", found, err)
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id='RB-1'`).Scan(&st); err != nil || st != "GRANTED" {
		t.Fatalf("status %q (%v): another grant's token must not claim the booking", st, err)
	}
}

func TestTwoConcurrentLoadsClaimOnceAndBothRead(t *testing.T) {
	pool := pgtest.New(t)
	granted(t, pool, "RB-1", "GRANTED")
	s := NewStore(pool)
	var wg sync.WaitGroup
	results := make([]domain.Booking, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, found, err := s.ClaimForSelection(ctx, "RB-1", "gt_RB-1")
			if err != nil || !found {
				t.Errorf("load %d: found=%v err=%v", i, found, err)
			}
			results[i] = b
		}(i)
	}
	wg.Wait()
	for i, b := range results {
		if b.Status != bookingstate.Selecting || !b.BookingExpiresAt.Equal(grantDeadline) {
			t.Errorf("load %d read %+v, want SELECTING with the grant deadline", i, b)
		}
	}
}

func TestTheCatalogReadsTheEventAndItsCategoriesInDisplayOrder(t *testing.T) {
	pool := pgtest.New(t)
	for _, q := range []string{
		`INSERT INTO events (id, name, event_date, venue, image_url) VALUES ('evt_a', 'Konser A', '2026-12-25', 'GBK', 'https://cdn/a.jpg')`,
		`INSERT INTO ticket_categories (id, event_id, name, description, price, capacity, available, max_per_order, sort_order) VALUES
		   ('cat_trib', 'evt_a', 'Tribune', 'Tribun', 500000, 10, 10, 3, 3),
		   ('cat_vip',  'evt_a', 'VIP', 'Dekat', 1500000, 10, 10, 2, 1),
		   ('cat_fest', 'evt_a', 'Festival', '', 850000, 10, 10, 3, 2)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s := NewStore(pool)
	e, cats, found, err := s.Page(ctx, "evt_a")
	if err != nil || !found || e != (domain.Event{ID: "evt_a", Name: "Konser A", Date: "2026-12-25", Venue: "GBK", ImageURL: "https://cdn/a.jpg"}) {
		t.Fatalf("event %+v found=%v err=%v", e, found, err)
	}
	if len(cats) != 3 || cats[0].ID != "cat_vip" || cats[1].ID != "cat_fest" || cats[2].ID != "cat_trib" {
		t.Fatalf("categories %+v, want VIP, Festival, Tribune", cats)
	}
	if cats[0] != (domain.Category{ID: "cat_vip", Name: "VIP", Description: "Dekat", Price: 1500000, MaxPerOrder: 2}) {
		t.Fatalf("VIP = %+v", cats[0])
	}
	if _, _, found, err := s.Page(ctx, "evt_none"); err != nil || found {
		t.Fatalf("unknown event found=%v err=%v", found, err)
	}
}

// A page load after the 5 minutes ran out (before the sweep reached the row) must not turn a lapse into an
// "opened, did not pay" expiry: the row stays GRANTED and the LAPSED sweep ends it.
func TestALoadAfterTheDeadlineDoesNotClaim(t *testing.T) {
	pool := pgtest.New(t)
	granted(t, pool, "RB-1", "GRANTED")
	if _, err := pool.Exec(ctx, `UPDATE bookings SET booking_expires_at = now() - interval '1 second' WHERE id = 'RB-1'`); err != nil {
		t.Fatal(err)
	}
	b, found, err := NewStore(pool).ClaimForSelection(ctx, "RB-1", "gt_RB-1")
	if err != nil || !found || b.Status != bookingstate.Granted {
		t.Fatalf("late load = %+v found=%v err=%v, want the row read back as GRANTED", b, found, err)
	}
	var claimedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT slot_claimed_at FROM bookings WHERE id='RB-1'`).Scan(&claimedAt); err != nil || claimedAt != nil {
		t.Fatalf("slot_claimed_at = %v (%v), want none after the deadline", claimedAt, err)
	}
}

func TestTheStoreReportsDatabaseErrors(t *testing.T) {
	pool := pgtest.New(t)
	s := NewStore(pool)
	pool.Close()
	if _, _, err := s.ClaimForSelection(ctx, "RB-1", "gt"); err == nil {
		t.Error("ClaimForSelection hid a database error")
	}
	if _, _, _, err := s.Page(ctx, "evt_a"); err == nil {
		t.Error("Page hid a database error")
	}
}
