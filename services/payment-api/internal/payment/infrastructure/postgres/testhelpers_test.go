package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
)

// ── rows ──────────────────────────────────────────────────────────────────────

type mockRow struct{ scanFn func(dest ...any) error }

func (r *mockRow) Scan(dest ...any) error { return r.scanFn(dest...) }

func rowErr(err error) *mockRow {
	return &mockRow{scanFn: func(...any) error { return err }}
}

func rowValues(vals ...any) *mockRow {
	return &mockRow{scanFn: func(dest ...any) error {
		for i, d := range dest {
			if i >= len(vals) {
				break
			}
			if err := assignAny(d, vals[i]); err != nil {
				return err
			}
		}
		return nil
	}}
}

// assignAny is a minimal type switch for test scan destinations. Only the
// shapes this repository actually scans are supported, so a new column in a
// query fails loudly here rather than scanning into the wrong field.
func assignAny(dst, src any) error {
	switch d := dst.(type) {
	case *string:
		if s, ok := src.(string); ok {
			*d = s
			return nil
		}
	case *bool:
		if b, ok := src.(bool); ok {
			*d = b
			return nil
		}
	case *int:
		if i, ok := src.(int); ok {
			*d = i
			return nil
		}
	case *int64:
		switch v := src.(type) {
		case int64:
			*d = v
			return nil
		case int:
			*d = int64(v)
			return nil
		}
	case *[]byte:
		switch v := src.(type) {
		case []byte:
			*d = v
			return nil
		case string:
			*d = []byte(v)
			return nil
		}
	case *entity.BookingStatus:
		switch v := src.(type) {
		case entity.BookingStatus:
			*d = v
			return nil
		case string:
			*d = entity.BookingStatus(v)
			return nil
		}
	case **int64:
		switch v := src.(type) {
		case *int64:
			*d = v
			return nil
		case int64:
			n := v
			*d = &n
			return nil
		case int:
			n := int64(v)
			*d = &n
			return nil
		case nil:
			*d = nil
			return nil
		}
	case **time.Time:
		switch v := src.(type) {
		case *time.Time:
			*d = v
			return nil
		case nil:
			*d = nil
			return nil
		}
	}
	if src == nil {
		return nil
	}
	return errors.New("testhelper: unsupported type pair in assignAny")
}

type mockRows struct {
	rows []func(dest ...any) error
	idx  int
	err  error
}

func (r *mockRows) Close()                                       {}
func (r *mockRows) Err() error                                   { return r.err }
func (r *mockRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *mockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *mockRows) Next() bool {
	if r.idx < len(r.rows) {
		r.idx++
		return true
	}
	return false
}
func (r *mockRows) Scan(dest ...any) error { return r.rows[r.idx-1](dest...) }
func (r *mockRows) Values() ([]any, error) { return nil, nil }
func (r *mockRows) RawValues() [][]byte    { return nil }
func (r *mockRows) Conn() *pgx.Conn        { return nil }

