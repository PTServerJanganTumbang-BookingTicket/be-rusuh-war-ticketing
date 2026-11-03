package postgres

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

var ctx = context.Background()

func repo(db DBConn) *UserRepository {
	return &UserRepository{pool: db}
}

// ── FindAccountByEmail ────────────────────────────────────────────────────────

func TestFindAccountByEmail_Found(t *testing.T) {
	now := time.Now()
	db := &mockDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return &mockRow{scanFn: func(dest ...any) error {
				*dest[0].(*string) = "id-1"
				*dest[1].(*string) = "a@b.com"
				*dest[2].(**time.Time) = &now // EmailVerifiedAt *time.Time
				*dest[3].(*string) = ""
				*dest[4].(*string) = ""
				*dest[5].(**time.Time) = nil  // NIKVerifiedAt *time.Time
				*dest[6].(*string) = ""
				*dest[7].(*time.Time) = now  // CreatedAt time.Time
				return nil
			}}
		},
	}
	a, err := repo(db).FindAccountByEmail(ctx, "a@b.com")
	require.NoError(t, err)
	assert.Equal(t, "id-1", a.ID)
	assert.Equal(t, "a@b.com", a.Email)
}

func TestFindAccountByEmail_NotFound(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}
	_, err := repo(db).FindAccountByEmail(ctx, "x@y.com")
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, http.StatusNotFound, ae.HTTPStatus)
}

func TestFindAccountByEmail_DBError(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(errors.New("connection refused"))
	}}
	_, err := repo(db).FindAccountByEmail(ctx, "a@b.com")
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, http.StatusInternalServerError, ae.HTTPStatus)
}

// ── FindAccountByID ───────────────────────────────────────────────────────────

func TestFindAccountByID_NotFound(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}
	_, err := repo(db).FindAccountByID(ctx, "missing")
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, http.StatusNotFound, ae.HTTPStatus)
}

// ── HasConsent ────────────────────────────────────────────────────────────────

func TestHasConsent_True(t *testing.T) {
	db := &mockDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return rowValues(true)
		},
	}
	ok, err := repo(db).HasConsent(ctx, "acc-1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHasConsent_False(t *testing.T) {
	db := &mockDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return rowValues(false)
		},
	}
	ok, err := repo(db).HasConsent(ctx, "acc-1")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHasConsent_DBError(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(errors.New("db down"))
	}}
	_, err := repo(db).HasConsent(ctx, "acc-1")
	assert.Error(t, err)
}

// ── MarkEmailVerified ─────────────────────────────────────────────────────────

func TestMarkEmailVerified_OK(t *testing.T) {
	db := &mockDB{}
	assert.NoError(t, repo(db).MarkEmailVerified(ctx, "acc-1"))
}

func TestMarkEmailVerified_DBError(t *testing.T) {
	db := &mockDB{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, errors.New("write failed")
	}}
	err := repo(db).MarkEmailVerified(ctx, "acc-1")
	assert.Error(t, err)
}

// ── RegisterAccount ───────────────────────────────────────────────────────────

func TestRegisterAccount_BeginFails(t *testing.T) {
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		return nil, errors.New("no connection")
	}}
	_, err := repo(db).RegisterAccount(ctx, portout.Registration{Account: entity.Account{ID: "x"}})
	assert.Error(t, err)
}

func TestRegisterAccount_InsertAccountFails_EmailConflict(t *testing.T) {
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &mockTx{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, &pgconn.PgError{Code: codeUniqueViolation, ConstraintName: indexAccountEmail}
		}}
		return tx, nil
	}}
	_, err := repo(db).RegisterAccount(ctx, portout.Registration{Account: entity.Account{ID: "x"}})
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperror.CodeEmailAlreadyUsed, ae.ErrorCode)
}

func TestRegisterAccount_CommitFails(t *testing.T) {
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &mockTx{
			execFn:   func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) { return pgconn.CommandTag{}, nil },
			commitFn: func(_ context.Context) error { return errors.New("commit failed") },
		}
		return tx, nil
	}}
	_, err := repo(db).RegisterAccount(ctx, portout.Registration{
		Account: entity.Account{ID: "acc-1"},
		Consent: entity.ConsentRecord{ID: "c-1"},
	})
	assert.Error(t, err)
}
