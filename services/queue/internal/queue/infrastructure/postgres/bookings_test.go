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
