package http

import (
	"net/http"

	"github.com/go-playground/validator/v10"

	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/inbound"
)

// Booking is the HTTP handler interface for booking endpoints.
type Booking interface {
	CreateBooking(w http.ResponseWriter, r *http.Request)
	GetBooking(w http.ResponseWriter, r *http.Request)
}

// BookingController is the concrete handler implementation.
type BookingController struct {
	bookingUsecase portin.IBookingUsecase
	validate       *validator.Validate
}

// NewBooking creates a new booking HTTP handler and returns the Booking interface.
func NewBooking(uc portin.IBookingUsecase) Booking {
	return &BookingController{
		bookingUsecase: uc,
		validate:       validator.New(),
	}
}
