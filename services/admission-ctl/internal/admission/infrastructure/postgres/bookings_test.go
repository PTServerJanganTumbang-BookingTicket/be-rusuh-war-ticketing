package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)
)

func grant(event, qt, id, gt string) domain.Grant {
	return domain.Grant{EventID: event, QueueToken: qt, BookingID: id, GrantToken: gt, GrantedAt: t0, ExpiresAt: t0.Add(5 * time.Minute)}
}

func seed(t *testing.T, pool *pgxpool.Pool, event, status, qt string, expires time.Time) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, terms_accepted_at, booking_expires_at)
		VALUES ($1, $2, $3, $4, $5, 'a@example.com', now(), $6)`, "id-"+qt, event, qt, status, "gt-"+qt, expires)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInsertGrantedIsIdempotentPerCustomer(t *testing.T) {
	pool := pgtest.New(t)
	s := NewBookings(pool)
	first, err := s.InsertGranted(ctx, grant("E1", "q1", "B1", "G1"))
	if err != nil || first.ID != "B1" || first.Status != "GRANTED" {
		t.Fatalf("first insert %+v %v", first, err)
	}
	again, err := s.InsertGranted(ctx, grant("E1", "q1", "B2", "G2"))
	if err != nil || again.ID != "B1" || again.GrantToken != "G1" || !again.GrantedAt.Equal(t0) || !again.ExpiresAt.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("second insert should return the first row, got %+v %v", again, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE event_id='E1' AND queue_token='q1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d (%v), want 1", n, err)
	}
}

func TestInsertGrantedStoresAGuestWithoutAccount(t *testing.T) {
	pool := pgtest.New(t)
	if _, err := NewBookings(pool).InsertGranted(ctx, grant("E1", "guest", "B1", "G1")); err != nil {
		t.Fatal(err)
	}
	var account *string
	if err := pool.QueryRow(ctx, `SELECT account_id FROM bookings WHERE id='B1'`).Scan(&account); err != nil || account != nil {
		t.Fatalf("account_id = %v (%v), want NULL", account, err)
	}
}

func TestInsertGrantedReusedGrantTokenIsAUniqueViolation(t *testing.T) {
	pool := pgtest.New(t)
	s := NewBookings(pool)
	if _, err := s.InsertGranted(ctx, grant("E1", "q1", "B1", "G1")); err != nil {
		t.Fatal(err)
	}
	_, err := s.InsertGranted(ctx, grant("E1", "q2", "B2", "G1"))
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		t.Fatalf("error = %v, want a unique violation", err)
	}
}

func TestInsertGrantedReadsBackARowWithoutGrantToken(t *testing.T) {
	pool := pgtest.New(t)
	if _, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, granted_at, booking_expires_at)
		VALUES ('B0', 'E1', 'q1', 'GRANTED', $1, $2)`, t0, t0.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := NewBookings(pool).InsertGranted(ctx, grant("E1", "q1", "B1", "G1"))
	if err != nil || got.ID != "B0" || got.GrantToken != "" {
		t.Fatalf("got %+v %v, want the old row with an empty grant_token", got, err)
	}
}

func TestInsertGrantedConcurrentRetriesLeaveOneRow(t *testing.T) {
	pool := pgtest.New(t)
	s := NewBookings(pool)
	var wg sync.WaitGroup
	ids := make([]string, 6)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, err := s.InsertGranted(ctx, grant("E1", "q1", fmt.Sprintf("B%d", i), fmt.Sprintf("G%d", i)))
			if err != nil {
				t.Errorf("insert %d: %v", i, err)
			}
			ids[i] = b.ID
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("callers got different bookings: %v", ids)
		}
	}
}

func TestCountAdmissionCountsOnlyActiveRowsOfOneEvent(t *testing.T) {
	pool := pgtest.New(t)
	far := t0.Add(24 * time.Hour)
	for i, st := range []string{"GRANTED", "SELECTING", "HELD", "AWAITING_PAYMENT", "PENDING_CANCELLATION", "CONFIRMED", "EXPIRED", "LAPSED", "ABANDONED"} {
		seed(t, pool, "E1", st, fmt.Sprintf("q%d", i), far)
	}
	seed(t, pool, "E2", "GRANTED", "other", far)
	got, err := NewBookings(pool).CountAdmission(ctx, "E1")
	if err != nil || got.Open != 5 || got.Browsing != 2 {
		t.Fatalf("count %+v %v, want open 5 browsing 2", got, err)
	}
	if got, _ := NewBookings(pool).CountAdmission(ctx, "none"); got.Open != 0 || got.Browsing != 0 {
		t.Fatalf("an unknown event counted %+v", got)
	}
}

func TestLapseDueMovesOnlyDueGrantedRowsAndOnlyOnce(t *testing.T) {
	pool := pgtest.New(t)
	seed(t, pool, "E1", "GRANTED", "due", t0.Add(-time.Second))
	seed(t, pool, "E1", "GRANTED", "exactly", t0)
	seed(t, pool, "E1", "GRANTED", "later", t0.Add(time.Minute))
	seed(t, pool, "E1", "SELECTING", "selecting", t0.Add(-time.Hour))
	s := NewBookings(pool)

	lapsed, err := s.LapseDue(ctx, t0)
	if err != nil || len(lapsed) != 2 {
		t.Fatalf("lapsed %+v %v, want the two due rows", lapsed, err)
	}
	for _, l := range lapsed {
		if l.GrantToken == "" || l.EventID != "E1" || l.QueueToken == "" {
			t.Fatalf("lapsed row misses fields: %+v", l)
		}
	}
	if again, _ := s.LapseDue(ctx, t0); len(again) != 0 {
		t.Fatalf("the same rows lapsed twice: %+v", again)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM bookings WHERE queue_token='selecting'`).Scan(&status); err != nil || status != "SELECTING" {
		t.Fatalf("a SELECTING row changed to %q (%v)", status, err)
	}
}

func TestLapseDueGivesEachRowToExactlyOneSweeper(t *testing.T) {
	pool := pgtest.New(t)
	for i := 0; i < 20; i++ {
		seed(t, pool, "E1", "GRANTED", fmt.Sprintf("q%d", i), t0.Add(-time.Minute))
	}
	s := NewBookings(pool)
	var wg sync.WaitGroup
	counts := make([]int, 2)
	for i := range counts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, err := s.LapseDue(ctx, t0)
			if err != nil {
				t.Errorf("sweeper %d: %v", i, err)
			}
			counts[i] = len(l)
		}(i)
	}
	wg.Wait()
	if counts[0]+counts[1] != 20 {
		t.Fatalf("sweepers lapsed %v, want 20 in total", counts)
	}
}

func TestStoreReportsDatabaseErrors(t *testing.T) {
	pool := pgtest.New(t)
	s := NewBookings(pool)
	pool.Close()
	if _, err := s.CountAdmission(ctx, "E1"); err == nil {
		t.Error("CountAdmission hid a database error")
	}
	if _, err := s.InsertGranted(ctx, grant("E1", "q", "B", "G")); err == nil {
		t.Error("InsertGranted hid a database error")
	}
	if _, err := s.LapseDue(ctx, t0); err == nil {
		t.Error("LapseDue hid a database error")
	}
}
