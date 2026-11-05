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

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// ── CreateRefreshToken ────────────────────────────────────────────────────────

func TestCreateRefreshToken_OK(t *testing.T) {
	db := &mockDB{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, nil
	}}
	err := repo(db).CreateRefreshToken(ctx, entity.RefreshToken{ID: "t-1"})
	assert.NoError(t, err)
}

func TestCreateRefreshToken_DBError(t *testing.T) {
	db := &mockDB{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, errors.New("insert failed")
	}}
	err := repo(db).CreateRefreshToken(ctx, entity.RefreshToken{ID: "t-1"})
	assert.Error(t, err)
}

// ── RevokeRefreshFamily ───────────────────────────────────────────────────────

func TestRevokeRefreshFamily_OK(t *testing.T) {
	db := &mockDB{}
	assert.NoError(t, repo(db).RevokeRefreshFamily(ctx, "family-1"))
}

func TestRevokeRefreshFamily_DBError(t *testing.T) {
	db := &mockDB{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, errors.New("revoke failed")
	}}
	assert.Error(t, repo(db).RevokeRefreshFamily(ctx, "family-1"))
}

// ── RotateRefreshToken ────────────────────────────────────────────────────────

func TestRotateRefreshToken_BeginFails(t *testing.T) {
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		return nil, errors.New("no connection")
	}}
	_, err := repo(db).RotateRefreshToken(ctx, "hash", entity.RefreshToken{})
	assert.Error(t, err)
}

func TestRotateRefreshToken_RedeemFails_NoRows(t *testing.T) {
	// ErrNoRows → classifyFailedRotation path.
	// The classify call also uses pool.QueryRow; make it return ErrNoRows
	// (token unknown) to hit the ErrNotFound path cleanly.
	db := &mockDB{
		beginFn: func(_ context.Context) (pgx.Tx, error) {
			tx := &mockTx{
				queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
					return rowErr(pgx.ErrNoRows)
				},
			}
			return tx, nil
		},
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return rowErr(pgx.ErrNoRows)
		},
	}
	_, err := repo(db).RotateRefreshToken(ctx, "old-hash", entity.RefreshToken{})
	require.Error(t, err)
}

func TestRotateRefreshToken_CommitFails(t *testing.T) {
	db := &mockDB{
		beginFn: func(_ context.Context) (pgx.Tx, error) {
			callN := 0
			tx := &mockTx{
				queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
					// First QueryRow in tx: redeemRefreshToken returns a row
					return rowValues("redeemed-id", "acc-1", "family-1")
				},
				execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
					callN++
					return pgconn.CommandTag{}, nil
				},
				commitFn: func(_ context.Context) error { return errors.New("commit failed") },
			}
			return tx, nil
		},
	}
	_, err := repo(db).RotateRefreshToken(ctx, "old-hash", entity.RefreshToken{
		ID: "new-1", IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	assert.Error(t, err)
}

// ── classifyFailedRotation ────────────────────────────────────────────────────

func TestClassifyFailedRotation_UnknownToken(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}
	err := repo(db).classifyFailedRotation(ctx, "hash")
	require.Error(t, err)
	assert.NotErrorIs(t, err, portout.ErrRefreshTokenReplayed)
}

func TestClassifyFailedRotation_RevokedOrExpired(t *testing.T) {
	// token exists, used == false → not a replay
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowValues("family-1", false)
	}}
	err := repo(db).classifyFailedRotation(ctx, "hash")
	require.Error(t, err)
	assert.NotErrorIs(t, err, portout.ErrRefreshTokenReplayed)
}

func TestClassifyFailedRotation_Replay(t *testing.T) {
	// token exists, used == true, revoke succeeds → ErrRefreshTokenReplayed
	execCalled := false
	db := &mockDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return rowValues("family-1", true)
		},
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			execCalled = true
			return pgconn.CommandTag{}, nil
		},
	}
	err := repo(db).classifyFailedRotation(ctx, "hash")
	assert.ErrorIs(t, err, portout.ErrRefreshTokenReplayed)
	assert.True(t, execCalled, "RevokeRefreshFamily must be called on replay")
}
