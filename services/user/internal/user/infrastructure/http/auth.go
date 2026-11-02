package http

import (
	"encoding/json"
	"net/http"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apinizer"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// RequestMagicLink handles POST /auth/magic-link (FR-31).
func (c *UserController) RequestMagicLink(w http.ResponseWriter, r *http.Request) {
	var req dto.RequestMagicLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body"))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		writeError(w, apperror.ErrBadRequest(err.Error()))
		return
	}
	if err := c.usecase.RequestMagicLink(r.Context(), req); err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Login link sent to your email", nil)
}

// ResendMagicLink handles POST /auth/magic-link/resend.
// It delegates to the same use-case method as RequestMagicLink.
func (c *UserController) ResendMagicLink(w http.ResponseWriter, r *http.Request) {
	var req dto.RequestMagicLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body"))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		writeError(w, apperror.ErrBadRequest(err.Error()))
		return
	}
	if err := c.usecase.RequestMagicLink(r.Context(), req); err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "New login link sent", nil)
}

// VerifyMagicLink handles GET /auth/verify?token=<magic_token> (FR-31, BR-09).
// The token arrives as a query parameter (the email link redirect target).
// On success it sets an HttpOnly cookie with the access token for web clients.
func (c *UserController) VerifyMagicLink(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, apperror.ErrBadRequest("token query parameter is required"))
		return
	}
	resp, err := c.usecase.VerifyMagicLink(r.Context(), token)
	if err != nil {
		writeError(w, err)
		return
	}
	// Set HttpOnly cookie for web clients (mobile clients read from the JSON body).
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    resp.AccessToken,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   resp.ExpiresIn,
		Path:     "/",
	})
	apinizer.WriteEnvelope(w, http.StatusOK, "Login successful", resp)
}

// Register handles POST /auth/register (FR-29, FR-30, FR-31).
func (c *UserController) Register(w http.ResponseWriter, r *http.Request) {
	var req dto.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body"))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		writeError(w, apperror.ErrBadRequest(err.Error()))
		return
	}
	resp, err := c.usecase.Register(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Verification link sent to your email", resp)
}
