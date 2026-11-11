package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// These cover the Go side of ExpireHeldDue (assembling, error handling, the transaction's end) on a fake; the SQL itself
// is proven on real Postgres in held_test.go.

var errBoom = errors.New("connection reset")

type reply struct {
	rows pgx.Rows
	err  error
}

type fakeTx struct {
	pgx.Tx
	replies   []reply
	queries   []queryCall
	commitErr error
	commits   int
	rollbacks int
}

type queryCall struct {
	sql  string
	args []any
}

func (f *fakeTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.queries = append(f.queries, queryCall{sql, args})
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r.rows, r.err
}
func (f *fakeTx) Commit(context.Context) error   { f.commits++; return f.commitErr }
func (f *fakeTx) Rollback(context.Context) error { f.rollbacks++; return nil }

type fakeQuerier struct {
	Querier // nil: the sweep must only Begin
	tx      *fakeTx
	err     error
}

func (f *fakeQuerier) Begin(context.Context) (pgx.Tx, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tx, nil
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
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = r.data[r.i-1][i].(string)
		case *int64:
			*p = r.data[r.i-1][i].(int64)
		default:
			return errors.New("fake scan: unsupported destination")
		}
	}
	return nil
}

var now = time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)

func updated() *fakeRows {
	return &fakeRows{data: [][]any{{"B1", "E1", "qt1", "gt1"}, {"B2", "E1", "qt2", ""}}}
}

func claims() *fakeRows {
	return &fakeRows{data: [][]any{{"B1", "n1"}, {"B1", "n2"}, {"B2", "n3"}}}
}

func counts() *fakeRows {
	return &fakeRows{data: [][]any{{"B1", "FEST", int64(1)}, {"B1", "VIP", int64(2)}, {"B2", "VIP", int64(1)}}}
}

func TestEachExpiredBookingComesBackWithItsOwnSeatsAndNIKs(t *testing.T) {
	tx := &fakeTx{replies: []reply{{rows: updated()}, {rows: claims()}, {rows: counts()}}}
	got, err := NewBookings(&fakeQuerier{tx: tx}).ExpireHeldDue(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.EndedHold{
		{Ended: domain.Ended{BookingID: "B1", EventID: "E1", QueueToken: "qt1", GrantToken: "gt1"},
			Seats: []domain.SeatCount{{CategoryID: "FEST", Qty: 1}, {CategoryID: "VIP", Qty: 2}}, NIKs: []string{"n1", "n2"}},
		{Ended: domain.Ended{BookingID: "B2", EventID: "E1", QueueToken: "qt2"},
			Seats: []domain.SeatCount{{CategoryID: "VIP", Qty: 1}}, NIKs: []string{"n3"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("commits=%d rollbacks=%d, want one commit", tx.commits, tx.rollbacks)
	}
}

func TestTheThreeStatementsRunInOneTransactionAndAreBoundToTheDueRows(t *testing.T) {
	tx := &fakeTx{replies: []reply{{rows: updated()}, {rows: claims()}, {rows: counts()}}}
	if _, err := NewBookings(&fakeQuerier{tx: tx}).ExpireHeldDue(ctx, now); err != nil {
		t.Fatal(err)
	}
	if len(tx.queries) != 3 {
		t.Fatalf("%d statements, want the update, the claims delete and the seat counts", len(tx.queries))
	}
	ids := []string{"B1", "B2"}
	if !strings.Contains(tx.queries[0].sql, "status = 'HELD'") || !strings.Contains(tx.queries[0].sql, "'EXPIRED'") || !reflect.DeepEqual(tx.queries[0].args, []any{now}) {
		t.Fatalf("update = %+v", tx.queries[0])
	}
	if !strings.Contains(tx.queries[1].sql, "DELETE FROM nik_claims") || !strings.Contains(tx.queries[1].sql, "RETURNING") || !reflect.DeepEqual(tx.queries[1].args, []any{ids}) {
		t.Fatalf("claims = %+v", tx.queries[1])
	}
	if !strings.Contains(tx.queries[2].sql, "FROM booking_items") || !strings.Contains(tx.queries[2].sql, "GROUP BY") || !reflect.DeepEqual(tx.queries[2].args, []any{ids}) {
		t.Fatalf("counts = %+v", tx.queries[2])
	}
}

func TestNothingDueIsOneStatementAndAnEmptyAnswer(t *testing.T) {
	tx := &fakeTx{replies: []reply{{rows: &fakeRows{}}}}
	got, err := NewBookings(&fakeQuerier{tx: tx}).ExpireHeldDue(ctx, now)
	if err != nil || len(got) != 0 || len(tx.queries) != 1 || tx.commits != 1 {
		t.Fatalf("got %+v (%v) statements=%d commits=%d", got, err, len(tx.queries), tx.commits)
	}
}

// If the status became EXPIRED but the claims stayed, those NIKs would be blocked for this event for good
// (UNIQUE (event_id, nik_hmac)): any failure after the UPDATE rolls the whole transaction back.
func TestAnyFailureRollsTheWholeTransactionBackSoNoExpiredBookingKeepsItsClaims(t *testing.T) {
	cases := map[string][]reply{
		"the update":          {{err: errBoom}},
		"reading the update":  {{rows: &fakeRows{data: [][]any{{"B1", "E1", "q", "g"}}, scanErr: errBoom}}},
		"the update's cursor": {{rows: &fakeRows{err: errBoom}}},
		"the claims delete":   {{rows: updated()}, {err: errBoom}},
		"reading the claims":  {{rows: updated()}, {rows: &fakeRows{data: [][]any{{"B1", "n"}}, scanErr: errBoom}}},
		"the claims' cursor":  {{rows: updated()}, {rows: &fakeRows{err: errBoom}}},
		"the seat counts":     {{rows: updated()}, {rows: claims()}, {err: errBoom}},
		"reading the counts":  {{rows: updated()}, {rows: claims()}, {rows: &fakeRows{data: [][]any{{"B1", "V", int64(1)}}, scanErr: errBoom}}},
		"the counts' cursor":  {{rows: updated()}, {rows: claims()}, {rows: &fakeRows{err: errBoom}}},
	}
	for name, replies := range cases {
		t.Run(name, func(t *testing.T) {
			tx := &fakeTx{replies: replies}
			got, err := NewBookings(&fakeQuerier{tx: tx}).ExpireHeldDue(ctx, now)
			if !errors.Is(err, errBoom) || got != nil {
				t.Fatalf("got %+v err = %v, want the error and no result (the transaction did not commit)", got, err)
			}
			if tx.commits != 0 || tx.rollbacks != 1 {
				t.Fatalf("commits=%d rollbacks=%d, want a rollback", tx.commits, tx.rollbacks)
			}
		})
	}
}

func TestBeginAndCommitFailuresAreReportedAndNothingIsReturned(t *testing.T) {
	if got, err := NewBookings(&fakeQuerier{err: errBoom}).ExpireHeldDue(ctx, now); !errors.Is(err, errBoom) || got != nil {
		t.Fatalf("begin: %+v %v", got, err)
	}
	tx := &fakeTx{replies: []reply{{rows: updated()}, {rows: claims()}, {rows: counts()}}, commitErr: errBoom}
	if got, err := NewBookings(&fakeQuerier{tx: tx}).ExpireHeldDue(ctx, now); !errors.Is(err, errBoom) || got != nil {
		t.Fatalf("commit: %+v %v, want an error and nothing to release (the status may not have changed)", got, err)
	}
}
