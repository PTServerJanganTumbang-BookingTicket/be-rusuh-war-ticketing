package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/_mock/user/port/inbound"
)

const bearer = "Bearer good-token"

// newMockUsecase builds the inbound-port mock on its own, for tests that need
// a controller with non-default Options.
func newMockUsecase(t *testing.T) *mock_inbound.MockIUserUsecase {
	t.Helper()
	return mock_inbound.NewMockIUserUsecase(gomock.NewController(t))
}

func newTestServer(t *testing.T) (*http.ServeMux, *mock_inbound.MockIUserUsecase) {
	t.Helper()
	uc := newMockUsecase(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewUser(uc, Options{SecureCookies: true, CookieDomain: ""}))
	return mux, uc
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func metaOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	return decode(t, rec)["meta"].(map[string]any)
}

// expectAuth makes the usecase accept the standard bearer token.
func expectAuth(uc *mock_inbound.MockIUserUsecase, accountID string) {
	uc.EXPECT().ValidateAccessToken(gomock.Any(), "good-token").Return(accountID, nil).AnyTimes()
}

func TestRequireAuth_MissingHeader(t *testing.T) {
	mux, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/users/me/tickets", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_NonBearerScheme(t *testing.T) {
	mux, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/accounts/me/saved-niks", nil)
	req.Header.Set("Authorization", "Basic abc123")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// httptestNewGet and serve keep the unauthenticated cases short.
func httptestNewGet(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, nil)
}

func serve(mux *http.ServeMux, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestWriteError_UnknownErrorIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, assert.AnError)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
