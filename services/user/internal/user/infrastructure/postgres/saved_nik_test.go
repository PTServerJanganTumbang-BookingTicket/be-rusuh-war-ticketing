package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// ── FindSavedNIKsByAccountID ──────────────────────────────────────────────────

func TestFindSavedNIKsByAccountID_Empty(t *testing.T) {
	db := &mockDB{queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
		return &mockRows{}, nil
	}}
	niks, err := repo(db).FindSavedNIKsByAccountID(ctx, "acc-1")
	require.NoError(t, err)
	assert.Empty(t, niks)
}

func TestFindSavedNIKsByAccountID_DBError(t *testing.T) {
	db := &mockDB{queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
		return nil, errors.New("query failed")
	}}
	_, err := repo(db).FindSavedNIKsByAccountID(ctx, "acc-1")
	assert.Error(t, err)
}

func TestFindSavedNIKsByAccountID_OneRow(t *testing.T) {
	db := &mockDB{queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
		return &mockRows{rows: []func(...any) error{
			func(dest ...any) error {
				// id, account_id, nik_hmac, nik_masked, label, verified_at, dukcapil_ref
				vals := []any{"n-1", "acc-1", "hmac", "****1234", "main", nil, "ref-1"}
				for i, d := range dest {
					if i < len(vals) {
						_ = assignAny(d, vals[i])
					}
				}
				return nil
			},
		}}, nil
	}}
	niks, err := repo(db).FindSavedNIKsByAccountID(ctx, "acc-1")
	require.NoError(t, err)
	assert.Len(t, niks, 1)
	assert.Equal(t, "n-1", niks[0].ID)
}

// ── DeleteSavedNIK ────────────────────────────────────────────────────────────

func TestDeleteSavedNIK_OK(t *testing.T) {
	db := &mockDB{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
		return okTag(1), nil
	}}
	assert.NoError(t, repo(db).DeleteSavedNIK(ctx, "acc-1", "n-1"))
}

func TestDeleteSavedNIK_NotFound(t *testing.T) {
	db := &mockDB{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
		return okTag(0), nil
	}}
	err := repo(db).DeleteSavedNIK(ctx, "acc-1", "n-missing")
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, 404, ae.HTTPStatus)
}

func TestDeleteSavedNIK_DBError(t *testing.T) {
	db := &mockDB{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, errors.New("delete failed")
	}}
	assert.Error(t, repo(db).DeleteSavedNIK(ctx, "acc-1", "n-1"))
}

// ── CreateSavedNIKWithinCap ───────────────────────────────────────────────────

func TestCreateSavedNIKWithinCap_AccountNotFound(t *testing.T) {
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &mockTx{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return rowErr(pgx.ErrNoRows) // FOR UPDATE found nothing
		}}
		return tx, nil
	}}
	_, err := repo(db).CreateSavedNIKWithinCap(ctx, entity.SavedNIK{AccountID: "acc-1"}, 5)
	assert.Error(t, err)
}

func TestCreateSavedNIKWithinCap_CapReached(t *testing.T) {
	callN := 0
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &mockTx{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			callN++
			if callN == 1 {
				return rowValues("acc-1") // FOR UPDATE lock
			}
			return rowValues(5) // count == cap
		}}
		return tx, nil
	}}
	_, err := repo(db).CreateSavedNIKWithinCap(ctx, entity.SavedNIK{AccountID: "acc-1"}, 5)
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperror.CodeMaxSavedNIKsReached, ae.ErrorCode)
}

func TestCreateSavedNIKWithinCap_DuplicateNIK(t *testing.T) {
	callN := 0
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &mockTx{
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				callN++
				if callN == 1 {
					return rowValues("acc-1") // lock
				}
				return rowValues(0) // count ok
			},
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, &pgconn.PgError{
					Code:           codeUniqueViolation,
					ConstraintName: indexSavedNIKPerAcc,
				}
			},
		}
		return tx, nil
	}}
	_, err := repo(db).CreateSavedNIKWithinCap(ctx, entity.SavedNIK{AccountID: "acc-1"}, 5)
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperror.CodeNIKAlreadySaved, ae.ErrorCode)
}

func TestCreateSavedNIKWithinCap_OK(t *testing.T) {
	callN := 0
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &mockTx{
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				callN++
				if callN == 1 {
					return rowValues("acc-1")
				}
				return rowValues(2) // under cap
			},
		}
		return tx, nil
	}}
	nik, err := repo(db).CreateSavedNIKWithinCap(ctx, entity.SavedNIK{ID: "n-1", AccountID: "acc-1"}, 5)
	require.NoError(t, err)
	assert.Equal(t, "n-1", nik.ID)
}
