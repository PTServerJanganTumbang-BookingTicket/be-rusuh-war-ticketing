package outbound

import (
	"context"
	"errors"
	"time"
)

// ChargeRequest is POST /v2/charge.
type ChargeRequest struct {
	OrderID     string
	GrossAmount int64
	// PaymentType is Midtrans's own value, which is why the method ids in
	// §5.1 are the Midtrans payment_type strings and need no mapping.
	PaymentType string
	// Bank applies to bank_transfer only.
	Bank string
	// HeldAt anchors custom_expiry.order_time. NOT the request time: a second
	// charge at 10:23 for a booking held at 10:15 must still expire at 10:25,
	// otherwise every method switch grants a fresh 10 minutes and BR-05 is
	// never enforced.
	HeldAt time.Time
	// ExpirySeconds is custom_expiry.expiry_duration, sent with unit "second".
	// Without custom_expiry Midtrans defaults a VA to 24 HOURS, so it stays
	// payable all day after the sweep released the seats and resold them.
	//
	// Seconds rather than minutes because Midtrans accepts second, minute,
	// hour or day, and the payment window is configurable to the second:
	// expressed in minutes, a 7m30s window would truncate to 7 and the
	// gateway would stop accepting payment while the customer still saw
	// thirty seconds on the clock.
	ExpirySeconds int
}

// ChargeResult is the subset of the charge response the client needs.
type ChargeResult struct {
	// Instructions is already shaped for the wire: {bank, va_number} for a VA,
	// {qr_url, deeplink_url} for GoPay.
	Instructions map[string]string
	// TransactionStatus as returned synchronously. Usually "pending"; a
	// synchronous "deny" is a real outcome and the caller must handle it.
	TransactionStatus string
	FraudStatus       string
	StatusCode        string
	StatusMessage     string
	// Raw is the response body, stored in payment_events.raw so that a replay
	// of §5.2 can return identical instructions without calling the gateway.
	Raw []byte
	// GatewayExpiry is the charge response's expiry_time: when MIDTRANS will
	// stop accepting payment. nil when the response omitted it.
	//
	// It is the only evidence that custom_expiry was honoured. Without it a
	// clamped or ignored custom_expiry is invisible: the charge still answers
	// 201, we still promise the customer a 10-minute window, and the virtual
	// account quietly stays payable for Midtrans's 24-hour default - long
	// after the sweep released the seats and sold them to someone else.
	GatewayExpiry *time.Time
}

// Declined reports a synchronously refused charge.
func (r ChargeResult) Declined() bool {
	switch r.TransactionStatus {
	case "deny", "cancel", "failure":
		return true
	default:
		return false
	}
}

// StatusResult is GET /v2/{order_id}/status.
type StatusResult struct {
	TransactionStatus string
	FraudStatus       string
	StatusCode        string
	GrossAmount       string
	// SettlementTime is when the money moved, in Midtrans's "YYYY-MM-DD HH:mm:ss"
	// WIB format. Present on settlement, absent on pending.
	SettlementTime string
	// TransactionTime is when the transaction was created, same format.
	TransactionTime string
	Raw             []byte
}

// ErrAlreadySettled reports that a cancel was refused because the payment had
// already been paid. §5.3 answers 409 PAYMENT_ALREADY_SETTLED and changes
// nothing; the webhook confirms the booking normally.
var ErrAlreadySettled = errors.New("payment: transaction already settled")

// IMidtransClient is the payment gateway.
//
// A WRITE dependency, which is why a timeout is never treated as a failure: the
// charge may well have been created. The breaker opens, the request answers
// 503, payment_ref stays NULL, and the charge reconciler resolves the truth
// later (ADR-005 §8, §9).
type IMidtransClient interface {
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)

	// Cancel is POST /v2/{order_id}/cancel — the merchant voids a pending
	// transaction. Deliberately not Expire: nothing expired when the customer
	// switched method, and Midtrans's dashboard reports the two separately.
	Cancel(ctx context.Context, orderID string) error

	// Expire is POST /v2/{order_id}/expire, called by the admission-ctl sweep
	// rather than by this service. Present because the client is the same one.
	Expire(ctx context.Context, orderID string) error

	// Status is GET /v2/{order_id}/status — READ-ONLY, and the only way the
	// reconciler learns the fate of a charge that timed out.
	Status(ctx context.Context, orderID string) (StatusResult, error)
}

// ICircuitBreaker guards the gateway.
//
// Shared state in Redis, so every Lambda instance sees the same verdict: a
// per-instance breaker learns nothing from the other hundred instances failing.
type ICircuitBreaker interface {
	// Allow reports whether calls are permitted right now.
	Allow(ctx context.Context) (bool, error)
	RecordSuccess(ctx context.Context) error
	RecordFailure(ctx context.Context) error
}
