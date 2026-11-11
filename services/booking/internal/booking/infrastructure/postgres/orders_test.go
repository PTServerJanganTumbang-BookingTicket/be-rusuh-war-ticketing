package postgres

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

// These run on real Postgres (pgtest skips them without DATABASE_URL, as in CI): they prove the SQL of the booking
// transaction. orders_unit_test.go covers its Go side on a fake.

// bookingAs seeds a booking in any status. contact_email is always set: the schema requires it from HELD on, and a
// SELECTING row may have one too.
func bookingAs(t *testing.T, pool *pgxpool.Pool, id, status string) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, granted_at, booking_expires_at, contact_email)
		VALUES ($1, 'evt_a', 'qt_'||$1, $2, 'gt_'||$1, $3, $4, 'seed@example.com')`, id, status, grantDeadline.Add(-5*time.Minute), grantDeadline)
	if err != nil {
		t.Fatal(err)
	}
}

func eventAndBooking(t *testing.T, pool *pgxpool.Pool, status string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO events (id, name, event_date, venue, image_url) VALUES ('evt_a', 'Konser A', '2026-12-25', 'GBK', 'https://cdn/a.jpg')`); err != nil {
		t.Fatal(err)
	}
	bookingAs(t, pool, "RB-1", status)
}

func countOf(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func heldHold(counter *atomic.Int32) func(context.Context) (domain.HoldAnswer, error) {
	return func(context.Context) (domain.HoldAnswer, error) {
		counter.Add(1)
		return domain.HoldAnswer{Code: domain.HoldHeld, Index: -1}, nil
	}
}

func TestPlaceWritesTheHeldBookingWithItsDeadlineItsSeatsInOrderAndItsClaims(t *testing.T) {
	pool := pgtest.New(t)
	eventAndBooking(t, pool, "SELECTING")
	var holds atomic.Int32
	res, err := NewStore(pool).Place(ctx, placement(), heldHold(&holds))
	if err != nil || res.Outcome != domain.Placed || holds.Load() != 1 {
		t.Fatalf("result = %+v (%v) holds=%d", res, err, holds.Load())
	}
	var (
		status, email string
		heldAt, due   time.Time
	)
	if err := pool.QueryRow(ctx, `SELECT status, contact_email, held_at, payment_expires_at FROM bookings WHERE id='RB-1'`).Scan(&status, &email, &heldAt, &due); err != nil {
		t.Fatal(err)
	}
	if status != "HELD" || email != "buyer@example.com" || due.Sub(heldAt) != 10*time.Minute {
		t.Fatalf("status=%s email=%s countdown=%v, want HELD with a 10-minute payment window from held_at", status, email, due.Sub(heldAt))
	}
	rows, err := pool.Query(ctx, `SELECT id, category_name, unit_price, nik_hmac, nik_masked FROM booking_items WHERE booking_id='RB-1' ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id, cat, h, m string
		var price int64
		if err := rows.Scan(&id, &cat, &price, &h, &m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id+":"+cat)
	}
	if !reflect.DeepEqual(ids, []string{"BI-1:VIP", "BI-2:Festival"}) {
		t.Fatalf("items in created_at order = %v, want the order they were chosen in", ids)
	}
	if n := countOf(t, pool, `SELECT count(*) FROM nik_claims WHERE booking_id='RB-1' AND event_id='evt_a'`); n != 2 {
		t.Fatalf("claims = %d, want one per seat", n)
	}
}

func TestTwoConcurrentPlacesOfOneBookingHoldOnceAndBothAnswer(t *testing.T) {
	pool := pgtest.New(t)
	eventAndBooking(t, pool, "SELECTING")
	s := NewStore(pool)
	var (
		wg      sync.WaitGroup
		holds   atomic.Int32
		placed  atomic.Int32
		already atomic.Int32
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.Place(ctx, placement(), heldHold(&holds))
			switch {
			case err != nil:
				t.Errorf("place: %v", err)
			case res.Outcome == domain.Placed:
				placed.Add(1)
			case res.Outcome == domain.AlreadyPlaced:
				already.Add(1)
			default:
				t.Errorf("outcome %v", res.Outcome)
			}
		}()
	}
	wg.Wait()
	if holds.Load() != 1 || placed.Load() != 1 || already.Load() != 1 {
		t.Fatalf("holds=%d placed=%d already=%d, want one hold, one placed, one idempotent retry", holds.Load(), placed.Load(), already.Load())
	}
	if n := countOf(t, pool, `SELECT count(*) FROM booking_items WHERE booking_id='RB-1'`); n != 2 {
		t.Fatalf("items = %d, want 2 (no second set of seats)", n)
	}
}

// Redis missed a NIK (a flush, a failover): the database refuses it, nothing is written, the booking stays SELECTING.
func TestANIKAnotherBookingAlreadyClaimedIsACommittedNothingAndNamesTheSeat(t *testing.T) {
	pool := pgtest.New(t)
	eventAndBooking(t, pool, "SELECTING")
	bookingAs(t, pool, "RB-9", "HELD")
	if _, err := pool.Exec(ctx, `INSERT INTO nik_claims (event_id, nik_hmac, booking_id) VALUES ('evt_a', 'hmac-b', 'RB-9')`); err != nil {
		t.Fatal(err)
	}
	res, err := NewStore(pool).Place(ctx, placement(), heldHold(new(atomic.Int32)))
	if err != nil || res.Outcome != domain.ClaimConflict || res.ConflictIndex != 1 {
		t.Fatalf("result = %+v (%v), want ClaimConflict at seat 1", res, err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id='RB-1'`).Scan(&status)
	if status != "SELECTING" || countOf(t, pool, `SELECT count(*) FROM booking_items WHERE booking_id='RB-1'`) != 0 ||
		countOf(t, pool, `SELECT count(*) FROM nik_claims WHERE booking_id='RB-1'`) != 0 {
		t.Fatalf("status=%s: the whole transaction must have rolled back (no HELD, no seats, no claims)", status)
	}
}

func TestAnythingButSelectingInTimeIsAnsweredBeforeTheHoldRuns(t *testing.T) {
	cases := []struct {
		name   string
		status string
		late   bool
		want   domain.PlaceOutcome
	}{
		{"never opened", "GRANTED", false, domain.NotOpened},
		{"never opened and late", "GRANTED", true, domain.TimeOver},
		{"selecting and late", "SELECTING", true, domain.TimeOver},
		{"already held", "HELD", false, domain.AlreadyPlaced},
		{"confirmed", "CONFIRMED", false, domain.AlreadyPlaced},
		{"lapsed", "LAPSED", false, domain.Gone},
		{"expired by the sweep", "EXPIRED", false, domain.Gone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := pgtest.New(t)
			eventAndBooking(t, pool, tc.status)
			if tc.late {
				if _, err := pool.Exec(ctx, `UPDATE bookings SET booking_expires_at = now() - interval '1 second' WHERE id = 'RB-1'`); err != nil {
					t.Fatal(err)
				}
			}
			var holds atomic.Int32
			res, err := NewStore(pool).Place(ctx, placement(), heldHold(&holds))
			if err != nil || res.Outcome != tc.want || holds.Load() != 0 {
				t.Fatalf("result = %+v (%v) holds=%d, want %v without a hold", res, err, holds.Load(), tc.want)
			}
		})
	}
}

