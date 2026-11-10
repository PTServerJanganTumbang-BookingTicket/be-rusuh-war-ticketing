package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

// These tests fake the database so they run without Postgres (CI runs without it). They cover the Go side of the
// store: scanning, status parsing, argument binding and error handling. bookings_test.go proves the SQL on real Postgres.

var (
	errDB    = errors.New("connection reset")
	deadline = time.Date(2026, 11, 10, 19, 5, 0, 0, time.UTC)
)

type call struct {
	sql  string
	args []any
}

type fakeDB struct {
	rows    []pgx.Row // consumed in order by QueryRow
	batch   pgx.BatchResults
	calls   []call
	batched []*pgx.QueuedQuery
}

func (f *fakeDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.calls = append(f.calls, call{sql, args})
	r := f.rows[0]
	f.rows = f.rows[1:]
	return r
}

func (f *fakeDB) SendBatch(_ context.Context, b *pgx.Batch) pgx.BatchResults {
	f.batched = b.QueuedQueries
	return f.batch
}

// fakeRow hands its values to Scan's destinations, like a real row would.
type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		if err := assign(d, r.vals[i]); err != nil {
			return err
		}
	}
	return nil
}

// assign stores v in *dest. nil is the zero value; a plain value is also accepted for a *T destination (a nullable column).
func assign(dest, v any) error {
	elem := reflect.ValueOf(dest).Elem()
	if v == nil {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}
	val := reflect.ValueOf(v)
	switch {
	case val.Type().AssignableTo(elem.Type()):
		elem.Set(val)
	case elem.Kind() == reflect.Pointer && val.Type().AssignableTo(elem.Type().Elem()):
		p := reflect.New(elem.Type().Elem())
		p.Elem().Set(val)
		elem.Set(p)
	default:
		return errors.New("fake scan: cannot assign " + val.Type().String() + " to " + elem.Type().String())
	}
	return nil
}

type fakeRows struct {
	data    [][]any
	i       int
	scanErr error // Scan fails on every row
	err     error // Err() after the loop
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Next() bool {
	if r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}
func (r *fakeRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	return fakeRow{vals: r.data[r.i-1]}.Scan(dest...)
}

// fakeBatch answers the two statements of Page and Detail: QueryRow for the first, Query for the second.
type fakeBatch struct {
	first    pgx.Row
	second   pgx.Rows
	queryErr error
}

func (b *fakeBatch) Exec() (pgconn.CommandTag, error) { return pgconn.CommandTag{}, nil }
func (b *fakeBatch) Query() (pgx.Rows, error)         { return b.second, b.queryErr }
func (b *fakeBatch) QueryRow() pgx.Row                { return b.first }
func (b *fakeBatch) Close() error                     { return nil }

func bookingRow(status string, booking, payment any) fakeRow {
	return fakeRow{vals: []any{"RB-1", "evt_a", status, booking, payment}}
}

func TestTheClaimReturnsTheRowTheUpdateReturnedAndDoesNotReadAgain(t *testing.T) {
	f := &fakeDB{rows: []pgx.Row{bookingRow("SELECTING", deadline, nil)}}
	b, found, err := newStore(f).ClaimForSelection(ctx, "RB-1", "gt_1")
	if err != nil || !found || b.Status != bookingstate.Selecting || !b.BookingExpiresAt.Equal(deadline) || !b.PaymentExpiresAt.IsZero() {
		t.Fatalf("claim = %+v found=%v err=%v", b, found, err)
	}
	if len(f.calls) != 1 || f.calls[0].sql != ClaimSQL || !reflect.DeepEqual(f.calls[0].args, []any{"RB-1", "gt_1"}) {
		t.Fatalf("calls = %+v, want one claim bound to the booking id and the grant token", f.calls)
	}
}

func TestWhenTheClaimMatchesNothingTheRowIsReadBackForTheSameGrant(t *testing.T) {
	f := &fakeDB{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, bookingRow("GRANTED", deadline, nil)}}
	b, found, err := newStore(f).ClaimForSelection(ctx, "RB-1", "gt_1")
	if err != nil || !found || b.Status != bookingstate.Granted {
		t.Fatalf("read back = %+v found=%v err=%v", b, found, err)
	}
	if len(f.calls) != 2 || f.calls[1].sql != readSQL || !reflect.DeepEqual(f.calls[1].args, []any{"RB-1", "gt_1"}) {
		t.Fatalf("calls = %+v, want the read bound to the same booking id and grant token", f.calls)
	}
}

func TestNoRowForTheGrantIsNotFound(t *testing.T) {
	f := &fakeDB{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, fakeRow{err: pgx.ErrNoRows}}}
	if _, found, err := newStore(f).ClaimForSelection(ctx, "RB-1", "gt_other"); err != nil || found {
		t.Fatalf("found=%v err=%v, want no row and no error", found, err)
	}
}

