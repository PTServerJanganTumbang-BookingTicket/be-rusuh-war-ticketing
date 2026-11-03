package apperror

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	err := New(http.StatusTeapot, "41800", "teapot")
	assert.Equal(t, http.StatusTeapot, err.HTTPStatus)
	assert.Equal(t, "41800", err.Code)
	assert.Equal(t, "teapot", err.Message)
}

func TestAppError_Error(t *testing.T) {
	err := New(http.StatusBadRequest, "40000", "bad input")
	assert.Equal(t, "40000: bad input", err.Error())
}

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        *AppError
		wantStatus int
		wantCode   string
	}{
		{"bad request", ErrBadRequest("x"), http.StatusBadRequest, "40000"},
		{"not found", ErrNotFound("x"), http.StatusNotFound, "40400"},
		{"conflict", ErrConflict("x"), http.StatusConflict, "40900"},
		{"internal", ErrInternal("x"), http.StatusInternalServerError, "50000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantStatus, tt.err.HTTPStatus)
			assert.Equal(t, tt.wantCode, tt.err.Code)
			assert.Equal(t, "x", tt.err.Message)
		})
	}
}

func TestWithCode(t *testing.T) {
	err := ErrConflict("NIK is bound to another account").WithCode(CodeNIKAlreadyRegistered)
	assert.Equal(t, http.StatusConflict, err.HTTPStatus)
	assert.Equal(t, CodeNIKAlreadyRegistered, err.ErrorCode)
	assert.Equal(t, "NIK is bound to another account", err.Message)
}

func TestWithCode_EmptyByDefault(t *testing.T) {
	assert.Empty(t, ErrInternal("boom").ErrorCode)
}

func TestNewErrorsAreDistinctInstances(t *testing.T) {
	// WithCode mutates the receiver, so the constructors must hand back a fresh
	// value each call — otherwise one caller's code leaks into another's error.
	a := ErrConflict("a").WithCode(CodeNIKAlreadySaved)
	b := ErrConflict("b")
	assert.Equal(t, CodeNIKAlreadySaved, a.ErrorCode)
	assert.Empty(t, b.ErrorCode)
}

func TestWithRetryAfter(t *testing.T) {
	err := ErrTooManyRequests("slow down").WithCode(CodeTooManyRequests).WithRetryAfter(90 * time.Second)
	assert.Equal(t, http.StatusTooManyRequests, err.HTTPStatus)
	assert.Equal(t, CodeTooManyRequests, err.ErrorCode)
	assert.Equal(t, 90*time.Second, err.RetryAfter)
}

func TestRetryAfter_ZeroByDefault(t *testing.T) {
	assert.Zero(t, ErrBadRequest("x").RetryAfter)
}
