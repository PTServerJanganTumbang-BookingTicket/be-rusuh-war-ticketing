package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ── mockRow ───────────────────────────────────────────────────────────────────

type mockRow struct {
	scanFn func(dest ...any) error
}

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

// assignAny is a minimal type switch for test scan destinations.
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
	}
	return errors.New("testhelper: unsupported type pair in assignAny")
}

// ── mockRows ──────────────────────────────────────────────────────────────────

type mockRows struct {
	rows []func(dest ...any) error
	idx  int
	err  error
}

func (r *mockRows) Close()                                  {}
func (r *mockRows) Err() error                              { return r.err }
func (r *mockRows) CommandTag() pgconn.CommandTag            { return pgconn.CommandTag{} }
func (r *mockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *mockRows) Next() bool {
	if r.idx < len(r.rows) {
		r.idx++
		return true
	}
	return false
}
func (r *mockRows) Scan(dest ...any) error  { return r.rows[r.idx-1](dest...) }
func (r *mockRows) Values() ([]any, error)  { return nil, nil }
func (r *mockRows) RawValues() [][]byte     { return nil }
func (r *mockRows) Conn() *pgx.Conn         { return nil }

// ── mockTx ────────────────────────────────────────────────────────────────────

type mockTx struct {
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	commitFn   func(ctx context.Context) error
	rollbackFn func(ctx context.Context) error
}

func (t *mockTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if t.execFn != nil {
		return t.execFn(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, nil
}
func (t *mockTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if t.queryRowFn != nil {
		return t.queryRowFn(ctx, sql, args...)
	}
	return rowErr(pgx.ErrNoRows)
}
func (t *mockTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if t.queryFn != nil {
		return t.queryFn(ctx, sql, args...)
	}
	return &mockRows{}, nil
}
func (t *mockTx) Commit(ctx context.Context) error {
	if t.commitFn != nil {
		return t.commitFn(ctx)
	}
	return nil
}
func (t *mockTx) Rollback(ctx context.Context) error {
	if t.rollbackFn != nil {
		return t.rollbackFn(ctx)
	}
	return nil
}
func (t *mockTx) Begin(ctx context.Context) (pgx.Tx, error)                   { return nil, nil }
func (t *mockTx) CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (t *mockTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults { return nil }
func (t *mockTx) LargeObjects() pgx.LargeObjects                               { return pgx.LargeObjects{} }
func (t *mockTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (t *mockTx) Conn() *pgx.Conn { return nil }

// ── mockDB ────────────────────────────────────────────────────────────────────

type mockDB struct {
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	beginFn    func(ctx context.Context) (pgx.Tx, error)
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if m.queryRowFn != nil {
		return m.queryRowFn(ctx, sql, args...)
	}
	return rowErr(pgx.ErrNoRows)
}
func (m *mockDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if m.queryFn != nil {
		return m.queryFn(ctx, sql, args...)
	}
	return &mockRows{}, nil
}
func (m *mockDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if m.execFn != nil {
		return m.execFn(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, nil
}
func (m *mockDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if m.beginFn != nil {
		return m.beginFn(ctx)
	}
	return &mockTx{}, nil
}

// okTag builds a CommandTag reporting n rows affected.
// (retained as a test helper; referenced by saved_nik_test.go)
func okTag(rowsAffected int) pgconn.CommandTag {
	if rowsAffected == 0 {
		return pgconn.NewCommandTag("DELETE 0")
	}
	return pgconn.NewCommandTag("DELETE 1")
}
