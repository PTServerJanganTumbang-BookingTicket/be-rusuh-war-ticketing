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
)

// ── SaveMagicTokenWithEvent ───────────────────────────────────────────────────

func TestSaveMagicTokenWithEvent_OK(t *testing.T) {
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		return &mockTx{}, nil
	}}
	err := repo(db).SaveMagicTokenWithEvent(ctx, entity.MagicToken{ID: "t-1"}, entity.OutboxEvent{ID: "e-1"})
	assert.NoError(t, err)
}

func TestSaveMagicTokenWithEvent_BeginFails(t *testing.T) {
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		return nil, errors.New("no conn")
	}}
	err := repo(db).SaveMagicTokenWithEvent(ctx, entity.MagicToken{}, entity.OutboxEvent{})
	assert.Error(t, err)
}

func TestSaveMagicTokenWithEvent_InsertTokenFails(t *testing.T) {
	db := &mockDB{beginFn: func(_ context.Context) (pgx.Tx, error) {
		tx := &mockTx{execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, errors.New("insert failed")
		}}
		return tx, nil
	}}
	err := repo(db).SaveMagicTokenWithEvent(ctx, entity.MagicToken{ID: "t-1"}, entity.OutboxEvent{})
	assert.Error(t, err)
}

// ── ConsumeMagicToken ─────────────────────────────────────────────────────────

func TestConsumeMagicToken_OK(t *testing.T) {
	now := time.Now()
	exp := now.Add(time.Hour)
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return &mockRow{scanFn: func(dest ...any) error {
			*dest[0].(*string) = "tok-1"
			*dest[1].(*string) = "acc-1"
			*dest[2].(*string) = "a@b.com"
			*dest[3].(*string) = "hash"
			*dest[4].(*string) = "verify_email"
			*dest[5].(*time.Time) = now
			*dest[6].(*time.Time) = exp
			*dest[7].(**time.Time) = &now // used_at *time.Time
			return nil
		}}
	}}
	tok, err := repo(db).ConsumeMagicToken(ctx, "hash", entity.PurposeVerifyEmail)
	require.NoError(t, err)
	assert.Equal(t, "tok-1", tok.ID)
}

func TestConsumeMagicToken_NotUsable(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}
	_, err := repo(db).ConsumeMagicToken(ctx, "expired", entity.PurposeVerifyEmail)
	require.Error(t, err)
}

func TestConsumeMagicToken_DBError(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(errors.New("scan error"))
	}}
	_, err := repo(db).ConsumeMagicToken(ctx, "hash", entity.PurposeVerifyEmail)
	assert.Error(t, err)
}
