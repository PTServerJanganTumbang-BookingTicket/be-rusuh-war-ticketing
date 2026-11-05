package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── test doubles (local; nik-verify postgres package has no helpers file) ─────

type testRow struct{ fn func(...any) error }

func (r testRow) Scan(dest ...any) error { return r.fn(dest...) }

func rowError(err error) testRow {
	return testRow{fn: func(...any) error { return err }}
}
func rowString(s string) testRow {
	return testRow{fn: func(dest ...any) error {
		if p, ok := dest[0].(*string); ok {
			*p = s
		}
		return nil
	}}
}

type testDB struct {
	qr func(ctx context.Context, sql string, args ...any) pgx.Row
}

func (d *testDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.qr(ctx, sql, args...)
}
func (d *testDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}
func (d *testDB) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (d *testDB) Begin(_ context.Context) (pgx.Tx, error) { return nil, nil }

// ── tests ──────────────────────────────────────────────────────────────────────

func TestFindEvidence_NoRows(t *testing.T) {
	db := &testDB{qr: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowError(pgx.ErrNoRows)
	}}
	repo := &EvidenceRepository{pool: db}
	source, err := repo.FindEvidence(context.Background(), "hmac")
	require.NoError(t, err)
	assert.Empty(t, source)
}

func TestFindEvidence_Registered(t *testing.T) {
	db := &testDB{qr: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowString("registered")
	}}
	repo := &EvidenceRepository{pool: db}
	source, err := repo.FindEvidence(context.Background(), "hmac")
	require.NoError(t, err)
	assert.Equal(t, "registered", string(source))
}

func TestFindEvidence_Saved(t *testing.T) {
	db := &testDB{qr: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowString("saved")
	}}
	repo := &EvidenceRepository{pool: db}
	source, err := repo.FindEvidence(context.Background(), "hmac")
	require.NoError(t, err)
	assert.Equal(t, "saved", string(source))
}

func TestFindEvidence_DBError(t *testing.T) {
	db := &testDB{qr: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowError(errors.New("db error"))
	}}
	repo := &EvidenceRepository{pool: db}
	_, err := repo.FindEvidence(context.Background(), "hmac")
	assert.Error(t, err)
}
