package delivery

import (
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
	bookinghttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/infrastructure/http"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/infrastructure/memory"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
)

// New creates and wires the HTTP server with all dependencies.
func New(_ config.Config) *http.ServeMux {
	mux := http.NewServeMux()

	apinizer.RegisterHealthCheck(mux, "/sys/ping")

	// Wire: repository -> usecase -> controller
	repo := memory.NewBookingRepository()
	uc := usecase.NewBooking(repo)
	handler := bookinghttp.NewBooking(uc)

	bookinghttp.RegisterRoutes(mux, handler)

	return mux
}
