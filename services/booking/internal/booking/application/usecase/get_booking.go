package usecase

import (
	"context"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain/mapper"
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
