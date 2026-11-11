package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// The Go side of the two reconciliation reads on a fake; reconcile_test.go proves the SQL on real Postgres.

type fakeReader struct {
	Querier // nil: reconciliation only reads
	rows    pgx.Rows
	err     error
	calls   []queryCall
}

func (f *fakeReader) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.calls = append(f.calls, queryCall{sql, args})
	return f.rows, f.err
}

func TestStockExpectationsAreReadPerCategoryInDisplayOrder(t *testing.T) {
	f := &fakeReader{rows: &fakeRows{data: [][]any{{"VIP", int64(10), int64(3)}, {"FEST", int64(20), int64(0)}}}}
	got, err := NewBookings(f).StockExpectations(ctx, "E1")
	want := []domain.StockExpectation{{CategoryID: "VIP", Capacity: 10, Taken: 3}, {CategoryID: "FEST", Capacity: 20, Taken: 0}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v (%v), want %+v", got, err, want)
	}
	sql := f.calls[0].sql
	for _, want := range []string{"ticket_categories", "'HELD'", "'AWAITING_PAYMENT'", "'PENDING_CANCELLATION'", "'CONFIRMED'", "ORDER BY c.sort_order"} {
		if !strings.Contains(sql, want) {
			t.Errorf("the query lacks %q", want)
		}
	}
	for _, not := range []string{"'SELECTING'", "'GRANTED'", "'EXPIRED'", "'LAPSED'"} {
		if strings.Contains(sql, not) {
			t.Errorf("the query counts %s: those statuses take no seats", not)
		}
	}
	if !reflect.DeepEqual(f.calls[0].args, []any{"E1"}) {
		t.Fatalf("args %v", f.calls[0].args)
	}
}

func TestClaimedNIKsAreReadForOneEvent(t *testing.T) {
	f := &fakeReader{rows: &fakeRows{data: [][]any{{"n1"}, {"n2"}}}}
	got, err := NewBookings(f).ClaimedNIKs(ctx, "E1")
	if err != nil || !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("got %v (%v)", got, err)
	}
	if !strings.Contains(f.calls[0].sql, "FROM nik_claims WHERE event_id = $1") || !reflect.DeepEqual(f.calls[0].args, []any{"E1"}) {
		t.Fatalf("call %+v", f.calls[0])
	}
}

func TestBothReadsReportEveryDatabaseError(t *testing.T) {
	// a fresh fake per call: a cursor is consumed once
	cases := map[string]func() *fakeReader{
		"query": func() *fakeReader { return &fakeReader{err: errBoom} },
		"scan": func() *fakeReader {
			return &fakeReader{rows: &fakeRows{data: [][]any{{"x", int64(1), int64(1)}}, scanErr: errBoom}}
		},
		"cursor": func() *fakeReader { return &fakeReader{rows: &fakeRows{err: errBoom}} },
	}
	for name, mk := range cases {
		if got, err := NewBookings(mk()).StockExpectations(ctx, "E1"); !errors.Is(err, errBoom) || got != nil {
			t.Errorf("expectations %s: %v %v", name, got, err)
		}
		if got, err := NewBookings(mk()).ClaimedNIKs(ctx, "E1"); !errors.Is(err, errBoom) || got != nil {
			t.Errorf("claims %s: %v %v", name, got, err)
		}
	}
}
