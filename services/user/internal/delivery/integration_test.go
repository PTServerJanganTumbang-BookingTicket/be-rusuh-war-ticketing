package delivery

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/config"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		App: config.App{
			Env:        "local",
			Port:       8080,
			NIKHMACKey: "test-hmac-key",
		},
		Log: config.Log{Level: "error", Format: "json"},
	}
	return httptest.NewServer(New(cfg))
}

func TestHealthCheck(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/sys/ping")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRequestMagicLink_integration(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := strings.NewReader(`{"email":"integration@example.com"}`)
	resp, err := http.Post(srv.URL+"/auth/magic-link", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRegisterAndVerify_integration(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Register a new account
	regBody := `{"email":"newuser@example.com","nik":"3171000000000001","consent":{"accepted":true,"policy_version":"2026-10-01"}}`
	resp, err := http.Post(srv.URL+"/auth/register", "application/json", strings.NewReader(regBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGetSavedNIKs_unauthenticated_integration(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/accounts/me/saved-niks", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestGetMyTickets_unauthenticated_integration(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/users/me/tickets", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
