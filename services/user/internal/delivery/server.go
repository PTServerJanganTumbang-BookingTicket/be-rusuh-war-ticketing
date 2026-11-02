package delivery

import (
	"net/http"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apinizer"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/config"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/application/usecase"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/infrastructure/dukcapil"
	userhttp "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/infrastructure/http"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/infrastructure/memory"
)

// New creates and wires the HTTP server with all dependencies.
// Dependency order: repository → dukcapil adapter → usecase → controller → routes.
func New(cfg config.Config) *http.ServeMux {
	mux := http.NewServeMux()

	apinizer.RegisterHealthCheck(mux, "/sys/ping")

	// Wire: repository + dukcapil → usecase → controller
	repo := memory.NewUserRepository()
	dukcapilAdapter := dukcapil.NewStubAdapter()
	uc := usecase.NewUser(repo, dukcapilAdapter, cfg.App.NIKHMACKey)
	handler := userhttp.NewUser(uc)

	userhttp.RegisterRoutes(mux, handler)

	return mux
}
