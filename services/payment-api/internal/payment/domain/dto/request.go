package dto

// InitiateRequest is the body of POST /payments/{booking_id} (§5.2).
//
// The shape differs per method: bank_transfer carries a bank, gopay carries
// nothing else. `bank` is validated against the opened list in the use case
// rather than with a struct tag, so the rejection can carry
// BANK_NOT_SUPPORTED instead of a generic validation error.
type InitiateRequest struct {
	Method string `json:"method" validate:"required,oneof=bank_transfer gopay"`
	Bank   string `json:"bank"`
}
