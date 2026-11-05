package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	mock_inbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/_mock/nik/port/inbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/dto"
)

func newTestServer(t *testing.T) (*http.ServeMux, *mock_inbound.MockINikUsecase) {
	t.Helper()
	uc := mock_inbound.NewMockINikUsecase(gomock.NewController(t))
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewNik(uc))
	return mux, uc
}

func do(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/nik/validate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestValidate_Success(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().Validate(gomock.Any(), dto.ValidateRequest{NIK: "3171000000000022"}).Return(dto.ValidateResponse{
		NIKMasked: "3171**********22",
		NIKHmac:   "abc123",
		Valid:     true,
		Status:    "ACTIVE",
		Source:    "verified",
	}, nil)

	rec := do(t, mux, `{"nik":"3171000000000022"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	body := decode(t, rec)
	data := body["data"].(map[string]any)
	assert.Equal(t, true, data["valid"])
	assert.Equal(t, "abc123", data["nik_hmac"])
	assert.Equal(t, "3171**********22", data["nik_masked"])
}

func TestValidate_RejectedNIKIsStill200(t *testing.T) {
	// The call succeeded; the answer was no. Non-2xx is reserved for "no answer
	// obtainable", which is the only thing callers must fail closed on.
	mux, uc := newTestServer(t)
	uc.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(dto.ValidateResponse{
		Valid: false, Status: "NOT_FOUND", Source: "verified",
	}, nil)

	rec := do(t, mux, `{"nik":"3171000000000000"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	data := decode(t, rec)["data"].(map[string]any)
	assert.Equal(t, false, data["valid"])
}

func TestValidate_DukcapilUnavailableCarriesErrorCode(t *testing.T) {
	mux, uc := newTestServer(t)
	uc.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(dto.ValidateResponse{},
		apperror.ErrServiceUnavailable("down").WithCode(apperror.CodeDukcapilUnavailable))

	rec := do(t, mux, `{"nik":"3171000000000022"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	meta := decode(t, rec)["meta"].(map[string]any)
	assert.Equal(t, apperror.CodeDukcapilUnavailable, meta["error_code"])
}

func TestValidate_MalformedBody(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := do(t, mux, `{not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	meta := decode(t, rec)["meta"].(map[string]any)
	assert.Equal(t, apperror.CodeValidationError, meta["error_code"])
}

func TestValidate_MissingNIK(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := do(t, mux, `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWriteError_UnknownErrorIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, assert.AnError)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestValidate_WrongMethodIsRejectedByTheMux(t *testing.T) {
	mux, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/nik/validate", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
