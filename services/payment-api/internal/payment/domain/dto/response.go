package dto

// MethodsResponse is GET /payments/{booking_id}/methods (§5.1).
type MethodsResponse struct {
	Methods []Method `json:"methods"`
}

// Method mirrors entity.Method on the wire.
type Method struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	Enabled     bool   `json:"enabled"`
	// Banks is omitted entirely for methods that have none, rather than sent
	// as an empty array: the client's check is "is there a bank step", and an
	// empty array answers that ambiguously.
	Banks []Bank `json:"banks,omitempty"`
}

// Bank is one bank choice under bank_transfer.
type Bank struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	IconURL string `json:"icon_url"`
}

// TTL is the countdown block shared by initiate and status.
type TTL struct {
	ExpiresAt  string `json:"expires_at"`
	TTLSeconds int    `json:"ttl_seconds"`
	ServerTime string `json:"server_time"`
}

// InitiateResponse is POST /payments/{booking_id} (§5.2).
//
// Instructions is method-shaped, so it is a map rather than a struct: a VA
// carries {bank, va_number}, a GoPay charge carries {qr_url, deeplink_url}.
type InitiateResponse struct {
	PaymentID    string            `json:"payment_id"`
	Method       string            `json:"method"`
	Status       string            `json:"status"`
	Instructions map[string]string `json:"instructions"`
	TTL          TTL               `json:"ttl"`
}

// RetryResponse is POST /payments/{booking_id}/retry (§5.3).
type RetryResponse struct {
	BookingID        string `json:"booking_id"`
	Status           string `json:"status"`
	Attempt          int    `json:"attempt"`
	CancelledOrderID string `json:"cancelled_order_id"`
	PaymentExpiresAt string `json:"payment_expires_at"`
	ServerTime       string `json:"server_time"`
}

// StatusResponse is GET /payments/{booking_id}/status (§5.4).
//
// Fields are omitempty because the contract returns a different subset per
// status: PENDING carries ttl_seconds, PAID carries paid_at and redirect_url,
// EXPIRED carries expired_at, FAILED carries reason, can_retry, attempt and
// payment_expires_at.
type StatusResponse struct {
	Status           string `json:"status"`
	ServerTime       string `json:"server_time,omitempty"`
	TTLSeconds       *int   `json:"ttl_seconds,omitempty"`
	PaidAt           string `json:"paid_at,omitempty"`
	RedirectURL      string `json:"redirect_url,omitempty"`
	ExpiredAt        string `json:"expired_at,omitempty"`
	Reason           string `json:"reason,omitempty"`
	CanRetry         *bool  `json:"can_retry,omitempty"`
	Attempt          *int   `json:"attempt,omitempty"`
	PaymentExpiresAt string `json:"payment_expires_at,omitempty"`
}

// API status values returned by §5.4, distinct from bookings.status.
const (
	StatusPending = "PENDING"
	StatusPaid    = "PAID"
	StatusExpired = "EXPIRED"
	StatusFailed  = "FAILED"
)
