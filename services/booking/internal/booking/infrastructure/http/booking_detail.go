package http

import (
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
)

// BookingDetail handles GET /bookings/{booking_id}. It must sit behind pkg/grantauth.Middleware.
type BookingDetail struct{ uc *usecase.BookingDetail }

// NewBookingDetail builds the handler.
func NewBookingDetail(uc *usecase.BookingDetail) *BookingDetail { return &BookingDetail{uc: uc} }

// ServeHTTP renders the booking. Without a grant in the context (route wired without the middleware) it refuses.
func (h *BookingDetail) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g, ok := grantauth.FromContext(r.Context())
	if !ok {
		apinizer.WriteError(w, grantauth.ErrMissing())
		return
	}
	view, err := h.uc.Get(r.Context(), g, r.PathValue("booking_id"))
	if err != nil {
		apinizer.WriteError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", view)
}
