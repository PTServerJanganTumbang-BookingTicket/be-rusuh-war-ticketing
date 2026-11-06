package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
)

// CreateBooking handles POST /bookings
func (c *BookingController) CreateBooking(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body"))
		return
	}

	if err := c.validate.Struct(req); err != nil {
		writeError(w, apperror.ErrBadRequest(err.Error()))
		return
	}

	res, err := c.bookingUsecase.CreateBooking(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}

	apinizer.WriteEnvelope(w, http.StatusCreated, "Booking created successfully", res)
}

// GetBooking handles GET /bookings/{booking_id}
func (c *BookingController) GetBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("booking_id")

	res, err := c.bookingUsecase.GetBooking(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}

	apinizer.WriteEnvelope(w, http.StatusOK, "Success", res)
}

// writeError maps an error to the response envelope. A known *apperror.AppError
// carries its own HTTP status and message; any other error is a 500.
func writeError(w http.ResponseWriter, err error) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		apinizer.WriteEnvelope(w, appErr.HTTPStatus, appErr.Message, nil)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusInternalServerError, "internal server error", nil)
}
