package apinizer

import (
	"encoding/json"
	"net/http"
)

// RegisterHealthCheck registers a liveness endpoint on the mux.
func RegisterHealthCheck(mux *http.ServeMux, healthPath string) {
	if healthPath == "" {
		healthPath = "/healthz"
	}
	mux.HandleFunc("GET "+healthPath, healthz)
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	WriteEnvelope(w, http.StatusOK, "ok", nil)
}

// WriteEnvelope writes the consistent {meta:{code,message}, data} response
// envelope used across all endpoints.
func WriteEnvelope(w http.ResponseWriter, status int, message string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"meta": map[string]any{"code": status, "message": message},
		"data": data,
	})
}
