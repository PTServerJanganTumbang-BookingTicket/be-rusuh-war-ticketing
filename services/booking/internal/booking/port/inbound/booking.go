package inbound

import (
	"context"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
)

// IBookingUsecase defines the inbound port (driving port) for booking business logic.
type IBookingUsecase interface {
	CreateBooking(ctx context.Context, req dto.CreateBookingRequest) (dto.BookingResponse, error)
	GetBooking(ctx context.Context, id string) (dto.BookingResponse, error)
}
