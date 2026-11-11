package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

func postJSON(t *testing.T, mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// ---- magic link ----

func TestRequestMagicLink_Success(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().RequestMagicLink(gomock.Any(), dto.RequestMagicLinkRequest{Email: "a@example.com"}).Return(nil)

	rec := postJSON(t, mux, "/auth/magic-link", `{"email":"a@example.com"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestResendMagicLink_Success(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().RequestMagicLink(gomock.Any(), gomock.Any()).Return(nil)

	rec := postJSON(t, mux, "/auth/magic-link/resend", `{"email":"a@example.com"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequestMagicLink_InvalidEmail(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := postJSON(t, mux, "/auth/magic-link", `{"email":"not-an-email"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, apperror.CodeValidationError, metaOf(t, rec)["error_code"])
}

func TestRequestMagicLink_MalformedBody(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := postJSON(t, mux, "/auth/magic-link", `{nope`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRequestMagicLink_RateLimitedCarriesRetryAfter(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().RequestMagicLink(gomock.Any(), gomock.Any()).Return(
		apperror.ErrTooManyRequests("wait").WithCode(apperror.CodeTooManyRequests).WithRetryAfter(60_000_000_000))

	rec := postJSON(t, mux, "/auth/magic-link", `{"email":"a@example.com"}`)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "60", rec.Header().Get("Retry-After"))
	data := decode(t, rec)["data"].(map[string]any)
	assert.Equal(t, float64(60), data["retry_after"])
}

// ---- verify ----

func TestVerifyMagicLink_ReadsTheCookieNotAQueryParam(t *testing.T) {
	// The OpenAPI spec declares a required magic_token cookie. A query
	// parameter would land the credential in history, referrers and logs.
	mux, uc := newTestServer(t)
	uc.EXPECT().VerifyMagicLink(gomock.Any(), "tok-123").Return(dto.VerifyMagicLinkResponse{
		AccessToken: "jwt-abc", ExpiresIn: 22740,
		User: dto.UserSummary{ID: "USR-001", Email: "a@example.com", NIK: "3171**********22"},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/verify", nil)
	req.AddCookie(&http.Cookie{Name: "magic_token", Value: "tok-123"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var access, magic *http.Cookie
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case "access_token":
			access = c
		case "magic_token":
			magic = c
		}
	}
	require.NotNil(t, access)
	assert.Equal(t, "jwt-abc", access.Value)
	assert.True(t, access.HttpOnly)
	assert.True(t, access.Secure)
	assert.Equal(t, http.SameSiteStrictMode, access.SameSite)

	require.NotNil(t, magic, "the spent single-use cookie must be cleared")
	assert.Equal(t, -1, magic.MaxAge)
}

func TestVerifyMagicLink_QueryParamIsNotAccepted(t *testing.T) {
	mux, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/auth/verify?token=tok-123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerifyMagicLink_Unauthorized(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().VerifyMagicLink(gomock.Any(), gomock.Any()).Return(dto.VerifyMagicLinkResponse{},
		apperror.ErrUnauthorized("bad").WithCode(apperror.CodeTokenInvalid))

	req := httptest.NewRequest(http.MethodGet, "/auth/verify", nil)
	req.AddCookie(&http.Cookie{Name: "magic_token", Value: "spent"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, apperror.CodeTokenInvalid, metaOf(t, rec)["error_code"])
}

// The token is a QUERY PARAMETER (§1.7), because this URL is clicked out of an
// email client: there is no cookie on that request, and nothing in the
// registration flow ever set one. This handler used to read `magic_token`,
// which meant every verification email's link answered 400.
func TestVerifyEmail_Success(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().VerifyEmail(gomock.Any(), "tok-xyz").Return(nil)

	rec := serve(mux, httptestNewGet("/auth/verify-email?token=tok-xyz"))
	require.Equal(t, http.StatusOK, rec.Code)
	// data {email_verified: true}, not a bare {}: the client reads a value
	// instead of inferring success from the status code.
	data := decode(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["email_verified"])
	assert.Equal(t, "Email verified", metaOf(t, rec)["message"])
}

func TestVerifyEmail_RequiresTheTokenQueryParameter(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := serve(mux, httptestNewGet("/auth/verify-email"))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, apperror.CodeTokenInvalid, metaOf(t, rec)["error_code"])
}

// A cookie is NOT accepted in its place. The two verify endpoints read
// deliberately different channels, and honouring both here would quietly
// re-create the path that could never work while hiding that it never did.
func TestVerifyEmail_IgnoresTheMagicTokenCookie(t *testing.T) {
	mux, _ := newTestServer(t)
	req := httptestNewGet("/auth/verify-email")
	req.AddCookie(&http.Cookie{Name: "magic_token", Value: "tok-xyz"})
	rec := serve(mux, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// An empty token is the same as none: ?token= is what a mail client produces
// from a template whose variable did not render.
func TestVerifyEmail_EmptyTokenIsRefused(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := serve(mux, httptestNewGet("/auth/verify-email?token="))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ---- register ----

func TestRegister_Success(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().Register(gomock.Any(), gomock.Any()).Return(dto.RegisterResponse{UserID: "USR-456"}, nil)

	rec := postJSON(t, mux, "/auth/register",
		`{"email":"a@example.com","nik":"3171000000000022","consent":{"accepted":true,"policy_version":"2026-10-01"}}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	data := decode(t, rec)["data"].(map[string]any)
	assert.Equal(t, "USR-456", data["user_id"])
}

func TestRegister_MissingConsentReaches422NotA400(t *testing.T) {
	// A missing consent object and an unchecked box are different answers to
	// the customer, and the contract specifies CONSENT_REQUIRED (422) for the
	// second. The handler must not short-circuit either into a 400.
	mux, uc := newTestServer(t)
	uc.EXPECT().Register(gomock.Any(), gomock.Any()).Return(dto.RegisterResponse{},
		apperror.ErrUnprocessable("consent required").WithCode(apperror.CodeConsentRequired))

	rec := postJSON(t, mux, "/auth/register", `{"email":"a@example.com","nik":"3171000000000022"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, apperror.CodeConsentRequired, metaOf(t, rec)["error_code"])
}

func TestRegister_DuplicateNIK(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().Register(gomock.Any(), gomock.Any()).Return(dto.RegisterResponse{},
		apperror.ErrConflict("taken").WithCode(apperror.CodeNIKAlreadyRegistered))

	rec := postJSON(t, mux, "/auth/register",
		`{"email":"a@example.com","nik":"3171000000000022","consent":{"accepted":true,"policy_version":"v1"}}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, apperror.CodeNIKAlreadyRegistered, metaOf(t, rec)["error_code"])
}

func TestRegister_MalformedBody(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := postJSON(t, mux, "/auth/register", `{broken`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ---- refresh ----

func TestRefreshSession_Success(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().RefreshSession(gomock.Any(), "old-refresh").Return(dto.RefreshSessionResponse{
		AccessToken: "jwt-new", RefreshToken: "refresh-new", ExpiresIn: 22740,
	}, nil)

	rec := postJSON(t, mux, "/auth/refresh", `{"refresh_token":"old-refresh"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	data := decode(t, rec)["data"].(map[string]any)
	assert.Equal(t, "jwt-new", data["access_token"])
	assert.Equal(t, "refresh-new", data["refresh_token"])

	var access *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "access_token" {
			access = c
		}
	}
	require.NotNil(t, access)
	assert.Equal(t, "jwt-new", access.Value)
	assert.True(t, access.HttpOnly)
}

func TestRefreshSession_FailureClearsTheCookie(t *testing.T) {
	// A browser must stop presenting a token the server has written off.
	mux, uc := newTestServer(t)
	uc.EXPECT().RefreshSession(gomock.Any(), gomock.Any()).Return(dto.RefreshSessionResponse{},
		apperror.ErrUnauthorized("session ended").WithCode(apperror.CodeTokenInvalid))

	rec := postJSON(t, mux, "/auth/refresh", `{"refresh_token":"stolen"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, apperror.CodeTokenInvalid, metaOf(t, rec)["error_code"])

	var cleared *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "access_token" {
			cleared = c
		}
	}
	require.NotNil(t, cleared)
	assert.Equal(t, -1, cleared.MaxAge)
}

func TestRefreshSession_MissingToken(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := postJSON(t, mux, "/auth/refresh", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRefreshSession_MalformedBody(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := postJSON(t, mux, "/auth/refresh", `{broken`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerifyMagicLink_PassesRedirectContextThrough(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().VerifyMagicLink(gomock.Any(), gomock.Any()).Return(dto.VerifyMagicLinkResponse{
		AccessToken: "jwt", RefreshToken: "refresh", ExpiresIn: 22740,
		RedirectContext: &dto.RedirectContext{
			Type: "booking", EventID: "evt_konser_a", BookingID: "RB-00912",
			Message: "Booking context restored",
		},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/verify", nil)
	req.AddCookie(&http.Cookie{Name: "magic_token", Value: "tok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	data := decode(t, rec)["data"].(map[string]any)
	redirect := data["redirect_context"].(map[string]any)
	assert.Equal(t, "booking", redirect["type"])
	assert.Equal(t, "RB-00912", redirect["booking_id"])
}

func TestVerifyMagicLink_OmitsRedirectContextWhenAbsent(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().VerifyMagicLink(gomock.Any(), gomock.Any()).Return(dto.VerifyMagicLinkResponse{
		AccessToken: "jwt", RefreshToken: "refresh", ExpiresIn: 22740,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/verify", nil)
	req.AddCookie(&http.Cookie{Name: "magic_token", Value: "tok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	data := decode(t, rec)["data"].(map[string]any)
	_, present := data["redirect_context"]
	assert.False(t, present, "omitempty keeps it out of the payload entirely")
}
