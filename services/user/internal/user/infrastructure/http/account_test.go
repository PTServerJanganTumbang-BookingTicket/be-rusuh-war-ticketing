package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	mock_inbound "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/_mock/user/port/inbound"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
)

const validToken = "valid-bearer-token"
const testAccID = "USR-test-001"

// expectAuth sets up the ValidateAccessToken mock used by protected endpoints.
func expectAuth(uc *mock_inbound.MockIUserUsecase) {
	uc.EXPECT().
		ValidateAccessToken(gomock.Any(), validToken).
		Return(testAccID, nil)
}

func authHeader(r *http.Request) *http.Request {
	r.Header.Set("Authorization", "Bearer "+validToken)
	return r
}

// ---- GetSavedNIKs ----

func TestGetSavedNIKs_success(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)
	uc.EXPECT().GetSavedNIKs(gomock.Any(), testAccID).Return(dto.SavedNIKsResponse{
		MaxEntries: 5,
		SavedNIKs: []dto.SavedNIKItem{
			{ID: "SNK-1", NIKMasked: "3171**********22", Label: "Budi", VerifiedAt: time.Now().Format(time.RFC3339), Status: "VERIFIED"},
		},
	}, nil)

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodGet, "/accounts/me/saved-niks", nil))
	c.GetSavedNIKs(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "SNK-1") {
		t.Errorf("body missing NIK id: %s", rec.Body.String())
	}
}

func TestGetSavedNIKs_unauthorized(t *testing.T) {
	c, _ := newTestController(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/accounts/me/saved-niks", nil) // no auth header
	c.GetSavedNIKs(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// ---- AddSavedNIK ----

func TestAddSavedNIK_success(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)
	uc.EXPECT().AddSavedNIK(gomock.Any(), testAccID, gomock.Any()).Return(dto.SavedNIKItem{
		ID: "SNK-new", Label: "Budi", Status: "VERIFIED",
	}, nil)

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodPost, "/accounts/me/saved-niks",
		strings.NewReader(`{"nik":"3171000000000033","label":"Budi"}`)))
	c.AddSavedNIK(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAddSavedNIK_maxReached(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)
	uc.EXPECT().AddSavedNIK(gomock.Any(), testAccID, gomock.Any()).Return(dto.SavedNIKItem{}, apperror.ErrConflict("MAX_SAVED_NIKS_REACHED"))

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodPost, "/accounts/me/saved-niks",
		strings.NewReader(`{"nik":"3171000000000099","label":"Extra"}`)))
	c.AddSavedNIK(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestAddSavedNIK_validationError(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)

	rec := httptest.NewRecorder()
	// missing label
	req := authHeader(httptest.NewRequest(http.MethodPost, "/accounts/me/saved-niks",
		strings.NewReader(`{"nik":"3171000000000033"}`)))
	c.AddSavedNIK(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ---- DeleteSavedNIK ----

func TestDeleteSavedNIK_success(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)
	uc.EXPECT().DeleteSavedNIK(gomock.Any(), testAccID, "SNK-1").Return(nil)

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodDelete, "/accounts/me/saved-niks/SNK-1", nil))
	req.SetPathValue("id", "SNK-1")
	c.DeleteSavedNIK(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestDeleteSavedNIK_notFound(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)
	uc.EXPECT().DeleteSavedNIK(gomock.Any(), testAccID, "SNK-missing").Return(apperror.ErrNotFound("not found"))

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodDelete, "/accounts/me/saved-niks/SNK-missing", nil))
	req.SetPathValue("id", "SNK-missing")
	c.DeleteSavedNIK(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
