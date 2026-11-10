package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type fakeRow struct{ scan func(dest ...any) error }

func (r fakeRow) Scan(dest ...any) error { return r.scan(dest...) }

type fakeDB struct {
	row  pgx.Row
	sql  string
	args []any
}

func (f *fakeDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.sql, f.args = sql, args
	return f.row
}

func failing(err error) fakeRow { return fakeRow{func(...any) error { return err }} }

func TestByQueueTokenBindsEventAndTokenAndReturnsTheStatus(t *testing.T) {
	db := &fakeDB{row: fakeRow{func(dest ...any) error { *dest[0].(*string) = "GRANTED"; return nil }}}
	b, found, err := NewBookings(db).ByQueueToken(context.Background(), "E1", "qt1")
	if err != nil || !found || b.Status != "GRANTED" {
		t.Fatalf("got %+v found=%v err=%v", b, found, err)
	}
	if db.sql != byQueueTokenSQL || len(db.args) != 2 || db.args[0] != "E1" || db.args[1] != "qt1" {
		t.Fatalf("query %q args %v", db.sql, db.args)
	}
}

func TestByQueueTokenTreatsNoRowsAsNotFoundAndWrapsOtherErrors(t *testing.T) {
	_, found, err := NewBookings(&fakeDB{row: failing(pgx.ErrNoRows)}).ByQueueToken(context.Background(), "E", "t")
	if err != nil || found {
		t.Fatalf("no rows: found=%v err=%v", found, err)
	}
	boom := errors.New("connection reset")
	if _, _, err := NewBookings(&fakeDB{row: failing(boom)}).ByQueueToken(context.Background(), "E", "t"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the driver error", err)
	}
}

func TestDetailFillsEveryFieldAndReadsDeadlinesInUTC(t *testing.T) {
	wib := time.FixedZone("WIB", 7*3600)
	booking := time.Date(2026, 11, 10, 19, 5, 0, 0, wib)
	payBy := booking.Add(10 * time.Minute)
	db := &fakeDB{row: fakeRow{func(d ...any) error {
		*d[0].(*string), *d[1].(*string), *d[2].(*string) = "RB-1", "GRANTED", "gt"
		*d[3].(**time.Time), *d[4].(**time.Time) = &booking, &payBy
		*d[5].(*string) = "pay-1"
		return nil
	}}}
	got, found, err := NewBookings(db).Detail(context.Background(), "E1", "qt1")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if got.ID != "RB-1" || got.Status != "GRANTED" || got.GrantToken != "gt" || got.PaymentRef != "pay-1" {
		t.Fatalf("detail = %+v", got)
	}
	if !got.BookingExpiresAt.Equal(booking) || got.BookingExpiresAt.Location() != time.UTC || !got.PaymentExpiresAt.Equal(payBy) {
		t.Fatalf("deadlines = %v / %v", got.BookingExpiresAt, got.PaymentExpiresAt)
	}
}

func TestDetailLeavesMissingDeadlinesAsTheZeroTime(t *testing.T) {
	db := &fakeDB{row: fakeRow{func(d ...any) error { *d[1].(*string) = "QUEUED"; return nil }}}
	got, found, err := NewBookings(db).Detail(context.Background(), "E1", "qt1")
	if err != nil || !found || !got.BookingExpiresAt.IsZero() || !got.PaymentExpiresAt.IsZero() {
		t.Fatalf("got %+v found=%v err=%v", got, found, err)
	}
}

func TestDetailTreatsNoRowsAsNotFoundAndWrapsOtherErrors(t *testing.T) {
	if _, found, err := NewBookings(&fakeDB{row: failing(pgx.ErrNoRows)}).Detail(context.Background(), "E", "t"); err != nil || found {
		t.Fatalf("no rows: found=%v err=%v", found, err)
	}
	boom := errors.New("timeout")
	if _, _, err := NewBookings(&fakeDB{row: failing(boom)}).Detail(context.Background(), "E", "t"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the driver error", err)
	}
}

func TestNewPoolAppliesMaxConnsAndLifetimeFromTheArguments(t *testing.T) {
	pool, err := NewPool(context.Background(), "postgres://u:p@127.0.0.1:1/db?sslmode=disable", 7, time.Minute)
	if err != nil {
		t.Fatalf("NewPool is lazy and should not dial: %v", err)
	}
	defer pool.Close()
	if c := pool.Config(); c.MaxConns != 7 || c.MaxConnLifetime != time.Minute {
		t.Fatalf("MaxConns=%d lifetime=%v", c.MaxConns, c.MaxConnLifetime)
	}
}
