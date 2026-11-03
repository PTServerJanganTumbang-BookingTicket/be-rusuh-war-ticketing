package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/inbound"
)

// User is the HTTP handler interface for all user-domain endpoints.
type User interface {
	RequestMagicLink(w http.ResponseWriter, r *http.Request)
	ResendMagicLink(w http.ResponseWriter, r *http.Request)
	VerifyMagicLink(w http.ResponseWriter, r *http.Request)
	VerifyEmail(w http.ResponseWriter, r *http.Request)
	Register(w http.ResponseWriter, r *http.Request)
	RefreshSession(w http.ResponseWriter, r *http.Request)

	GetSavedNIKs(w http.ResponseWriter, r *http.Request)
	AddSavedNIK(w http.ResponseWriter, r *http.Request)
	DeleteSavedNIK(w http.ResponseWriter, r *http.Request)

	GetMyTickets(w http.ResponseWriter, r *http.Request)
	GetETicket(w http.ResponseWriter, r *http.Request)
}

// Options configures cookie behaviour for the controller.
type Options struct {
	// SecureCookies marks session cookies Secure. False only for plain-HTTP
	// local development, where a Secure cookie is silently dropped.
	SecureCookies bool
	// CookieDomain scopes the session cookie; empty means host-only.
	CookieDomain string
}

// UserController translates HTTP to use case calls and back.
type UserController struct {
	usecase  portin.IUserUsecase
	validate *validator.Validate
	opts     Options
}

// NewUser creates a UserController behind the User interface.
func NewUser(uc portin.IUserUsecase, opts Options) User {
	return &UserController{usecase: uc, validate: validator.New(), opts: opts}
}

// requireAuth extracts and verifies the bearer token, returning the account ID.
// It writes an error response and returns ("", false) on failure.
func (c *UserController) requireAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeError(w, apperror.
			ErrUnauthorized("missing or invalid Authorization header").
			WithCode(apperror.CodeTokenInvalid))
		return "", false
	}
	accountID, err := c.usecase.ValidateAccessToken(r.Context(), strings.TrimPrefix(header, "Bearer "))
	if err != nil {
		writeError(w, err)
		return "", false
	}
	return accountID, true
}

// requireSameOrigin rejects cross-site state-changing requests (§9 CSRF).
//
// Only enforced when the browser sent an Origin: native clients and
// server-to-server callers do not, and rejecting those would break the shared
// web/mobile API that NFR-08 requires.
func (c *UserController) requireSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if c.opts.CookieDomain != "" && !strings.Contains(origin, c.opts.CookieDomain) {
		writeError(w, apperror.ErrForbidden("cross-origin request rejected"))
		return false
	}
	return true
}

// writeError maps an error onto the response envelope.
//
// The Appendix A code travels in meta.error_code so clients switch on a machine
// value instead of parsing meta.message, and a retry hint becomes a real
// Retry-After header as well as a field.
func writeError(w http.ResponseWriter, err error) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		var data any
		if appErr.RetryAfter > 0 {
			seconds := int(appErr.RetryAfter.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			data = map[string]any{"retry_after": seconds}
		}
		apinizer.WriteErrorEnvelope(w, appErr.HTTPStatus, appErr.ErrorCode, appErr.Message, data)
		return
	}
	apinizer.WriteErrorEnvelope(w, http.StatusInternalServerError, "", "internal server error", nil)
}
