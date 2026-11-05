package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	mock_outbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/_mock/booking/port/outbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
)

func TestBookingUsecase_CreateBooking(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mock_outbound.NewMockIBookingRepository(ctrl)
	uc := usecase.NewBooking(repo)

	t.Run("success returns PENDING_PAYMENT booking with priced summary", func(t *testing.T) {
		repo.EXPECT().
			Save(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, b entity.Booking) (entity.Booking, error) {
				return b, nil
			})

		res, err := uc.CreateBooking(context.Background(), validRequest())

		assert.Nil(t, err)
		assert.NotEmpty(t, res.BookingID)
		assert.Equal(t, string(entity.StatusPendingPayment), res.Status)
		assert.Len(t, res.Tickets, 1)
		assert.Equal(t, "VIP", res.Tickets[0].Category)
		assert.Equal(t, int64(1500000), res.Summary.Total)
		assert.Equal(t, "3171********xx01", res.Tickets[0].NIKMasked)
		assert.LessOrEqual(t, res.PaymentTTL.TTLSeconds, entity.PaymentTTLSeconds)
	})

	t.Run("unknown category is bad request", func(t *testing.T) {
		req := validRequest()
		req.Tickets[0].CategoryID = "cat_unknown"

		_, err := uc.CreateBooking(context.Background(), req)

		assertCode(t, err, "40000")
	})

	t.Run("more than 3 tickets is bad request", func(t *testing.T) {
		req := validRequest()
		req.Tickets = []dto.TicketRequest{
			{CategoryID: "cat_vip", NIK: "3171000000000001", HolderEmail: "a@example.com"},
			{CategoryID: "cat_vip", NIK: "3171000000000002", HolderEmail: "b@example.com"},
			{CategoryID: "cat_vip", NIK: "3171000000000003", HolderEmail: "c@example.com"},
			{CategoryID: "cat_vip", NIK: "3171000000000004", HolderEmail: "d@example.com"},
		}

		_, err := uc.CreateBooking(context.Background(), req)

		assertCode(t, err, "40000")
	})

	t.Run("repository error is propagated", func(t *testing.T) {
		repo.EXPECT().
			Save(gomock.Any(), gomock.Any()).
			Return(entity.Booking{}, apperror.ErrInternal("db down"))

		_, err := uc.CreateBooking(context.Background(), validRequest())

		assertCode(t, err, "50000")
	})
}
