package dukcapil

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

func serveDukcapil(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotEmpty(t, r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
}

// ── extra headers ──────────────────────────────────────────────────────────

func TestVerifyNIK_SendsExtraHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(verifyResponse{Valid: true, Status: "ACTIVE"})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "secret-key", 5*time.Second, map[string]string{
		"apidogToken": "mock-token",
		"X-Trace-Id":  "abc",
	})
	_, err := client.VerifyNIK(context.Background(), "1234567890123456")
	require.NoError(t, err)

	assert.Equal(t, "mock-token", got.Get("apidogToken"))
	assert.Equal(t, "abc", got.Get("X-Trace-Id"))
	// Still present: extras add, they do not replace.
	assert.Equal(t, "secret-key", got.Get("X-Api-Key"))
	assert.Equal(t, "application/json", got.Get("Content-Type"))
}

func TestVerifyNIK_ExtraHeadersCannotOverrideTheContract(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(verifyResponse{Valid: true})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "real-key", 5*time.Second, map[string]string{
		"X-Api-Key":    "hijacked",
		"Content-Type": "text/plain",
	})
	_, err := client.VerifyNIK(context.Background(), "1234567890123456")
	require.NoError(t, err)

	assert.Equal(t, "real-key", got.Get("X-Api-Key"))
	assert.Equal(t, "application/json", got.Get("Content-Type"))
}

// ── VerifyNIK ─────────────────────────────────────────────────────────────────

func TestVerifyNIK_Valid(t *testing.T) {
	srv := serveDukcapil(t, http.StatusOK, verifyResponse{Valid: true, Status: "ACTIVE", Ref: "ref-1"})
	defer srv.Close()

	client := NewClient(srv.URL, "secret-key", 5*time.Second, nil)
	result, err := client.VerifyNIK(context.Background(), "1234567890123456")
	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Equal(t, "ACTIVE", result.Status)
	assert.Equal(t, "ref-1", result.Ref)
}

func TestVerifyNIK_InvalidNIK(t *testing.T) {
	srv := serveDukcapil(t, http.StatusOK, verifyResponse{Valid: false, Status: "INACTIVE"})
	defer srv.Close()

	client := NewClient(srv.URL, "key", 5*time.Second, nil)
	result, err := client.VerifyNIK(context.Background(), "0000000000000000")
	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.Equal(t, "INACTIVE", result.Status)
}

func TestVerifyNIK_NotFound_Returns404(t *testing.T) {
	srv := serveDukcapil(t, http.StatusNotFound, nil)
	defer srv.Close()

	client := NewClient(srv.URL, "key", 5*time.Second, nil)
	result, err := client.VerifyNIK(context.Background(), "bad")
	require.NoError(t, err) // 404 → result with Valid=false, not an error
	assert.False(t, result.Valid)
	assert.Equal(t, "NOT_FOUND", result.Status)
}

func TestVerifyNIK_ServerError(t *testing.T) {
	srv := serveDukcapil(t, http.StatusInternalServerError, nil)
	defer srv.Close()

	client := NewClient(srv.URL, "key", 5*time.Second, nil)
	_, err := client.VerifyNIK(context.Background(), "nik")
	assert.Error(t, err)
}

func TestVerifyNIK_TooManyRequests(t *testing.T) {
	srv := serveDukcapil(t, http.StatusTooManyRequests, nil)
	defer srv.Close()

	client := NewClient(srv.URL, "key", 5*time.Second, nil)
	_, err := client.VerifyNIK(context.Background(), "nik")
	assert.Error(t, err)
}

func TestVerifyNIK_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "key", 5*time.Second, nil)
	_, err := client.VerifyNIK(context.Background(), "nik")
	assert.Error(t, err)
}

func TestVerifyNIK_DefaultStatus_WhenEmpty(t *testing.T) {
	// status field empty → should default to "ACTIVE"
	srv := serveDukcapil(t, http.StatusOK, verifyResponse{Valid: true, Status: ""})
	defer srv.Close()

	client := NewClient(srv.URL, "key", 5*time.Second, nil)
	result, err := client.VerifyNIK(context.Background(), "nik")
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", result.Status)
}

func TestVerifyNIK_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond) // handler finishes eventually; client times out first
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "key", 10*time.Millisecond, nil)
	_, err := client.VerifyNIK(context.Background(), "nik")
	assert.Error(t, err)
}