func TestADatabaseErrorOnTheClaimOrTheReadIsReturned(t *testing.T) {
	for name, rows := range map[string][]pgx.Row{
		"claim": {fakeRow{err: errDB}},
		"read":  {fakeRow{err: pgx.ErrNoRows}, fakeRow{err: errDB}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, found, err := newStore(&fakeDB{rows: rows}).ClaimForSelection(ctx, "RB-1", "gt_1"); !errors.Is(err, errDB) || found {
				t.Fatalf("found=%v err=%v, want the database error", found, err)
			}
		})
	}
}

func TestAStoredStatusOutsideTheKnownNineIsAnErrorNotAGuess(t *testing.T) {
	f := &fakeDB{rows: []pgx.Row{bookingRow("SOLD_OUT", deadline, nil)}}
	if _, found, err := newStore(f).ClaimForSelection(ctx, "RB-1", "gt_1"); !errors.Is(err, bookingstate.ErrUnknownStatus) || found {
		t.Fatalf("found=%v err=%v, want ErrUnknownStatus", found, err)
	}
}

func TestDeadlinesAreReadInUTCAndMissingOnesStayZero(t *testing.T) {
	wib := time.FixedZone("WIB", 7*3600)
	f := &fakeDB{rows: []pgx.Row{bookingRow("HELD", deadline.In(wib), deadline.Add(10*time.Minute).In(wib))}}
	b, _, err := newStore(f).ClaimForSelection(ctx, "RB-1", "gt_1")
	if err != nil || b.BookingExpiresAt.Location() != time.UTC || !b.PaymentExpiresAt.Equal(deadline.Add(10*time.Minute)) {
		t.Fatalf("deadlines = %v / %v (%v), want UTC and the payment deadline", b.BookingExpiresAt, b.PaymentExpiresAt, err)
	}
	f = &fakeDB{rows: []pgx.Row{bookingRow("GRANTED", nil, nil)}}
	if b, _, err := newStore(f).ClaimForSelection(ctx, "RB-1", "gt_1"); err != nil || !b.BookingExpiresAt.IsZero() || !b.PaymentExpiresAt.IsZero() {
		t.Fatalf("missing deadlines = %v / %v (%v), want zero", b.BookingExpiresAt, b.PaymentExpiresAt, err)
	}
}

func eventRow() fakeRow {
	return fakeRow{vals: []any{"evt_a", "Konser A", time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC), "GBK", "https://cdn/a.jpg"}}
}

func TestThePageReadsTheEventAndItsCategoriesInOneBatch(t *testing.T) {
	f := &fakeDB{batch: &fakeBatch{first: eventRow(), second: &fakeRows{data: [][]any{
		{"cat_vip", "VIP", "Dekat", int64(1500000), 2},
		{"cat_fest", "Festival", "", int64(850000), 3},
	}}}}
	e, cats, found, err := newStore(f).Page(ctx, "evt_a")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if e != (domain.Event{ID: "evt_a", Name: "Konser A", Date: "2026-12-25", Venue: "GBK", ImageURL: "https://cdn/a.jpg"}) {
		t.Fatalf("event = %+v", e)
	}
	want := []domain.Category{
		{ID: "cat_vip", Name: "VIP", Description: "Dekat", Price: 1500000, MaxPerOrder: 2},
		{ID: "cat_fest", Name: "Festival", Price: 850000, MaxPerOrder: 3},
	}
	if !reflect.DeepEqual(cats, want) {
		t.Fatalf("categories = %+v, want %+v", cats, want)
	}
	if len(f.batched) != 2 || !reflect.DeepEqual(f.batched[0].Arguments, []any{"evt_a"}) || !reflect.DeepEqual(f.batched[1].Arguments, []any{"evt_a"}) {
		t.Fatalf("batch = %+v, want the event and the categories, both for evt_a", f.batched)
	}
}

func TestAnUnknownEventIsNotFoundAndAnEventWithoutCategoriesHasNone(t *testing.T) {
	f := &fakeDB{batch: &fakeBatch{first: fakeRow{err: pgx.ErrNoRows}, second: &fakeRows{}}}
	if _, _, found, err := newStore(f).Page(ctx, "evt_none"); err != nil || found {
		t.Fatalf("found=%v err=%v, want not found", found, err)
	}
	f = &fakeDB{batch: &fakeBatch{first: eventRow(), second: &fakeRows{}}}
	if _, cats, found, err := newStore(f).Page(ctx, "evt_a"); err != nil || !found || len(cats) != 0 {
		t.Fatalf("categories = %+v found=%v err=%v, want found with none", cats, found, err)
	}
}