func TestAnotherGrantsTokenFindsNoBooking(t *testing.T) {
	pool := pgtest.New(t)
	eventAndBooking(t, pool, "SELECTING")
	p := placement()
	p.GrantToken = "gt_of_someone_else"
	res, err := NewStore(pool).Place(ctx, p, heldHold(new(atomic.Int32)))
	if err != nil || res.Outcome != domain.Gone {
		t.Fatalf("result = %+v (%v), want Gone: the row is bound to id AND grant token", res, err)
	}
}

func TestARefusedHoldLeavesTheBookingSelecting(t *testing.T) {
	pool := pgtest.New(t)
	eventAndBooking(t, pool, "SELECTING")
	res, err := NewStore(pool).Place(ctx, placement(), func(context.Context) (domain.HoldAnswer, error) {
		return domain.HoldAnswer{Code: domain.HoldNoStock, Index: 0}, nil
	})
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id='RB-1'`).Scan(&status)
	if err != nil || res.Outcome != domain.Refused || status != "SELECTING" {
		t.Fatalf("result = %+v (%v) status=%s", res, err, status)
	}
}

func TestTheStatusReadUsesTheDatabaseClockForTheDeadline(t *testing.T) {
	pool := pgtest.New(t)
	eventAndBooking(t, pool, "SELECTING")
	s := NewStore(pool)
	st, found, err := s.Status(ctx, "RB-1", "gt_RB-1")
	if err != nil || !found || st != (domain.BookingStatus{Status: bookingstate.Selecting, PastDeadline: false}) {
		t.Fatalf("in time: %+v found=%v err=%v", st, found, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bookings SET booking_expires_at = now() - interval '1 second' WHERE id = 'RB-1'`); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := s.Status(ctx, "RB-1", "gt_RB-1"); !st.PastDeadline {
		t.Fatal("a deadline in the past must read as passed")
	}
	if _, found, _ := s.Status(ctx, "RB-1", "wrong-token"); found {
		t.Fatal("the read is bound to the grant token")
	}
}

