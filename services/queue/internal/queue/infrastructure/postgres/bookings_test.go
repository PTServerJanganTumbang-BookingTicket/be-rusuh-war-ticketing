package postgres

import (
	"context"
	"testing"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
)

func TestByQueueTokenFindsOnlyTheMatchingEventAndToken(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status) VALUES ('B1', 'E1', 'qt1', 'GRANTED')`); err != nil {
		t.Fatal(err)
	}
	store := NewBookings(pool)

	b, found, err := store.ByQueueToken(ctx, "E1", "qt1")
	if err != nil || !found || b.Status != "GRANTED" {
		t.Fatalf("matching row: %+v found=%v err=%v", b, found, err)
	}
	for _, c := range []struct{ event, token string }{{"E2", "qt1"}, {"E1", "other"}} {
		if _, found, err := store.ByQueueToken(ctx, c.event, c.token); err != nil || found {
			t.Errorf("%s/%s: found=%v err=%v, want not found", c.event, c.token, found, err)
		}
	}
}

func TestByQueueTokenReportsDatabaseErrors(t *testing.T) {
	pool := pgtest.New(t)
	pool.Close() // every later query fails
	if _, _, err := NewBookings(pool).ByQueueToken(context.Background(), "E1", "qt1"); err == nil {
		t.Fatal("a closed pool read as 'not found'")
	}
}

func TestNewPoolRejectsABadDSN(t *testing.T) {
	if _, err := NewPool(context.Background(), "not a dsn %%", 2, 0); err == nil {
		t.Fatal("a malformed DATABASE_URL was accepted")
	}
}

func TestDetailReturnsTheFieldsMeNeeds(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, booking_expires_at, payment_expires_at, payment_ref, contact_email)
		VALUES ('RB-1', 'E1', 'qt1', 'AWAITING_PAYMENT', 'gt_1', '2026-11-10T19:05:00Z', '2026-11-10T19:10:00Z', 'pay_1', 'a@example.com'),
		       ('RB-2', 'E1', 'qt2', 'GRANTED', NULL, NULL, NULL, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	store := NewBookings(pool)

	d, found, err := store.Detail(ctx, "E1", "qt1")
	if err != nil || !found || d.ID != "RB-1" || d.Status != "AWAITING_PAYMENT" || d.GrantToken != "gt_1" || d.PaymentRef != "pay_1" ||
		d.BookingExpiresAt.UTC().Format("2006-01-02T15:04:05Z") != "2026-11-10T19:05:00Z" || d.PaymentExpiresAt.UTC().Format("15:04") != "19:10" {
		t.Fatalf("detail %+v found=%v err=%v", d, found, err)
	}
	// NULL columns read as zero values, not as an error.
	if d, found, err := store.Detail(ctx, "E1", "qt2"); err != nil || !found || d.GrantToken != "" || d.PaymentRef != "" || !d.PaymentExpiresAt.IsZero() {
		t.Fatalf("null columns: %+v found=%v err=%v", d, found, err)
	}
	for _, c := range []struct{ event, token string }{{"E2", "qt1"}, {"E1", "nope"}} {
		if _, found, err := store.Detail(ctx, c.event, c.token); err != nil || found {
			t.Errorf("%s/%s found=%v err=%v", c.event, c.token, found, err)
		}
	}
}

func TestDetailReportsDatabaseErrors(t *testing.T) {
	pool := pgtest.New(t)
	pool.Close()
	if _, _, err := NewBookings(pool).Detail(context.Background(), "E1", "qt1"); err == nil {
		t.Fatal("a closed pool read as 'not found'")
	}
}
