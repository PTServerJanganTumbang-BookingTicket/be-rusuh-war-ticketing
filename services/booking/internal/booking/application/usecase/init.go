package usecase

import (
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/inbound"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// BookingUsecase implements the inbound port for booking business logic.
type BookingUsecase struct {
	repo portout.IBookingRepository
}

// NewBooking creates a new booking usecase and returns the inbound port interface.
func NewBooking(repo portout.IBookingRepository) portin.IBookingUsecase {
	return &BookingUsecase{
		repo: repo,
	}
}
