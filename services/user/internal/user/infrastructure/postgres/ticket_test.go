package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// ── FindTicketByID ────────────────────────────────────────────────────────────

func TestFindTicketByID_NotFound(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}
	_, err := repo(db).FindTicketByID(ctx, "acc-1", "ticket-missing")
	assert.Error(t, err)
}

func TestFindTicketByID_DBError(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(errors.New("db down"))
	}}
	_, err := repo(db).FindTicketByID(ctx, "acc-1", "t-1")
	assert.Error(t, err)
}

// ── FindTicketsByAccountID ────────────────────────────────────────────────────

func TestFindTicketsByAccountID_CountFails(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
		return rowErr(errors.New("count failed"))
	}}
	_, _, err := repo(db).FindTicketsByAccountID(ctx, "acc-1", dto.GetMyTicketsRequest{Limit: 10})
	assert.Error(t, err)
}

func TestFindTicketsByAccountID_ZeroResults(t *testing.T) {
	rowCalled := false
	db := &mockDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			rowCalled = true
			return rowValues(0) // count = 0
		},
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &mockRows{}, nil
		},
	}
	tickets, total, err := repo(db).FindTicketsByAccountID(ctx, "acc-1", dto.GetMyTicketsRequest{Limit: 10})
	require.NoError(t, err)
	assert.True(t, rowCalled)
	assert.Equal(t, 0, total)
	assert.Empty(t, tickets)
}

func TestFindTicketsByAccountID_QueryFails(t *testing.T) {
	db := &mockDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return rowValues(2)
		},
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return nil, errors.New("page query failed")
		},
	}
	_, _, err := repo(db).FindTicketsByAccountID(ctx, "acc-1", dto.GetMyTicketsRequest{Limit: 10})
	assert.Error(t, err)
}