func rowsOf(vals ...[]any) *mockRows {
	out := &mockRows{}
	for _, v := range vals {
		v := v
		out.rows = append(out.rows, func(dest ...any) error {
			for i, d := range dest {
				if i >= len(v) {
					break
				}
				if err := assignAny(d, v[i]); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return out
}

// ── transaction ───────────────────────────────────────────────────────────────

// execCall records one statement the repository issued, so a test can assert
// on the ORDER of writes - which is where this repository's correctness lives.
type execCall struct {
	sql  string
	args []any
}

type mockTx struct {
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	commitErr  error

	execs     []execCall
	queries   []string
	commits   int
	rollbacks int
}

func (t *mockTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execs = append(t.execs, execCall{sql: sql, args: args})
	if t.execFn != nil {
		return t.execFn(ctx, sql, args...)
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (t *mockTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	t.queries = append(t.queries, sql)
	if t.queryRowFn != nil {
		return t.queryRowFn(ctx, sql, args...)
	}
	return rowErr(pgx.ErrNoRows)
}

func (t *mockTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.queries = append(t.queries, sql)
	if t.queryFn != nil {
		return t.queryFn(ctx, sql, args...)
	}
	return &mockRows{}, nil
}

func (t *mockTx) Commit(ctx context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *mockTx) Rollback(ctx context.Context) error {
	t.rollbacks++
	return nil
}

func (t *mockTx) Begin(ctx context.Context) (pgx.Tx, error) { return nil, nil }
func (t *mockTx) CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (t *mockTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults { return nil }
func (t *mockTx) LargeObjects() pgx.LargeObjects                               { return pgx.LargeObjects{} }
func (t *mockTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (t *mockTx) Conn() *pgx.Conn { return nil }

// execMatching returns the recorded statements whose SQL contains every one of
// the given fragments, which keeps assertions readable without pinning
// whitespace.
func (t *mockTx) execMatching(fragments ...string) []execCall {
	var out []execCall
	for _, c := range t.execs {
		all := true
		for _, f := range fragments {
			if !strings.Contains(c.sql, f) {
				all = false
				break
			}
		}
		if all {
			out = append(out, c)
		}
	}
	return out
}

// ── pool ──────────────────────────────────────────────────────────────────────

type mockDB struct {
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	beginErr   error
	tx         *mockTx

	queries []string
	args    [][]any
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	m.queries = append(m.queries, sql)
	m.args = append(m.args, args)
	if m.queryRowFn != nil {
		return m.queryRowFn(ctx, sql, args...)
	}
	return rowErr(pgx.ErrNoRows)
}

func (m *mockDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	m.queries = append(m.queries, sql)
	return &mockRows{}, nil
}

func (m *mockDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (m *mockDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if m.beginErr != nil {
		return nil, m.beginErr
	}
	if m.tx == nil {
		m.tx = &mockTx{}
	}
	return m.tx, nil
}

// ── fixtures ──────────────────────────────────────────────────────────────────

var fixedNow = time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)

// bookingValues mirrors bookingColumns, in order. A test that changes a status
// changes one element rather than restating thirteen.
func bookingValues(status entity.BookingStatus, mutate ...func([]any)) []any {
	held := fixedNow.Add(-5 * time.Minute)
	exp := held.Add(10 * time.Minute)
	vals := []any{
		"RB-00912",      // id
		"EV-1",          // event_id
		"ACC-1",         // account_id
		"buyer@example", // contact_email
		status,          // status
		&held,           // held_at
		&exp,            // payment_expires_at
		"",              // payment_ref
		nil,             // paid_at — NULL until a settlement confirms it
		0,               // payment_attempts
		"",              // payment_method
		false,           // payment_disputed
		"grant-abc",     // grant_token
		"qt-1",          // queue_token
		nil,             // payment_amount — NULL until a charge (0017)
		"",              // payment_status — empty until a charge (0017)
		nil,             // payment_instructions — NULL until a charge (0018)
	}
	for _, m := range mutate {
		m(vals)
	}
	return vals
}

func withPaymentRef(ref string) func([]any) {
	return func(v []any) { v[7] = ref }
}

func withPaymentAmount(n int64) func([]any) {
	return func(v []any) { v[14] = n }
}

func withPaymentStatus(s string) func([]any) {
	return func(v []any) { v[15] = s }
}

// withInstructions supplies the JSONB column as the driver would: raw bytes.
func withInstructions(j string) func([]any) {
	return func(v []any) { v[16] = []byte(j) }
}

func withAttempts(n int) func([]any) {
	return func(v []any) { v[9] = n }
}

func withExpiresAt(t time.Time) func([]any) {
	return func(v []any) { v[6] = &t }
}

func withAccountID(id string) func([]any) {
	return func(v []any) { v[2] = id }
}

// ids returns deterministic primary keys so assertions can name them.
func ids() func() string {
	n := 0
	return func() string {
		n++
		return "id-" + string(rune('0'+n))
	}
}

func newRepo(db DBConn) *BookingRepository {
	return &BookingRepository{
		db:       db,
		newID:    ids(),
		newNonce: func() (string, error) { return "nonce-x", nil },
	}
}
