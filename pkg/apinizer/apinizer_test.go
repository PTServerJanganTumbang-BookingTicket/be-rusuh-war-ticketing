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
	assert.Equal(t, map[string]any{}, body["data"], "contract: data is {} when empty, never null")
	assert.NotEmpty(t, meta["server_time"], "contract: server_time on every response (BR-10)")
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

func TestNewPagination(t *testing.T) {
	cases := []struct {
		name                 string
		total, limit, offset int
		wantTotalPage        int
	}{
		{"exact multiple", 20, 10, 0, 2},
		{"partial last page", 21, 10, 20, 3},
		{"single short page", 3, 10, 0, 1},
		{"empty result", 0, 10, 0, 1},
		{"zero limit does not divide by zero", 5, 0, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPagination(tc.total, tc.limit, tc.offset)
			assert.Equal(t, tc.wantTotalPage, p.TotalPage)
			assert.Equal(t, tc.total, p.TotalData)
			assert.Equal(t, tc.limit, p.Limit)
			assert.Equal(t, tc.offset, p.Offset)
		})
	}
}

func TestWriteList_EmitsPagination(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteList(rec, http.StatusOK, "Success", []map[string]string{{"id": "TKT-001"}}, NewPagination(1, 10, 0))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	meta := body["meta"].(map[string]any)
	page := meta["pagination"].(map[string]any)
	assert.Equal(t, float64(1), page["totalData"])
	assert.Equal(t, float64(1), page["totalPage"])
	assert.Equal(t, float64(10), page["limit"])
	assert.Equal(t, float64(0), page["offset"])
	assert.Len(t, body["data"], 1)
}

func TestWriteEnvelope_OmitsPaginationAndErrorCode(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteEnvelope(rec, http.StatusOK, "Success", nil)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	meta := body["meta"].(map[string]any)
	_, hasPage := meta["pagination"]
	_, hasCode := meta["error_code"]
	assert.False(t, hasPage)
	assert.False(t, hasCode)
}

func TestWriteErrorEnvelope_EmitsErrorCode(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteErrorEnvelope(rec, http.StatusConflict, "NIK_ALREADY_REGISTERED", "NIK is bound to another account", nil)

	assert.Equal(t, http.StatusConflict, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	meta := body["meta"].(map[string]any)
	assert.Equal(t, "NIK_ALREADY_REGISTERED", meta["error_code"])
	assert.Equal(t, "NIK is bound to another account", meta["message"])
}
