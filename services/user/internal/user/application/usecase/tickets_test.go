package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

func sampleTicket() entity.UserTicket {
	return entity.UserTicket{
		TicketID:      "TKT-001",
		BookingID:     "RB-00912",
		EventID:       "evt_konser_a",
		EventName:     "Konser A",
		EventDate:     "2026-12-25",
		EventTime:     "19:00",
		EventVenue:    "GBK, Jakarta",
		EventImageURL: "https://cdn.rusuh.id/events/konser-a.jpg",
		Category:      "VIP",
		Price:         1500000,
		NIKHmac:       "hmac-placeholder",
		QRPayload:     "TKT-001|evt_konser_a|hmac",
		IssuedAt:      time.Now().UTC(),
		Status:        entity.TicketStatusValid,
	}
}

// ---- GetMyTickets ----

func TestGetMyTickets_success(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().
		FindTicketsByAccountID(gomock.Any(), testAccountID, gomock.Any()).
		Return([]entity.UserTicket{sampleTicket()}, 1, nil)

	resp, err := uc.GetMyTickets(context.Background(), testAccountID, dto.GetMyTicketsRequest{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Pagination.TotalData)
	assert.Len(t, resp.Tickets, 1)
	assert.Equal(t, "TKT-001", resp.Tickets[0].TicketID)
}

func TestGetMyTickets_empty(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().
		FindTicketsByAccountID(gomock.Any(), testAccountID, gomock.Any()).
		Return([]entity.UserTicket{}, 0, nil)

	resp, err := uc.GetMyTickets(context.Background(), testAccountID, dto.GetMyTicketsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Tickets)
	assert.Equal(t, defaultLimit, resp.Pagination.Limit)
}

func TestGetMyTickets_clampLimit(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().
		FindTicketsByAccountID(gomock.Any(), testAccountID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req dto.GetMyTicketsRequest) ([]entity.UserTicket, int, error) {
			assert.Equal(t, maxLimit, req.Limit, "limit must be clamped to maxLimit")
			return []entity.UserTicket{}, 0, nil
		})

	_, err := uc.GetMyTickets(context.Background(), testAccountID, dto.GetMyTicketsRequest{Limit: 9999})
	require.NoError(t, err)
}

func TestGetMyTickets_repoError(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().
		FindTicketsByAccountID(gomock.Any(), testAccountID, gomock.Any()).
		Return(nil, 0, errors.New("db error"))

	_, err := uc.GetMyTickets(context.Background(), testAccountID, dto.GetMyTicketsRequest{})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 500, appErr.HTTPStatus)
}

// ---- GetETicket ----

func TestGetETicket_success(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().
		FindTicketByID(gomock.Any(), testAccountID, "TKT-001").
		Return(sampleTicket(), nil)

	resp, err := uc.GetETicket(context.Background(), testAccountID, "TKT-001")
	require.NoError(t, err)
	assert.Equal(t, "TKT-001", resp.TicketID)
	assert.Equal(t, "VIP", resp.Category)
	assert.Equal(t, "Tunjukkan QR code dan identitas pemegang saat masuk venue.", resp.Instructions)
}

func TestGetETicket_notFound(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().
		FindTicketByID(gomock.Any(), testAccountID, "TKT-missing").
		Return(entity.UserTicket{}, apperror.ErrNotFound("ticket not found"))

	_, err := uc.GetETicket(context.Background(), testAccountID, "TKT-missing")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 404, appErr.HTTPStatus)
}
