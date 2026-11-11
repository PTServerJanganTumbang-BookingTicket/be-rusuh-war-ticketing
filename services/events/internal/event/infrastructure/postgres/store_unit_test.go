package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// These tests fake the database so they run without Postgres (CI has none). store_test.go proves the SQL on real Postgres.

var (
	errDB = errors.New("connection reset")
	day   = time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)
	start = time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
)

type fakeDB struct {
	batch   pgx.BatchResults
	queries []*pgx.QueuedQuery
}

func (f *fakeDB) SendBatch(_ context.Context, b *pgx.Batch) pgx.BatchResults {
	f.queries = b.QueuedQueries
	return f.batch
}

// fakeBatch answers by method, not by order: QueryRow gives row, Query gives rows.
type fakeBatch struct {
	row      pgx.Row
	rows     pgx.Rows
	queryErr error
}

func (b *fakeBatch) Exec() (pgconn.CommandTag, error) { return pgconn.CommandTag{}, nil }
func (b *fakeBatch) Query() (pgx.Rows, error)         { return b.rows, b.queryErr }
func (b *fakeBatch) QueryRow() pgx.Row                { return b.row }
func (b *fakeBatch) Close() error                     { return nil }

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

// assign stores v in *dest. nil is the zero value; a plain value also fits a *T destination (a nullable column).
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
	scanErr error
	err     error
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

func eventVals(id string, saleStartsAt any) []any {
	return []any{id, "Konser", "LIVE MUSIC", day, "19:00", "GBK", "Jakarta", "https://x/y.jpg", "d", saleStartsAt}
}

func newStore(f *fakeDB) *Store { return &Store{db: f} }

var ctx = context.Background()

func TestListScansTheEventsAndTheTotalAndBindsLimitAndOffset(t *testing.T) {
	f := &fakeDB{batch: &fakeBatch{
		rows: &fakeRows{data: [][]any{eventVals("a", nil), eventVals("b", start)}},
		row:  fakeRow{vals: []any{7}},
	}}
	got, total, err := newStore(f).List(ctx, 2, 4)
	if err != nil || total != 7 || len(got) != 2 {
		t.Fatalf("got %+v total %d err %v", got, total, err)
	}
	if got[0].ID != "a" || got[0].City != "Jakarta" || got[0].Time != "19:00" || !got[0].Date.Equal(day) || got[0].SaleStartsAt != nil {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].SaleStartsAt == nil || !got[1].SaleStartsAt.Equal(start) {
		t.Fatalf("second sale_starts_at = %v", got[1].SaleStartsAt)
	}
	if len(f.queries) != 2 || f.queries[0].SQL != ListSQL || !reflect.DeepEqual(f.queries[0].Arguments, []any{2, 4}) || f.queries[1].SQL != CountSQL {
		t.Fatalf("queries = %+v", f.queries)
	}
}

func TestListReportsEachWayTheBatchCanFail(t *testing.T) {
	for name, b := range map[string]*fakeBatch{
		"the page query fails":     {queryErr: errDB},
		"a row cannot be scanned":  {rows: &fakeRows{data: [][]any{eventVals("a", nil)}, scanErr: errDB}},
		"the rows end in an error": {rows: &fakeRows{err: errDB}},
		"the count fails":          {rows: &fakeRows{}, row: fakeRow{err: errDB}},
	} {
		if _, _, err := newStore(&fakeDB{batch: b}).List(ctx, 10, 0); !errors.Is(err, errDB) {
			t.Errorf("%s: err = %v, want the database error", name, err)
		}
	}
}

func TestGetScansTheEventAndItsCategoriesInOrder(t *testing.T) {
	f := &fakeDB{batch: &fakeBatch{
		row:  fakeRow{vals: eventVals("a", start)},
		rows: &fakeRows{data: [][]any{{"vip", "VIP", "close", int64(1500000), int64(12)}, {"trib", "Tribune", "far", int64(500000), int64(0)}}},
	}}
	ev, cats, ok, err := newStore(f).Get(ctx, "a")
	if err != nil || !ok || ev.ID != "a" || len(cats) != 2 || cats[0].ID != "vip" || cats[0].Price != 1500000 || cats[1].Available != 0 {
		t.Fatalf("ev %+v cats %+v ok %v err %v", ev, cats, ok, err)
	}
	if len(f.queries) != 2 || f.queries[0].SQL != GetEventSQL || f.queries[1].SQL != CategoriesSQL ||
		!reflect.DeepEqual(f.queries[0].Arguments, []any{"a"}) || !reflect.DeepEqual(f.queries[1].Arguments, []any{"a"}) {
		t.Fatalf("queries = %+v", f.queries)
	}
}

func TestGetOfAnEventWithNoRowIsNotFoundAndNotAnError(t *testing.T) {
	_, _, ok, err := newStore(&fakeDB{batch: &fakeBatch{row: fakeRow{err: pgx.ErrNoRows}}}).Get(ctx, "nope")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestGetReportsEachWayTheBatchCanFail(t *testing.T) {
	good := fakeRow{vals: eventVals("a", nil)}
	for name, b := range map[string]*fakeBatch{
		"the event row fails":          {row: fakeRow{err: errDB}},
		"the categories query fails":   {row: good, queryErr: errDB},
		"a category cannot be scanned": {row: good, rows: &fakeRows{data: [][]any{{"vip", "VIP", "", int64(1), int64(1)}}, scanErr: errDB}},
		"the categories end in error":  {row: good, rows: &fakeRows{err: errDB}},
	} {
		if _, _, _, err := newStore(&fakeDB{batch: b}).Get(ctx, "a"); !errors.Is(err, errDB) {
			t.Errorf("%s: err = %v, want the database error", name, err)
		}
	}
}