func TestEveryStepOfThePageReportsItsDatabaseError(t *testing.T) {
	cases := map[string]*fakeBatch{
		"event":           {first: fakeRow{err: errDB}, second: &fakeRows{}},
		"categories":      {first: eventRow(), second: nil, queryErr: errDB},
		"category row":    {first: eventRow(), second: &fakeRows{data: [][]any{{"cat_vip", "VIP", "", int64(1), 1}}, scanErr: errDB}},
		"category cursor": {first: eventRow(), second: &fakeRows{err: errDB}},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, found, err := newStore(&fakeDB{batch: b}).Page(ctx, "evt_a"); !errors.Is(err, errDB) || found {
				t.Fatalf("found=%v err=%v, want the database error", found, err)
			}
		})
	}
}

func detailRow(status string, payment any, method string) fakeRow {
	return fakeRow{vals: []any{"RB-1", "evt_a", status, payment, method, "evt_a", "Konser A",
		time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC), "GBK", "https://cdn/a.jpg"}}
}

func TestTheDetailReadsTheBookingTheEventAndEachSeatForTheSameGrant(t *testing.T) {
	f := &fakeDB{batch: &fakeBatch{first: detailRow("CONFIRMED", deadline, "gopay"), second: &fakeRows{data: [][]any{
		{"it_1", "VIP", int64(1500000), "3171********0001", "TKT-001", "VALID"},
		{"it_2", "Festival", int64(850000), "3171********0002", "", ""},
	}}}}
	d, found, err := newStore(f).Detail(ctx, "RB-1", "gt_1")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if d.ID != "RB-1" || d.Status != bookingstate.Confirmed || d.PaymentMethod != "gopay" || !d.PaymentExpiresAt.Equal(deadline) ||
		d.Event.Date != "2026-12-25" || d.Event.Name != "Konser A" {
		t.Fatalf("detail = %+v", d)
	}
	wantItems := []domain.Item{
		{ID: "it_1", CategoryName: "VIP", UnitPrice: 1500000, NIKMasked: "3171********0001", TicketID: "TKT-001", TicketStatus: "VALID"},
		{ID: "it_2", CategoryName: "Festival", UnitPrice: 850000, NIKMasked: "3171********0002"},
	}
	if !reflect.DeepEqual(d.Items, wantItems) {
		t.Fatalf("items = %+v, want %+v", d.Items, wantItems)
	}
	if len(f.batched) != 2 || !reflect.DeepEqual(f.batched[0].Arguments, []any{"RB-1", "gt_1"}) || !reflect.DeepEqual(f.batched[1].Arguments, []any{"RB-1", "gt_1"}) {
		t.Fatalf("batch = %+v, want both statements bound to the booking id and the grant token", f.batched)
	}
}

func TestADetailWithoutARowOrWithAnUnknownStatusIsNotReturned(t *testing.T) {
	f := &fakeDB{batch: &fakeBatch{first: fakeRow{err: pgx.ErrNoRows}, second: &fakeRows{}}}
	if _, found, err := newStore(f).Detail(ctx, "RB-1", "gt_other"); err != nil || found {
		t.Fatalf("found=%v err=%v, want not found", found, err)
	}
	f = &fakeDB{batch: &fakeBatch{first: detailRow("SOLD_OUT", nil, ""), second: &fakeRows{}}}
	if _, found, err := newStore(f).Detail(ctx, "RB-1", "gt_1"); !errors.Is(err, bookingstate.ErrUnknownStatus) || found {
		t.Fatalf("found=%v err=%v, want ErrUnknownStatus", found, err)
	}
	f = &fakeDB{batch: &fakeBatch{first: detailRow("SELECTING", nil, ""), second: &fakeRows{}}}
	if d, found, err := newStore(f).Detail(ctx, "RB-1", "gt_1"); err != nil || !found || len(d.Items) != 0 || !d.PaymentExpiresAt.IsZero() {
		t.Fatalf("detail = %+v found=%v err=%v, want a booking without seats or deadline", d, found, err)
	}
}

func TestEveryStepOfTheDetailReportsItsDatabaseError(t *testing.T) {
	item := [][]any{{"it_1", "VIP", int64(1), "3171********0001", "", ""}}
	cases := map[string]*fakeBatch{
		"booking":     {first: fakeRow{err: errDB}, second: &fakeRows{}},
		"items":       {first: detailRow("HELD", nil, ""), queryErr: errDB},
		"item row":    {first: detailRow("HELD", nil, ""), second: &fakeRows{data: item, scanErr: errDB}},
		"item cursor": {first: detailRow("HELD", nil, ""), second: &fakeRows{err: errDB}},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, found, err := newStore(&fakeDB{batch: b}).Detail(ctx, "RB-1", "gt_1"); !errors.Is(err, errDB) || found {
				t.Fatalf("found=%v err=%v, want the database error", found, err)
			}
		})
	}
}

func newStore(f *fakeDB) *Store { return &Store{db: f} }