func TestCategoriesAndStoredIdentitiesOfAnAccount(t *testing.T) {
	pool := pgtest.New(t)
	for _, q := range []string{
		`INSERT INTO events (id, name, event_date, venue, image_url) VALUES ('evt_a', 'Konser A', '2026-12-25', 'GBK', 'https://cdn/a.jpg')`,
		`INSERT INTO ticket_categories (id, event_id, name, description, price, capacity, available, max_per_order, sort_order) VALUES
		   ('cat_trib', 'evt_a', 'Tribune', 'Tribun', 500000, 10, 10, 3, 3),
		   ('cat_vip',  'evt_a', 'VIP', 'Dekat', 1500000, 10, 10, 2, 1)`,
		`INSERT INTO accounts (id, email, nik_hmac, nik_masked, nik_verified_at) VALUES ('acc_1', 'a@example.com', 'hmac-own', '3171**********22', now())`,
		`INSERT INTO accounts (id, email) VALUES ('acc_2', 'b@example.com')`,
		`INSERT INTO saved_niks (id, account_id, nik_hmac, nik_masked, label, verified_at, dukcapil_ref) VALUES
		   ('sn_1', 'acc_1', 'hmac-s1', '3171**********33', 'Teman', now(), 'r1'),
		   ('sn_x', 'acc_2', 'hmac-s2', '3171**********44', 'Lain', now(), 'r2')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s := NewStore(pool)
	cats, err := s.Categories(ctx, "evt_a")
	if err != nil || len(cats) != 2 || cats[0].ID != "cat_vip" || cats[1].ID != "cat_trib" || cats[0].Price != 1500000 {
		t.Fatalf("categories = %+v (%v), want VIP then Tribune", cats, err)
	}
	ids, err := s.Identities(ctx, "acc_1", []string{"sn_1", "sn_x", "sn_nope"})
	if err != nil || ids.Own == nil || ids.Own.NIKHmac != "hmac-own" {
		t.Fatalf("own = %+v (%v)", ids.Own, err)
	}
	if _, ok := ids.Saved["sn_1"]; !ok || len(ids.Saved) != 1 {
		t.Fatalf("saved = %+v, want only this account's sn_1 (another account's id looks unknown)", ids.Saved)
	}
	if ids, err := s.Identities(ctx, "acc_2", nil); err != nil || ids.Own != nil {
		t.Fatalf("an account without a verified NIK has no own identity: %+v (%v)", ids, err)
	}
}
