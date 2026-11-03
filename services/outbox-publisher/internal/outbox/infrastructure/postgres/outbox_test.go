package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// ── minimal test doubles ──────────────────────────────────────────────────────

type fakeRow struct{ fn func(...any) error }

func (r fakeRow) Scan(dest ...any) error { return r.fn(dest...) }

func scanErr(err error) fakeRow  { return fakeRow{fn: func(...any) error { return err }} }
func scanInt(n int) fakeRow {
	return fakeRow{fn: func(dest ...any) error {
		if p, ok := dest[0].(*int); ok {
			*p = n
		}
		return nil
	}}
}

type fakeRows struct {
	data [][]any
	idx  int
	err  error
}

func (r *fakeRows) Close()                                      {}
func (r *fakeRows) Err() error                                  { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool {
	if r.idx < len(r.data) {
		r.idx++
		return true
	}
	return false
}
func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.idx-1]
	for i, d := range dest {
		if i >= len(row) {
			break
		}
		switch p := d.(type) {
		case *string:
			if s, ok := row[i].(string); ok {
				*p = s
			}
		case *[]byte:
			if b, ok := row[i].([]byte); ok {
				*p = b
			}
		case *time.Time:
			if t, ok := row[i].(time.Time); ok {
				*p = t
			}
		}
	}
	return nil
}
func (r *fakeRows) Values() ([]any, error) { return nil, nil }
func (r *fakeRows) RawValues() [][]byte    { return nil }
func (r *fakeRows) Conn() *pgx.Conn        { return nil }

type fakeTx struct {
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	commitFn   func(ctx context.Context) error
	rollbackFn func(ctx context.Context) error
}

func (t *fakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if t.queryFn != nil {
		return t.queryFn(ctx, sql, args...)
	}
	return &fakeRows{}, nil
}
func (t *fakeTx) Commit(ctx context.Context) error {
	if t.commitFn != nil {
		return t.commitFn(ctx)
	}
	return nil
}
func (t *fakeTx) Rollback(ctx context.Context) error {
	if t.rollbackFn != nil {
		return t.rollbackFn(ctx)
	}
	return nil
}
func (t *fakeTx) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (t *fakeTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return scanErr(pgx.ErrNoRows)
}
func (t *fakeTx) Begin(_ context.Context) (pgx.Tx, error) { return nil, nil }
func (t *fakeTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (t *fakeTx) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults { return nil }
func (t *fakeTx) LargeObjects() pgx.LargeObjects                             { return pgx.LargeObjects{} }
func (t *fakeTx) Prepare(_ context.Context, _, _ string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (t *fakeTx) Conn() *pgx.Conn { return nil }

type fakeDB struct {
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	beginFn    func(ctx context.Context) (pgx.Tx, error)
}

func (d *fakeDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if d.queryRowFn != nil {
		return d.queryRowFn(ctx, sql, args...)
	}
	return scanErr(pgx.ErrNoRows)
}
func (d *fakeDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &fakeRows{}, nil
}
func (d *fakeDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if d.execFn != nil {
		return d.execFn(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, nil
}
func (d *fakeDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if d.beginFn != nil {
		return d.beginFn(ctx)
	}
	return &fakeTx{}, nil
}

var ctx = context.Background()

// ── CountUnpublished ──────────────────────────────────────────────────────────

func TestCountUnpublished_Zero(t *testing.T) {
	db := &fakeDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return scanInt(0)
	}}
	n, err := NewOutboxRepository(db).(*OutboxRepository).CountUnpublished(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestCountUnpublished_NonZero(t *testing.T) {
	db := &fakeDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return scanInt(42)
	}}
	n, err := (&OutboxRepository{pool: db}).CountUnpublished(ctx)
	require.NoError(t, err)
	assert.Equal(t, 42, n)
}

func TestCountUnpublished_DBError(t *testing.T) {
	db := &fakeDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return scanErr(errors.New("db down"))
	}}
	_, err := (&OutboxRepository{pool: db}).CountUnpublished(ctx)
	assert.Error(t, err)
}

// ── MarkPublished ─────────────────────────────────────────────────────────────

