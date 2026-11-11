package dto

// HandoffResponse is the 201 of POST /payments/{booking_id}/handoff (§5.6).
type HandoffResponse struct {
	BookingID   string `json:"booking_id"`
	HandoffCode string `json:"handoff_code"`
	ExpiresAt   string `json:"expires_at"`
}

// ExchangeRequest is the body of POST /payments/handoff/exchange (§5.7).
type ExchangeRequest struct {
	BookingID   string `json:"booking_id"`
	HandoffCode string `json:"handoff_code"`
}

// ExchangeResponse is the 200 of POST /payments/handoff/exchange (§5.7).
type ExchangeResponse struct {
	BookingID      string `json:"booking_id"`
	PaymentSession string `json:"payment_session"`
	ExpiresAt      string `json:"expires_at"`
}
