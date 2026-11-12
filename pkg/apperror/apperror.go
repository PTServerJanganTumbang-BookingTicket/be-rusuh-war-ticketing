package apperror

import (
	"net/http"
	"time"
)

// AppError is a transport-agnostic application error carrying an HTTP status,
// a stable machine code, and a human-readable message.
type AppError struct {
	HTTPStatus int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`

	// ErrorCode is the identifier from the API contract's Appendix A
	// (NIK_ALREADY_REGISTERED, CONSENT_REQUIRED, DUKCAPIL_UNAVAILABLE, …).
	// Clients switch on this; Message is for humans and may be reworded freely.
	// Empty means the handler has no contract code for this condition.
	ErrorCode string `json:"error_code,omitempty"`

	// RetryAfter is how long the caller should wait before trying again. Set
	// on 429 responses, where the contract returns a retry_after field
	// alongside the message (§1.1). Zero means "not applicable".
	RetryAfter time.Duration `json:"-"`

	// Data is written as the envelope's data on error (e.g. queue_state,
	// sale_starts_at). Nil means {}.
	Data any `json:"-"`
}

// WithData attaches the error response's data object.
func (e *AppError) WithData(data any) *AppError {
	e.Data = data
	return e
}

// WithCode attaches an Appendix A error code and returns the error, so call
// sites read as one expression:
//
//	apperror.ErrConflict("NIK is bound to another account").WithCode(apperror.CodeNIKAlreadyRegistered)
func (e *AppError) WithCode(errorCode string) *AppError {
	e.ErrorCode = errorCode
	return e
}

// WithRetryAfter attaches a retry hint, for rate-limited responses.
func (e *AppError) WithRetryAfter(d time.Duration) *AppError {
	e.RetryAfter = d
	return e
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
	ErrUnauthorized = func(msg string) *AppError {
		return New(http.StatusUnauthorized, "40100", msg)
	}
	ErrForbidden = func(msg string) *AppError {
		return New(http.StatusForbidden, "40300", msg)
	}
	ErrNotFound = func(msg string) *AppError {
		return New(http.StatusNotFound, "40400", msg)
	}
	ErrConflict = func(msg string) *AppError {
		return New(http.StatusConflict, "40900", msg)
	}
	ErrUnprocessable = func(msg string) *AppError {
		return New(http.StatusUnprocessableEntity, "42200", msg)
	}
	ErrTooManyRequests = func(msg string) *AppError {
		return New(http.StatusTooManyRequests, "42900", msg)
	}
	ErrInternal = func(msg string) *AppError {
		return New(http.StatusInternalServerError, "50000", msg)
	}
	ErrServiceUnavailable = func(msg string) *AppError {
		return New(http.StatusServiceUnavailable, "50300", msg)
	}
)

// Appendix A error codes. Only the ones reachable from the user and nik-verify
// domains are listed; add the rest as their owning services need them.
const (
	CodeValidationError      = "VALIDATION_ERROR"
	CodeNIKNotFound          = "NIK_NOT_FOUND"
	CodeNIKInactive          = "NIK_INACTIVE"
	CodeNIKValidationFailed  = "NIK_VALIDATION_FAILED"
	CodeDukcapilUnavailable  = "DUKCAPIL_UNAVAILABLE"
	CodeNIKAlreadyRegistered = "NIK_ALREADY_REGISTERED"
	CodeNIKAlreadySaved      = "NIK_ALREADY_SAVED"
	CodeMaxSavedNIKsReached  = "MAX_SAVED_NIKS_REACHED"
	CodeConsentRequired      = "CONSENT_REQUIRED"
	CodeEmailAlreadyUsed     = "EMAIL_ALREADY_REGISTERED"
	// CodeEmailNotRegistered is §1.1/§1.2 refusing a magic link for an address
	// with no account. It makes the endpoint an enumeration oracle, which the
	// rate limiter in front of it is the only thing throttling.
	CodeEmailNotRegistered = "EMAIL_NOT_REGISTERED"
	CodeTooManyRequests    = "TOO_MANY_REQUESTS"
	CodeTokenExpired       = "TOKEN_EXPIRED"
	CodeTokenInvalid       = "TOKEN_INVALID"
	CodeAdmissionPaused    = "ADMISSION_PAUSED"

	// Grant and device binding codes live in pkg/grantauth, next to the
	// middleware that is the only thing allowed to emit them.

	// Shared 404: a resource that does not exist and one that is not the
	// caller's answer identically. 403 would confirm the row exists, which is
	// enough to map the active id range (contract Appendix A).
	CodeNotFound = "NOT_FOUND"

	// payment-api (contract §5).
	CodeBookingStatusInvalid      = "BOOKING_STATUS_INVALID"
	CodePaymentAlreadyInitiated   = "PAYMENT_ALREADY_INITIATED"
	CodePaymentAlreadySettled     = "PAYMENT_ALREADY_SETTLED"
	CodePaymentWindowClosed       = "PAYMENT_WINDOW_CLOSED"
	CodePaymentExpired            = "PAYMENT_EXPIRED"
	CodePaymentGatewayUnavailable = "PAYMENT_GATEWAY_UNAVAILABLE"
	CodeMaxAttemptsExceeded       = "MAX_ATTEMPTS_EXCEEDED"
	CodeBankNotSupported          = "BANK_NOT_SUPPORTED"
	// CodeHandoffCodeInvalid is the 401 of POST /payments/handoff/exchange (§5.7, ADR-007).
	CodeHandoffCodeInvalid = "HANDOFF_CODE_INVALID"
)
