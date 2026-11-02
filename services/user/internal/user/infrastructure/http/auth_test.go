package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	mock_inbound "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/_mock/user/port/inbound"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
)

func newTestController(t *testing.T) (*UserController, *mock_inbound.MockIUserUsecase) {
	t.Helper()
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIUserUsecase(ctrl)
	return NewUser(uc).(*UserController), uc
}

// ---- RequestMagicLink ----

func TestRequestMagicLink_success(t *testing.T) {
	c, uc := newTestController(t)
	uc.EXPECT().RequestMagicLink(gomock.Any(), gomock.Any()).Return(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(`{"email":"user@example.com"}`))
	c.RequestMagicLink(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequestMagicLink_invalidBody(t *testing.T) {
	c, _ := newTestController(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader("{invalid"))
	c.RequestMagicLink(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRequestMagicLink_validationError(t *testing.T) {
	c, _ := newTestController(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(`{"email":"not-an-email"}`))
	c.RequestMagicLink(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ---- VerifyMagicLink ----

func TestVerifyMagicLink_success(t *testing.T) {
	c, uc := newTestController(t)
	uc.EXPECT().VerifyMagicLink(gomock.Any(), "magic-token-123").Return(dto.VerifyMagicLinkResponse{
		AccessToken: "at_abc",
		ExpiresIn:   300,
		User:        dto.UserSummary{ID: "USR-001", Email: "user@example.com"},
	}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/verify?token=magic-token-123", nil)
	c.VerifyMagicLink(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "at_abc") {
		t.Errorf("body missing access token: %s", rec.Body.String())
	}
}

func TestVerifyMagicLink_missingToken(t *testing.T) {
	c, _ := newTestController(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/verify", nil)
	c.VerifyMagicLink(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestVerifyMagicLink_invalidToken(t *testing.T) {
	c, uc := newTestController(t)
	uc.EXPECT().VerifyMagicLink(gomock.Any(), "bad").Return(dto.VerifyMagicLinkResponse{}, apperror.ErrUnauthorized("invalid"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/verify?token=bad", nil)
	c.VerifyMagicLink(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// ---- Register ----

func TestRegister_success(t *testing.T) {
	c, uc := newTestController(t)
	uc.EXPECT().Register(gomock.Any(), gomock.Any()).Return(dto.RegisterResponse{UserID: "USR-001"}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{
		"email":"alex@example.com",
		"nik":"3171000000000001",
		"consent":{"accepted":true,"policy_version":"2026-10-01"}
	}`))
	c.Register(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRegister_validationError(t *testing.T) {
	c, _ := newTestController(t)

	rec := httptest.NewRecorder()
	// missing NIK
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"alex@example.com"}`))
	c.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRegister_consentRequired(t *testing.T) {
	c, uc := newTestController(t)
	uc.EXPECT().Register(gomock.Any(), gomock.Any()).Return(dto.RegisterResponse{}, apperror.ErrUnprocessable("CONSENT_REQUIRED"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{
		"email":"alex@example.com",
		"nik":"3171000000000001",
		"consent":{"accepted":false,"policy_version":"2026-10-01"}
	}`))
	c.Register(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestRegister_internalError(t *testing.T) {
	c, uc := newTestController(t)
	uc.EXPECT().Register(gomock.Any(), gomock.Any()).Return(dto.RegisterResponse{}, errors.New("db down"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{
		"email":"alex@example.com",
		"nik":"3171000000000001",
		"consent":{"accepted":true,"policy_version":"2026-10-01"}
	}`))
	c.Register(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// ---- ResendMagicLink ----

func TestResendMagicLink_success(t *testing.T) {
	c, uc := newTestController(t)
	uc.EXPECT().RequestMagicLink(gomock.Any(), gomock.Any()).Return(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link/resend", strings.NewReader(`{"email":"user@example.com"}`))
	c.ResendMagicLink(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "New login link sent") {
		t.Errorf("body missing expected message: %s", rec.Body.String())
	}
}

func TestResendMagicLink_invalidBody(t *testing.T) {
	c, _ := newTestController(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link/resend", strings.NewReader("{bad"))
	c.ResendMagicLink(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestResendMagicLink_validationError(t *testing.T) {
	c, _ := newTestController(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link/resend", strings.NewReader(`{"email":"not-valid"}`))
	c.ResendMagicLink(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ---- RegisterRoutes smoke test ----

func TestRegisterRoutes_registersAllPaths(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIUserUsecase(ctrl)
	handler := NewUser(uc)
	mux := http.NewServeMux()
	RegisterRoutes(mux, handler)

	// Ensure at least one well-known route resolves without panicking.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(`{}`))
	// The handler will fail validation (no email), but we just want to confirm
	// the route is registered and the mux dispatches to it.
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Error("route POST /auth/magic-link was not registered")
	}
}
