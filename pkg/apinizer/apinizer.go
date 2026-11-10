package apinizer

import (
	"encoding/json"
	"net/http"
	"time"
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

// Pagination is the offset-based page descriptor carried in meta.pagination on
// list endpoints. Field names match the API contract exactly (camelCase for the
// two totals, lowercase for limit/offset) — do not "tidy" them.
type Pagination struct {
	TotalData int `json:"totalData"`
	TotalPage int `json:"totalPage"`
	Limit     int `json:"limit"`
	Offset    int `json:"offset"`
}

// NewPagination builds a Pagination from a total row count and the effective
// window, deriving totalPage. A zero limit yields a single page, so callers that
// forgot to clamp still emit a coherent descriptor instead of dividing by zero.
func NewPagination(totalData, limit, offset int) Pagination {
	totalPage := 1
	if limit > 0 {
		totalPage = totalData / limit
		if totalData%limit != 0 {
			totalPage++
		}
		if totalPage == 0 {
			totalPage = 1
		}
	}
	return Pagination{TotalData: totalData, TotalPage: totalPage, Limit: limit, Offset: offset}
}

// WriteEnvelope writes the consistent {meta:{code,message}, data} response
// envelope used across all endpoints.
func WriteEnvelope(w http.ResponseWriter, status int, message string, data any) {
	write(w, status, meta(status, message, "", nil), data)
}

// WriteList writes the envelope with meta.pagination alongside the data array.
// List endpoints must use this rather than WriteEnvelope: clients page on
// meta.pagination and have no other source for the row count.
func WriteList(w http.ResponseWriter, status int, message string, data any, p Pagination) {
	write(w, status, meta(status, message, "", &p), data)
}

// WriteErrorEnvelope writes the envelope with a machine-readable code in
// meta.error_code, so clients switch on the code rather than string-matching
// meta.message. Codes are the identifiers in the API contract's Appendix A
// (VALIDATION_ERROR, NIK_ALREADY_REGISTERED, DUKCAPIL_UNAVAILABLE, …).
func WriteErrorEnvelope(w http.ResponseWriter, status int, errorCode, message string, data any) {
	write(w, status, meta(status, message, errorCode, nil), data)
}

func meta(status int, message, errorCode string, p *Pagination) map[string]any {
	// server_time is on EVERY response, success or error, and it is filled
	// here rather than by each handler (API contract §7).
	//
	// It exists so a client can synchronise its clock from any reply it is
	// already making (BR-10) - which is why there is no GET /time: an extra
	// round trip to learn the time is a round trip the countdown cannot
	// afford, and a dedicated endpoint would be called least often exactly
	// when drift matters most.
	//
	// Distinct from a server_time INSIDE data: several payment and queue
	// payloads carry their own, scoped to the countdown that response is
	// about. This one is about the response itself.
	m := map[string]any{
		"code":        status,
		"message":     message,
		"server_time": time.Now().UTC().Format(time.RFC3339),
	}
	if errorCode != "" {
		m["error_code"] = errorCode
	}
	if p != nil {
		m["pagination"] = p
	}
	return m
}

func write(w http.ResponseWriter, status int, m map[string]any, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"meta": m, "data": data})
}
