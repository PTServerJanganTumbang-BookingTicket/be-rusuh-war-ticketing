package apinizer

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
)

// ErrorCodeInternal is meta.error_code for any 5xx written without a code.
const ErrorCodeInternal = "INTERNAL_ERROR"

// internalMessage is the only text a client sees for an unexpected error.
const internalMessage = "internal server error"

var clock atomic.Pointer[func() time.Time]

// SetClock replaces the clock behind meta.server_time and returns a function
// that restores the previous one. For tests.
func SetClock(now func() time.Time) (restore func()) {
	prev := clock.Swap(&now)
	return func() { clock.Store(prev) }
}

func serverTime() string {
	now := time.Now
	if f := clock.Load(); f != nil {
		now = *f
	}
	// Millisecond precision, trailing zeros trimmed: 09:12:00.412Z, 02:00:05Z.
	return now().UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
}

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

// WriteEnvelope writes the contract envelope used across all endpoints:
// {meta:{code,message,server_time}, data}. Nil data is written as {}.
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

// WriteError writes err as an error envelope. An *apperror.AppError keeps its
// status, code, message and data (and Retry-After); any other error becomes a
// 500 INTERNAL_ERROR whose text never reaches the client.
func WriteError(w http.ResponseWriter, err error) {
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		WriteErrorEnvelope(w, http.StatusInternalServerError, ErrorCodeInternal, internalMessage, nil)
		return
	}
	if appErr.RetryAfter > 0 {
		seconds := max(int(appErr.RetryAfter.Seconds()), 1)
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	WriteErrorEnvelope(w, appErr.HTTPStatus, appErr.ErrorCode, appErr.Message, appErr.Data)
}

func meta(status int, message, errorCode string, p *Pagination) map[string]any {
	m := map[string]any{"code": status, "message": message, "server_time": serverTime()}
	if errorCode == "" && status >= http.StatusInternalServerError {
		errorCode = ErrorCodeInternal
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
	if data == nil {
		data = struct{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"meta": m, "data": data})
}
