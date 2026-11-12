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

// TTL is the deadline block on §5.2.
//
// No ttl_seconds: contract v2.15 removed it from every endpoint that had one.
// It was a second source for one number and stale the moment the response left
// the server. The client counts down from ExpiresAt using ServerTime to correct
// its own clock drift (BR-10).
type TTL struct {
	ExpiresAt  string `json:"expires_at"`
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
// status: PENDING carries expires_at, PAID carries paid_at and redirect_url,
// EXPIRED carries expired_at, FAILED carries reason, can_retry, attempt and
// payment_expires_at.
type StatusResponse struct {
	Status     string `json:"status"`
	ServerTime string `json:"server_time,omitempty"`
	// ExpiresAt is the payment deadline, on PENDING only (v2.15). It replaced
	// ttl_seconds: the client derives the countdown from it and ServerTime.
	ExpiresAt        string `json:"expires_at,omitempty"`
	PaidAt           string `json:"paid_at,omitempty"`
	RedirectURL      string `json:"redirect_url,omitempty"`
	ExpiredAt        string `json:"expired_at,omitempty"`
	Reason           string `json:"reason,omitempty"`
	CanRetry         *bool  `json:"can_retry,omitempty"`
	Attempt          *int   `json:"attempt,omitempty"`
	PaymentExpiresAt string `json:"payment_expires_at,omitempty"`

	// Payment detail (0017). Present once a charge exists, on every status
	// including the terminal ones: a customer reading a receipt, or support
	// reading a failure, needs to know what was charged and how.
	//
	// All omitempty: a booking that never reached a charge has none of them,
	// and sending zeroes would read as "charged nothing" rather than "not
	// charged".
	PaymentID string `json:"payment_id,omitempty"`
	Method    string `json:"method,omitempty"`
	// TotalAmount is what was SENT to the gateway, read from the column
	// written at charge time — not a sum of booking_items, which GetOwned does
	// not compute and which would drift from the charged amount.
	TotalAmount *int64 `json:"total_amount,omitempty"`
	// PaymentStatus is the GATEWAY's transaction_status, deliberately distinct
	// from Status above: ours is the booking's lifecycle, this is what
	// Midtrans last said about the money.
	PaymentStatus string `json:"payment_status,omitempty"`
}

// WithPaymentDetail copies the persisted charge detail onto a status response
// and returns it. Value receiver, so it composes at a single call site without
// mutating anything the caller still holds.
func (r StatusResponse) WithPaymentDetail(paymentID, method, gatewayStatus string, amount *int64) StatusResponse {
	r.PaymentID = paymentID
	r.Method = method
	r.PaymentStatus = gatewayStatus
	r.TotalAmount = amount
	return r
}

// API status values returned by §5.4, distinct from bookings.status.
const (
	StatusPending = "PENDING"
	StatusPaid    = "PAID"
	StatusExpired = "EXPIRED"
	StatusFailed  = "FAILED"
)
