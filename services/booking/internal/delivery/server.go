package delivery

import (
	"net/http"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
	bookinghttp "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/infrastructure/http"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/infrastructure/memory"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/config"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apinizer"
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
