package apinizer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterHealthCheck_CustomPath(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealthCheck(mux, "/sys/ping")

	req := httptest.NewRequest(http.MethodGet, "/sys/ping", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	meta := body["meta"].(map[string]any)
	assert.Equal(t, float64(http.StatusOK), meta["code"])
	assert.Equal(t, "ok", meta["message"])
	assert.Nil(t, body["data"])
}

func TestRegisterHealthCheck_DefaultPath(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealthCheck(mux, "")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestWriteEnvelope_WithData(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteEnvelope(rec, http.StatusCreated, "created", map[string]string{"id": "abc"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	meta := body["meta"].(map[string]any)
	assert.Equal(t, float64(http.StatusCreated), meta["code"])
	assert.Equal(t, "created", meta["message"])
	data := body["data"].(map[string]any)
	assert.Equal(t, "abc", data["id"])
}
