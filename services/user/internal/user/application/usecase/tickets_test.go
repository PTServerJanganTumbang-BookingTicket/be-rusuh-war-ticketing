package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
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
		NIKMasked:     "3171**********22",
		QRNonce:       "nonce-abc",
		IssuedAt:      time.Now().UTC(),
		Status:        entity.TicketStatusValid,
	}
}

// ---- GetMyTickets ----

func TestGetMyTickets_CarriesTheTotalForPagination(t *testing.T) {
	// The gap this closes: the total used to be computed and then dropped, so
	// meta.pagination never reached the client (contract §6.1).
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindTicketsByAccountID(gomock.Any(), testAccountID, gomock.Any()).
		Return([]entity.UserTicket{sampleTicket()}, 42, nil)

	resp, err := uc.GetMyTickets(context.Background(), testAccountID, dto.GetMyTicketsRequest{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 42, resp.Total)
	assert.Equal(t, 10, resp.Limit)
	require.Len(t, resp.Tickets, 1)
	assert.Equal(t, "TKT-001", resp.Tickets[0].TicketID)
	assert.Equal(t, "/tickets/TKT-001/eticket", resp.Tickets[0].ETicketURL)
}

func TestGetMyTickets_DefaultsAndClamps(t *testing.T) {
	cases := []struct {
		name      string
		in        dto.GetMyTicketsRequest
		wantLimit int
		wantOff   int
	}{
		{"zero limit defaults", dto.GetMyTicketsRequest{}, defaultLimit, 0},
		{"negative offset floors", dto.GetMyTicketsRequest{Limit: 5, Offset: -3}, 5, 0},
		{"oversized limit clamps", dto.GetMyTicketsRequest{Limit: 9999}, maxLimit, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc, d := newTestUsecase(t)
			d.repo.EXPECT().FindTicketsByAccountID(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, req dto.GetMyTicketsRequest) ([]entity.UserTicket, int, error) {
					assert.Equal(t, tc.wantLimit, req.Limit)
					assert.Equal(t, tc.wantOff, req.Offset)
					return []entity.UserTicket{}, 0, nil
				})

			resp, err := uc.GetMyTickets(context.Background(), testAccountID, tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.wantLimit, resp.Limit)
		})
	}
}

func TestGetMyTickets_EmptyMarshalsAsArray(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindTicketsByAccountID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]entity.UserTicket{}, 0, nil)

	resp, err := uc.GetMyTickets(context.Background(), testAccountID, dto.GetMyTicketsRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp.Tickets)
	assert.Empty(t, resp.Tickets)
}

func TestGetMyTickets_RepoError(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindTicketsByAccountID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, 0, errors.New("db error"))

	_, err := uc.GetMyTickets(context.Background(), testAccountID, dto.GetMyTicketsRequest{})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 500, appErr.HTTPStatus)
}

// ---- GetETicket ----

func TestGetETicket_Success(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindTicketByID(gomock.Any(), testAccountID, "TKT-001").Return(sampleTicket(), nil)

	resp, err := uc.GetETicket(context.Background(), testAccountID, "TKT-001")
	require.NoError(t, err)
	assert.Equal(t, "TKT-001", resp.TicketID)
	assert.Equal(t, "VIP", resp.Category)
	assert.Equal(t, "3171**********22", resp.Holder.NIKMasked)
	assert.Equal(t, "Konser A", resp.Event.Name)
}

func TestGetETicket_QRCarriesNoIdentity(t *testing.T) {
	// A QR is screenshotted, printed and photographed in queues. The gate
	// resolves the ticket from it and matches the attendee's physical KTP
	// against the stored hash, so it never needs the NIK.
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindTicketByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(sampleTicket(), nil)

	resp, err := uc.GetETicket(context.Background(), testAccountID, "TKT-001")
	require.NoError(t, err)
	assert.Equal(t, "TKT-001.nonce-abc", resp.QRValue)
	assert.NotContains(t, resp.QRValue, "3171")
	assert.NotContains(t, resp.QRValue, "*")
}

func TestGetETicket_NotFoundAndNotYoursAreIndistinguishable(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindTicketByID(gomock.Any(), testAccountID, "TKT-other").
		Return(entity.UserTicket{}, apperror.ErrNotFound("ticket not found"))

	_, err := uc.GetETicket(context.Background(), testAccountID, "TKT-other")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 404, appErr.HTTPStatus)
}

func TestGetETicket_UnexpectedErrorStillHidesExistence(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindTicketByID(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(entity.UserTicket{}, errors.New("db down"))

	_, err := uc.GetETicket(context.Background(), testAccountID, "TKT-001")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 404, appErr.HTTPStatus)
}
