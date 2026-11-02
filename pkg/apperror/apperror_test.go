package apperror

import (
	"net/http"
	"testing"

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