func TestMarkPublished_Empty(t *testing.T) {
	db := &fakeDB{}
	assert.NoError(t, (&OutboxRepository{pool: db}).MarkPublished(ctx, nil))
}

func TestMarkPublished_OK(t *testing.T) {
	db := &fakeDB{}
	assert.NoError(t, (&OutboxRepository{pool: db}).MarkPublished(ctx, []string{"id-1", "id-2"}))
}

func TestMarkPublished_DBError(t *testing.T) {
	db := &fakeDB{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, errors.New("exec failed")
	}}
	err := (&OutboxRepository{pool: db}).MarkPublished(ctx, []string{"id-1"})
	assert.Error(t, err)
}

// ── ClaimUnpublished ──────────────────────────────────────────────────────────

func TestClaimUnpublished_BeginFails(t *testing.T) {
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		return nil, errors.New("no connection")
	}}
	_, finish, err := (&OutboxRepository{pool: db}).ClaimUnpublished(ctx, 10)
	assert.Error(t, err)
	assert.Nil(t, finish)
}

func TestClaimUnpublished_QueryFails(t *testing.T) {
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &fakeTx{queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return nil, errors.New("query failed")
		}}
		return tx, nil
	}}
	_, _, err := (&OutboxRepository{pool: db}).ClaimUnpublished(ctx, 10)
	assert.Error(t, err)
}

func TestClaimUnpublished_Empty(t *testing.T) {
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		return &fakeTx{}, nil
	}}
	records, finish, err := (&OutboxRepository{pool: db}).ClaimUnpublished(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, records)
	require.NotNil(t, finish)
	assert.NoError(t, finish(ctx, true))
}

func TestClaimUnpublished_OneRecord(t *testing.T) {
	now := time.Now()
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &fakeTx{queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			rows := &fakeRows{data: [][]any{
				{"id-1", "agg-1", string(events.TypeAccountVerify), []byte(`{}`), now},
			}}
			return rows, nil
		}}
		return tx, nil
	}}
	records, finish, err := (&OutboxRepository{pool: db}).ClaimUnpublished(ctx, 10)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "id-1", records[0].ID)
	assert.NoError(t, finish(ctx, true))
}

func TestClaimUnpublished_FinishRollback(t *testing.T) {
	rollbackCalled := false
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &fakeTx{rollbackFn: func(_ context.Context) error {
			rollbackCalled = true
			return nil
		}}
		return tx, nil
	}}
	_, finish, err := (&OutboxRepository{pool: db}).ClaimUnpublished(ctx, 10)
	require.NoError(t, err)
	assert.NoError(t, finish(ctx, false))
	assert.True(t, rollbackCalled)
}

func TestClaimUnpublished_ScanError(t *testing.T) {
	errScan := errors.New("scan failed")
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &fakeTx{queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &fakeRows{data: [][]any{
				// intentionally short row so scan fails
				{"id-1"},
			}}, nil
		}}
		_ = errScan // suppress "declared and not used"
		// Override Scan to return an error
		return &brokenScanTx{tx}, nil
	}}
	_, _, err := (&OutboxRepository{pool: db}).ClaimUnpublished(ctx, 10)
	assert.Error(t, err)
}

// brokenScanTx wraps a fakeTx and returns scan errors
type brokenScanTx struct{ *fakeTx }

func (b *brokenScanTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return &brokenScanRows{}, nil
}

type brokenScanRows struct{}

func (r *brokenScanRows) Close()                                      {}
func (r *brokenScanRows) Err() error                                  { return nil }
func (r *brokenScanRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *brokenScanRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *brokenScanRows) Next() bool                                   { return true }
func (r *brokenScanRows) Scan(_ ...any) error                          { return errors.New("scan failed") }
func (r *brokenScanRows) Values() ([]any, error)                       { return nil, nil }
func (r *brokenScanRows) RawValues() [][]byte                          { return nil }
func (r *brokenScanRows) Conn() *pgx.Conn                              { return nil }

func TestClaimUnpublished_IterateError(t *testing.T) {
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &fakeTx{queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &fakeRows{err: errors.New("iteration error")}, nil
		}}
		return tx, nil
	}}
	_, _, err := (&OutboxRepository{pool: db}).ClaimUnpublished(ctx, 10)
	assert.Error(t, err)
}
