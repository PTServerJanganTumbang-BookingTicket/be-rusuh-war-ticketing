package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

func engRepo(db DBConn) *EngagementRepository {
	return &EngagementRepository{pool: db}
}

func TestFindActiveEngagement_None(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}
	eng, err := engRepo(db).FindActiveEngagement(ctx, "acc-1")
	require.NoError(t, err)
	assert.Equal(t, entity.EngagementNone, eng.Kind)
}

func TestFindActiveEngagement_Booking(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowValues("booking", "event-1", "booking-1")
	}}
	eng, err := engRepo(db).FindActiveEngagement(ctx, "acc-1")
	require.NoError(t, err)
	assert.Equal(t, entity.EngagementKind("booking"), eng.Kind)
	assert.Equal(t, "event-1", eng.EventID)
}

func TestFindActiveEngagement_DBError(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(errors.New("db error"))
	}}
	_, err := engRepo(db).FindActiveEngagement(ctx, "acc-1")
	assert.Error(t, err)
}
