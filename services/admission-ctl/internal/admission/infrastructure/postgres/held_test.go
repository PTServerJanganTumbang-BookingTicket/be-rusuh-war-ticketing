package postgres

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
)

// Real Postgres (pgtest skips them without DATABASE_URL, as in CI): they prove the SQL of the HELD sweep.

type heldSeat struct{ category, nik string }

// seedHeld writes a HELD booking due at `due`, with one booking_items row and one nik_claims row per seat.
func seedHeld(t *testing.T, pool *pgxpool.Pool, event, qt string, due time.Time, seats ...heldSeat) {
	t.Helper()
	id := "id-" + qt
	_, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, booking_expires_at, held_at, payment_expires_at)
		VALUES ($1, $2, $3, 'HELD', $4, 'a@example.com', $5, $6, $7)`, id, event, qt, "gt-"+qt, due.Add(-time.Hour), due.Add(-10*time.Minute), due)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range seats {
		if _, err := pool.Exec(ctx, `INSERT INTO booking_items (id, booking_id, event_id, category_id, category_name, unit_price, nik_hmac, nik_masked)
			VALUES ($1, $2, $3, $4, $4, 1000, $5, 'masked')`, fmt.Sprintf("%s-i%d", id, i), id, event, s.category, s.nik); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO nik_claims (event_id, nik_hmac, booking_id) VALUES ($1, $2, $3)`, event, s.nik, id); err != nil {
			t.Fatal(err)
		}
	}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExpireHeldDueEndsOnlyDueHeldBookingsFreesTheirNIKsAndReportsWhatTheyHeld(t *testing.T) {
	pool := pgtest.New(t)
	seedHeld(t, pool, "E1", "due", t0.Add(-time.Second), heldSeat{"VIP", "n1"}, heldSeat{"VIP", "n2"}, heldSeat{"FEST", "n3"})
	seedHeld(t, pool, "E1", "exactly", t0, heldSeat{"VIP", "n4"})
	seedHeld(t, pool, "E1", "later", t0.Add(time.Second), heldSeat{"VIP", "n5"})
	for _, st := range []string{"GRANTED", "SELECTING", "AWAITING_PAYMENT", "PENDING_CANCELLATION", "CONFIRMED"} {
		seed(t, pool, "E1", st, "other_"+st, t0.Add(-time.Hour))
		if st != "GRANTED" && st != "SELECTING" {
			if _, err := pool.Exec(ctx, `UPDATE bookings SET payment_expires_at = $1 WHERE queue_token = $2`, t0.Add(-time.Hour), "other_"+st); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := NewBookings(pool)

	got, err := s.ExpireHeldDue(ctx, t0)
	if err != nil || len(got) != 2 {
		t.Fatalf("expired %+v (%v), want the two due HELD bookings", got, err)
	}
	byID := map[string]int{}
	for i, h := range got {
		byID[h.BookingID] = i
		if h.GrantToken == "" || h.EventID != "E1" || h.QueueToken == "" {
			t.Fatalf("row misses fields: %+v", h)
		}
	}
	due := got[byID["id-due"]]
	if len(due.Seats) != 2 || due.Seats[0].CategoryID != "FEST" || due.Seats[0].Qty != 1 || due.Seats[1].CategoryID != "VIP" || due.Seats[1].Qty != 2 || len(due.NIKs) != 3 {
		t.Fatalf("seats/NIKs of the due booking = %+v / %v, want 2 VIP + 1 FEST and 3 NIKs", due.Seats, due.NIKs)
	}
	if again, _ := s.ExpireHeldDue(ctx, t0); len(again) != 0 {
		t.Fatalf("the same bookings expired twice: %+v", again)
	}
	var status string
	for qt, want := range map[string]string{"due": "EXPIRED", "exactly": "EXPIRED", "later": "HELD",
		"other_AWAITING_PAYMENT": "AWAITING_PAYMENT", "other_PENDING_CANCELLATION": "PENDING_CANCELLATION", "other_CONFIRMED": "CONFIRMED",
		"other_GRANTED": "GRANTED", "other_SELECTING": "SELECTING"} {
		_ = pool.QueryRow(ctx, `SELECT status FROM bookings WHERE queue_token = $1`, qt).Scan(&status)
		if status != want {
			t.Errorf("%s is %s, want %s (a payment in flight belongs to 07-payment and is never touched)", qt, status, want)
		}
	}
	// the claims of the expired bookings are gone (their NIKs are free again), the others stay; the items stay as history
	if n := count(t, pool, `SELECT count(*) FROM nik_claims WHERE booking_id IN ('id-due','id-exactly')`); n != 0 {
		t.Fatalf("%d claims of expired bookings left, want 0", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM nik_claims WHERE booking_id = 'id-later'`); n != 1 {
		t.Fatal("the claim of a booking that is not due was deleted")
	}
	if n := count(t, pool, `SELECT count(*) FROM booking_items WHERE booking_id = 'id-due'`); n != 3 {
		t.Fatalf("items = %d, want the 3 seats kept as history", n)
	}
}

// The NIK of an expired booking can be claimed again: the UNIQUE (event_id, nik_hmac) rule no longer blocks it.
func TestAFreedNIKCanBeClaimedByAnotherBooking(t *testing.T) {
	pool := pgtest.New(t)
	seedHeld(t, pool, "E1", "gone", t0.Add(-time.Second), heldSeat{"VIP", "n1"})
	if _, err := NewBookings(pool).ExpireHeldDue(ctx, t0); err != nil {
		t.Fatal(err)
	}
	seed(t, pool, "E1", "SELECTING", "next", t0.Add(time.Minute))
	if _, err := pool.Exec(ctx, `INSERT INTO nik_claims (event_id, nik_hmac, booking_id) VALUES ('E1', 'n1', 'id-next')`); err != nil {
		t.Fatalf("the freed NIK was refused: %v", err)
	}
}

func TestExpireHeldDueGivesEachBookingToExactlyOneSweeper(t *testing.T) {
	pool := pgtest.New(t)
	for i := 0; i < 20; i++ {
		seedHeld(t, pool, "E1", fmt.Sprintf("q%d", i), t0.Add(-time.Minute), heldSeat{"VIP", fmt.Sprintf("n%d", i)})
	}
	s := NewBookings(pool)
	var wg sync.WaitGroup
	counts := make([]int, 2)
	for i := range counts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, err := s.ExpireHeldDue(ctx, t0)
			if err != nil {
				t.Errorf("sweeper %d: %v", i, err)
			}
			counts[i] = len(e)
		}(i)
	}
	wg.Wait()
	if counts[0]+counts[1] != 20 {
		t.Fatalf("sweepers expired %v, want 20 in total (each booking released once)", counts)
	}
	if n := count(t, pool, `SELECT count(*) FROM nik_claims`); n != 0 {
		t.Fatalf("%d claims left after every booking expired", n)
	}
}

func TestAnExpiredHeldBookingNoLongerCountsAsInside(t *testing.T) {
	pool := pgtest.New(t)
	seedHeld(t, pool, "E1", "gone", t0.Add(-time.Second), heldSeat{"VIP", "n1"})
	seedHeld(t, pool, "E1", "stays", t0.Add(time.Minute), heldSeat{"VIP", "n2"})
	s := NewBookings(pool)
	before, err := s.CountAdmission(ctx, "E1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExpireHeldDue(ctx, t0); err != nil {
		t.Fatal(err)
	}
	after, _ := s.CountAdmission(ctx, "E1")
	if after.Open != before.Open-1 || after.Browsing != before.Browsing {
		t.Fatalf("count before %+v after %+v, want one fewer open and the same browsing (a HELD booking was never browsing)", before, after)
	}
}

func TestTheHeldSweepUsesItsPartialIndex(t *testing.T) {
	pool := pgtest.New(t)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var plan strings.Builder
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		if _, err := conn.Exec(ctx, "SET plan_cache_mode = "+mode); err != nil {
			t.Fatal(err)
		}
		rows, err := conn.Query(ctx, "EXPLAIN "+ExpireHeldSQL, t0)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(line + "\n")
		}
		rows.Close()
	}
	if strings.Count(plan.String(), "bookings_held_deadline_idx") < 2 {
		t.Fatalf("both plans must use bookings_held_deadline_idx:\n%s", plan.String())
	}
}
