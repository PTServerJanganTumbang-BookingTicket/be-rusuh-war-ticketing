package http

import (
	"encoding/json"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// magicTokenCookie is the cookie the emailed link carries, per the OpenAPI
// spec for GET /auth/verify. A query parameter would land the credential in
// browser history, referrer headers and access logs.
const magicTokenCookie = "magic_token"

// accessTokenCookie holds the session for web clients. Mobile clients read the
// token from the response body instead (§9).
const accessTokenCookie = "access_token"

// RequestMagicLink handles POST /auth/magic-link (FR-31).
func (c *UserController) RequestMagicLink(w http.ResponseWriter, r *http.Request) {
	c.handleMagicLink(w, r, "Login link sent to your email")
}

// ResendMagicLink handles POST /auth/magic-link/resend.
func (c *UserController) ResendMagicLink(w http.ResponseWriter, r *http.Request) {
	c.handleMagicLink(w, r, "New login link sent")
}

func (c *UserController) handleMagicLink(w http.ResponseWriter, r *http.Request, message string) {
	if !c.requireSameOrigin(w, r) {
		return
	}
	var req dto.RequestMagicLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body").WithCode(apperror.CodeValidationError))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		writeError(w, apperror.ErrBadRequest("a valid email address is required").WithCode(apperror.CodeValidationError))
		return
	}
	if err := c.usecase.RequestMagicLink(r.Context(), req); err != nil {
		writeError(w, err)
		return
	}
	// The same 200 whether or not the address has an account: anything else
	// enumerates which addresses are registered.
	apinizer.WriteEnvelope(w, http.StatusOK, message, nil)
}

// VerifyMagicLink handles GET /auth/verify (FR-31).
//
// The token arrives in the magic_token cookie, as the OpenAPI spec declares.
// On success the cookie is cleared — it is single-use and leaving it set means
// a spent credential rides along on every later request.
func (c *UserController) VerifyMagicLink(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(magicTokenCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, apperror.
			ErrBadRequest("magic_token cookie is required").
			WithCode(apperror.CodeTokenInvalid))
		return
	}

	resp, err := c.usecase.VerifyMagicLink(r.Context(), cookie.Value)
	if err != nil {
		writeError(w, err)
		return
	}

	c.clearCookie(w, magicTokenCookie)
	http.SetCookie(w, &http.Cookie{ //NOSONAR - Secure follows SecureCookies config, enforced true in production
		Name:     accessTokenCookie,
		Value:    resp.AccessToken,
		Path:     "/",
		Domain:   c.opts.CookieDomain,
		HttpOnly: true,
		Secure:   c.opts.SecureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   resp.ExpiresIn,
	})
	apinizer.WriteEnvelope(w, http.StatusOK, "Login successful", resp)
}

// VerifyEmail handles GET /auth/verify-email (FR-31).
//
// A distinct endpoint and a distinct token purpose: confirming an address must
// not mint a session, or "please confirm your email" becomes a login bypass.
func (c *UserController) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(magicTokenCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, apperror.
			ErrBadRequest("magic_token cookie is required").
			WithCode(apperror.CodeTokenInvalid))
		return
	}
	if err := c.usecase.VerifyEmail(r.Context(), cookie.Value); err != nil {
		writeError(w, err)
		return
	}
	c.clearCookie(w, magicTokenCookie)
	apinizer.WriteEnvelope(w, http.StatusOK, "Email verified", nil)
}

// Register handles POST /auth/register (FR-29, FR-30, FR-31a..d).
func (c *UserController) Register(w http.ResponseWriter, r *http.Request) {
	if !c.requireSameOrigin(w, r) {
		return
	}
	var req dto.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body").WithCode(apperror.CodeValidationError))
		return
	}
	// Consent is checked in the use case, not by the validator: a missing
	// consent object and an unchecked box are different answers to the
	// customer, and CONSENT_REQUIRED (422) is the one the contract specifies.
	if req.Consent != nil {
		if err := c.validate.Struct(req); err != nil {
			writeError(w, apperror.ErrBadRequest("email, nik and consent are required").WithCode(apperror.CodeValidationError))
			return
		}
	}

	resp, err := c.usecase.Register(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Verification link sent to your email", resp)
}

func (c *UserController) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{ //NOSONAR - Secure follows SecureCookies config, enforced true in production
		Name:     name,
		Value:    "",
		Path:     "/",
		Domain:   c.opts.CookieDomain,
		HttpOnly: true,
		Secure:   c.opts.SecureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// RefreshSession handles POST /auth/refresh.
//
// Contract §1.4, added in v2.2.
//
// Not part of the booking journey, and not part of the wait either: the access
// token is derived from SALE_WINDOW_SECONDS plus the 930-second journey, so it
// outlives both and this is never called mid-payment OR in the waiting room. It
// exists to carry a session across days — the refresh token lasts 30 days —
// rather than to patch a token that is too short.
func (c *UserController) RefreshSession(w http.ResponseWriter, r *http.Request) {
	if !c.requireSameOrigin(w, r) {
		return
	}
	var req dto.RefreshSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body").WithCode(apperror.CodeValidationError))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		writeError(w, apperror.ErrBadRequest("refresh_token is required").WithCode(apperror.CodeValidationError))
		return
	}

	resp, err := c.usecase.RefreshSession(r.Context(), req.RefreshToken)
	if err != nil {
		// A failed refresh ends the session, so clear the cookie too —
		// otherwise a browser keeps presenting a token the server has
		// already written off.
		c.clearCookie(w, accessTokenCookie)
		writeError(w, err)
		return
	}

	http.SetCookie(w, &http.Cookie{ //NOSONAR - Secure follows SecureCookies config, enforced true in production
		Name:     accessTokenCookie,
		Value:    resp.AccessToken,
		Path:     "/",
		Domain:   c.opts.CookieDomain,
		HttpOnly: true,
		Secure:   c.opts.SecureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   resp.ExpiresIn,
	})
	apinizer.WriteEnvelope(w, http.StatusOK, "Session refreshed", resp)
}
