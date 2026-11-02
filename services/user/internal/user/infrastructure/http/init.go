package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apinizer"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	portin "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/port/inbound"
)

// User is the HTTP handler interface for all user-domain endpoints.
// The composition root wires a concrete implementation (UserController) to this.
type User interface {
	// Auth
	RequestMagicLink(w http.ResponseWriter, r *http.Request)
	ResendMagicLink(w http.ResponseWriter, r *http.Request)
	VerifyMagicLink(w http.ResponseWriter, r *http.Request)
	Register(w http.ResponseWriter, r *http.Request)

	// Account / Saved NIKs
	GetSavedNIKs(w http.ResponseWriter, r *http.Request)
	AddSavedNIK(w http.ResponseWriter, r *http.Request)
	DeleteSavedNIK(w http.ResponseWriter, r *http.Request)

	// Tickets
	GetMyTickets(w http.ResponseWriter, r *http.Request)
	GetETicket(w http.ResponseWriter, r *http.Request)
}

// UserController is the concrete HTTP adapter that translates HTTP
// requests to use case calls and HTTP responses.
type UserController struct {
	usecase  portin.IUserUsecase
	validate *validator.Validate
}

// NewUser creates a UserController and returns it behind the User interface.
func NewUser(uc portin.IUserUsecase) User {
	return &UserController{
		usecase:  uc,
		validate: validator.New(),
	}
}

// requireAuth extracts and validates the Bearer token, returning the accountID.
// It writes an error response and returns ("", false) on failure.
func (c *UserController) requireAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeError(w, apperror.ErrUnauthorized("missing or invalid Authorization header"))
		return "", false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	accountID, err := c.usecase.ValidateAccessToken(r.Context(), token)
	if err != nil {
		writeError(w, err)
		return "", false
	}
	return accountID, true
}

// writeError maps an error to the response envelope.
// A known *apperror.AppError carries its own HTTP status and message;
// any other error falls back to 500.
func writeError(w http.ResponseWriter, err error) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		apinizer.WriteEnvelope(w, appErr.HTTPStatus, appErr.Message, nil)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusInternalServerError, "internal server error", nil)
}
