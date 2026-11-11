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

func authed(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", bearer)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// ── GET /accounts/me (contract §1.8) ─────────────────────────────────────────

func TestGetMyAccount_ReturnsEmailAndMaskedNIK(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetMyAccount(gomock.Any(), "USR-001").
		Return(dto.AccountProfileResponse{Email: "alex@example.com", NIKMasked: "3171********xx22"}, nil)

	rec := authed(t, mux, http.MethodGet, "/accounts/me", "")
	require.Equal(t, http.StatusOK, rec.Code)

	data := decode(t, rec)["data"].(map[string]any)
	assert.Equal(t, "alex@example.com", data["email"])
	assert.Equal(t, "3171********xx22", data["nik_masked"])
	assert.Len(t, data, 2)

	// The account stores no name, so there is none to return (FR-15a, §1.8).
	assert.NotContains(t, data, "name")
	// And no field carries the sixteen digits (FR-32c).
	assert.NotContains(t, rec.Body.String(), "3171000000000022")

	meta := metaOf(t, rec)
	assert.Equal(t, float64(200), meta["code"])
	assert.Equal(t, "Success", meta["message"])
	assert.NotEmpty(t, meta["server_time"], "every response carries the server clock (BR-10)")
}

// The route exists. Registering the handler and forgetting the route is the
// failure this catches, and it looks exactly like a 404 from the gateway.
func TestGetMyAccount_IsRoutedAndDistinctFromSavedNIKs(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetMyAccount(gomock.Any(), "USR-001").Return(dto.AccountProfileResponse{Email: "a@example.com"}, nil)
	uc.EXPECT().GetSavedNIKs(gomock.Any(), "USR-001").Return(dto.SavedNIKsResponse{MaxEntries: 5}, nil)

	assert.Equal(t, http.StatusOK, authed(t, mux, http.MethodGet, "/accounts/me", "").Code)
	// The sibling path still reaches its own handler: neither pattern shadows
	// the other.
	assert.Equal(t, http.StatusOK, authed(t, mux, http.MethodGet, "/accounts/me/saved-niks", "").Code)
}

func TestGetMyAccount_WithoutABearerTokenIs401(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := serve(mux, httptestNewGet("/accounts/me"))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, apperror.CodeTokenInvalid, metaOf(t, rec)["error_code"])
}

// §1.8: a grant is not accepted here. It proves its bearer holds seats and
// carries no account identity, so there is nothing in it that could answer
// "whose profile" — and honouring one would hand a profile to any grant holder.
func TestGetMyAccount_AGrantTokenIsNotACredentialHere(t *testing.T) {
	mux, _ := newTestServer(t)
	req := httptestNewGet("/accounts/me")
	req.Header.Set("X-Grant-Token", "gt_live_grant")
	req.Header.Set("X-Device-Id", "d3f1a9c4b70e42f88a1c5e6072b9d4af")
	rec := serve(mux, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// The expiry distinction survives the handler: TOKEN_EXPIRED tells the client
// to refresh (§1.4), TOKEN_INVALID to log in again (§1.1). Collapsing them
// sends a customer with a merely stale token back to the login screen.
func TestGetMyAccount_PassesTheSessionErrorCodeThrough(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code string
	}{
		"expired": {apperror.ErrUnauthorized("access token has expired").WithCode(apperror.CodeTokenExpired), apperror.CodeTokenExpired},
		"invalid": {apperror.ErrUnauthorized("invalid access token").WithCode(apperror.CodeTokenInvalid), apperror.CodeTokenInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			mux, uc := newTestServer(t)
			uc.EXPECT().ValidateAccessToken(gomock.Any(), "good-token").Return("", tc.err)

			rec := authed(t, mux, http.MethodGet, "/accounts/me", "")
			require.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, tc.code, metaOf(t, rec)["error_code"])
		})
	}
}

func TestGetMyAccount_UsecaseFailureIsReportedWithoutDetail(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetMyAccount(gomock.Any(), "USR-001").
		Return(dto.AccountProfileResponse{}, apperror.ErrInternal("failed to read account"))

	rec := authed(t, mux, http.MethodGet, "/accounts/me", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "accounts")
}

// A read changes nothing, so it is not CSRF-guarded like the write handlers.
// A cross-origin GET that 403s here would break a profile page served from a
// second origin for no security gain.
func TestGetMyAccount_IsNotOriginCheckedBecauseItChangesNothing(t *testing.T) {
	uc := newMockUsecase(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewUser(uc, Options{SecureCookies: true, CookieDomain: "rusuh.id"}))
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetMyAccount(gomock.Any(), "USR-001").Return(dto.AccountProfileResponse{Email: "a@example.com"}, nil)

	req := httptestNewGet("/accounts/me")
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Origin", "https://evil.example.com")
	assert.Equal(t, http.StatusOK, serve(mux, req).Code)
}

