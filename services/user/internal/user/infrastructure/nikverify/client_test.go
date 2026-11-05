package nikverify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

type fakeResp struct {
	Meta struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		ErrorCode string `json:"error_code"`
	} `json:"meta"`
	Data struct {
		NIKMasked string `json:"nik_masked"`
		NIKHmac   string `json:"nik_hmac"`
		Valid     bool   `json:"valid"`
		Status    string `json:"status"`
		Source    string `json:"source"`
	} `json:"data"`
}

func serveJSON(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// ── tests ──────────────────────────────────────────────────────────────────────

func TestVerifyNIK_ValidResponse(t *testing.T) {
	resp := fakeResp{}
	resp.Data.Valid = true
	resp.Data.NIKHmac = "abc123"
	resp.Data.NIKMasked = "****1234"
	resp.Data.Status = "ACTIVE"
	srv := serveJSON(t, http.StatusOK, resp)
	defer srv.Close()

	client := NewClient(srv.URL, 5*time.Second)
	result, err := client.VerifyNIK(context.Background(), "1234567890123456")
	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Equal(t, "abc123", result.NIKHmac)
	assert.Equal(t, "****1234", result.NIKMasked)
}

func TestVerifyNIK_InvalidNIK(t *testing.T) {
	resp := fakeResp{}
	resp.Data.Valid = false
	resp.Data.Status = "NOT_FOUND"
	srv := serveJSON(t, http.StatusOK, resp)
	defer srv.Close()

	client := NewClient(srv.URL, 5*time.Second)
	result, err := client.VerifyNIK(context.Background(), "0000000000000000")
	require.NoError(t, err)
	assert.False(t, result.Valid)
}

func TestVerifyNIK_ServerError(t *testing.T) {
	srv := serveJSON(t, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
	defer srv.Close()

	client := NewClient(srv.URL, 5*time.Second)
	_, err := client.VerifyNIK(context.Background(), "1234567890123456")
	assert.Error(t, err)
}

func TestVerifyNIK_ValidWithNoHmac_ReturnsError(t *testing.T) {
	resp := fakeResp{}
	resp.Data.Valid = true
	resp.Data.NIKHmac = "" // missing hash
	srv := serveJSON(t, http.StatusOK, resp)
	defer srv.Close()

	client := NewClient(srv.URL, 5*time.Second)
	_, err := client.VerifyNIK(context.Background(), "1234567890123456")
	assert.Error(t, err)
}

func TestVerifyNIK_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, 5*time.Second)
	_, err := client.VerifyNIK(context.Background(), "1234567890123456")
	assert.Error(t, err)
}

func TestVerifyNIK_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond) // handler finishes eventually; client times out first
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, 10*time.Millisecond)
	_, err := client.VerifyNIK(context.Background(), "1234567890123456")
	assert.Error(t, err)
}
