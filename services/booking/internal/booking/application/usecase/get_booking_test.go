package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	mock_outbound "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/_mock/booking/port/outbound"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/entity"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
)

func TestBookingUsecase_GetBooking(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mock_outbound.NewMockIBookingRepository(ctrl)
	uc := usecase.NewBooking(repo)

	t.Run("empty id is bad request", func(t *testing.T) {
		_, err := uc.GetBooking(context.Background(), "")
		assertCode(t, err, "40000")
	})

	t.Run("found booking is mapped to contract response", func(t *testing.T) {
		repo.EXPECT().
			FindByID(gomock.Any(), "RB-00001").
			Return(entity.Booking{
				ID:     "RB-00001",
				Status: entity.StatusPendingPayment,
				Event:  entity.Event{ID: "evt_rusuh_2026", Name: "Rusuh Fest"},
				Tickets: []entity.Ticket{
					{CategoryID: "cat_vip", CategoryName: "VIP", Price: 1500000, NIK: "3171000000000001"},
				},
				Subtotal: 1500000,
				Total:    1500000,
			}, nil)

		res, err := uc.GetBooking(context.Background(), "RB-00001")
		assert.Nil(t, err)
		assert.Equal(t, "RB-00001", res.BookingID)
		assert.Equal(t, string(entity.StatusPendingPayment), res.Status)
		assert.Equal(t, "3171********xx01", res.Tickets[0].NIKMasked)
	})

	t.Run("not found is propagated", func(t *testing.T) {
		repo.EXPECT().
			FindByID(gomock.Any(), "missing").
			Return(entity.Booking{}, apperror.ErrNotFound("booking not found"))

		_, err := uc.GetBooking(context.Background(), "missing")
		assertCode(t, err, "40400")
	})
}
