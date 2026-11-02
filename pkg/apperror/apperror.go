package apperror

import "net/http"

// AppError is a transport-agnostic application error carrying an HTTP status,
// a stable machine code, and a human-readable message.
type AppError struct {
	HTTPStatus int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e *AppError) Error() string {
	return e.Code + ": " + e.Message
}

// New builds an AppError.
func New(httpStatus int, code, message string) *AppError {
	return &AppError{HTTPStatus: httpStatus, Code: code, Message: message}
}

// Predefined errors used across domains.
var (
	ErrBadRequest = func(msg string) *AppError {
		return New(http.StatusBadRequest, "40000", msg)
	}
	ErrNotFound = func(msg string) *AppError {
		return New(http.StatusNotFound, "40400", msg)
	}
	ErrConflict = func(msg string) *AppError {
		return New(http.StatusConflict, "40900", msg)
	}
	ErrInternal = func(msg string) *AppError {
		return New(http.StatusInternalServerError, "50000", msg)
	}
)