// ── GET /accounts/me/saved-niks ──────────────────────────────────────────────

func TestGetSavedNIKs_ReturnsMaskedOnly(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetSavedNIKs(gomock.Any(), "USR-001").Return(dto.SavedNIKsResponse{
		MaxEntries: 5,
		SavedNIKs: []dto.SavedNIKItem{
			{ID: "SNK-1", NIKMasked: "3171**********33", Label: "Budi", VerifiedAt: "2026-10-20T08:11:00Z", Status: "VERIFIED"},
		},
	}, nil)

	rec := authed(t, mux, http.MethodGet, "/accounts/me/saved-niks", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "3171000000000033", "the full NIK is never returned (FR-32c)")

	data := decode(t, rec)["data"].(map[string]any)
	assert.Equal(t, float64(5), data["max_entries"])
}

func TestAddSavedNIK_Created(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().AddSavedNIK(gomock.Any(), "USR-001", dto.AddSavedNIKRequest{NIK: "3171000000000033", Label: "Budi"}).
		Return(dto.SavedNIKItem{ID: "SNK-1", NIKMasked: "3171**********33", Label: "Budi", Status: "VERIFIED"}, nil)

	rec := authed(t, mux, http.MethodPost, "/accounts/me/saved-niks", `{"nik":"3171000000000033","label":"Budi"}`)
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestAddSavedNIK_ConsentRequired(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().AddSavedNIK(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.SavedNIKItem{},
		apperror.ErrUnprocessable("consent required").WithCode(apperror.CodeConsentRequired))

	rec := authed(t, mux, http.MethodPost, "/accounts/me/saved-niks", `{"nik":"3171000000000033","label":"Budi"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, apperror.CodeConsentRequired, metaOf(t, rec)["error_code"])
}

func TestAddSavedNIK_MaxReached(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().AddSavedNIK(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.SavedNIKItem{},
		apperror.ErrConflict("full").WithCode(apperror.CodeMaxSavedNIKsReached))

	rec := authed(t, mux, http.MethodPost, "/accounts/me/saved-niks", `{"nik":"3171000000000033","label":"Budi"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, apperror.CodeMaxSavedNIKsReached, metaOf(t, rec)["error_code"])
}

func TestAddSavedNIK_DukcapilUnavailable(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().AddSavedNIK(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.SavedNIKItem{},
		apperror.ErrServiceUnavailable("down").WithCode(apperror.CodeDukcapilUnavailable))

	rec := authed(t, mux, http.MethodPost, "/accounts/me/saved-niks", `{"nik":"3171000000000033","label":"Budi"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAddSavedNIK_MissingLabel(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	rec := authed(t, mux, http.MethodPost, "/accounts/me/saved-niks", `{"nik":"3171000000000033"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAddSavedNIK_Unauthenticated(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := postJSON(t, mux, "/accounts/me/saved-niks", `{"nik":"3171000000000033","label":"Budi"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDeleteSavedNIK_NoContent(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().DeleteSavedNIK(gomock.Any(), "USR-001", "SNK-1").Return(nil)

	rec := authed(t, mux, http.MethodDelete, "/accounts/me/saved-niks/SNK-1", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestDeleteSavedNIK_NotFound(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().DeleteSavedNIK(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(apperror.ErrNotFound("saved NIK not found"))

	rec := authed(t, mux, http.MethodDelete, "/accounts/me/saved-niks/SNK-other", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRequireSameOrigin_RejectsForeignOrigin(t *testing.T) {
	uc := newMockUsecase(t)
	expectAuth(uc, "USR-001")
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewUser(uc, Options{SecureCookies: true, CookieDomain: "rusuh.id"}))

	req := httptest.NewRequest(http.MethodPost, "/accounts/me/saved-niks",
		strings.NewReader(`{"nik":"3171000000000033","label":"Budi"}`))
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireSameOrigin_AllowsAbsentOrigin(t *testing.T) {
	// Native clients and server-to-server callers send no Origin, and
	// rejecting those would break the shared web/mobile API (NFR-08).
	uc := newMockUsecase(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().AddSavedNIK(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(dto.SavedNIKItem{ID: "SNK-1"}, nil)
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewUser(uc, Options{SecureCookies: true, CookieDomain: "rusuh.id"}))

	rec := authed(t, mux, http.MethodPost, "/accounts/me/saved-niks", `{"nik":"3171000000000033","label":"Budi"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
}
