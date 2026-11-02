package http

import "net/http"

// RegisterRoutes registers booking HTTP routes on the given mux
func RegisterRoutes(mux *http.ServeMux, handler Booking) {
	mux.HandleFunc("POST /bookings", handler.CreateBooking)
	mux.HandleFunc("GET /bookings/{booking_id}", handler.GetBooking)
}
