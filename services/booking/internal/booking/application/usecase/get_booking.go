package usecase

import (
	"context"
	"time"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/mapper"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
)

func (u *BookingUsecase) GetBooking(ctx context.Context, id string) (dto.BookingResponse, error) {
	if id == "" {
		return dto.BookingResponse{}, apperror.ErrBadRequest("id is required")
	}

	booking, err := u.repo.FindByID(ctx, id)
	if err != nil {
		return dto.BookingResponse{}, err
	}

	return mapper.ToBookingResponse(booking, time.Now().UTC()), nil
}
