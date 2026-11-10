package outbound

import (
	"context"
	"errors"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/valueobject"
)

// ErrNotFound reports that no booking matched the id AND the grant token.
//
// The two are deliberately indistinguishable. A valid grant proves the bearer
// holds a slot, NOT that the slot belongs to the booking_id in the URL, and
// booking ids look like RB-00912 — sequential and trivial to enumerate at
// 300,000 concurrent customers. Answering 403 for "exists but not yours" would
// confirm the id range in use, so both answer 404 (ADR-005 §12).
var ErrNotFound = errors.New("payment: booking not found")

// ErrDuplicateEvent reports that gateway_event_id already exists — the
// notification is a redelivery. The whole transaction is rolled back and the
// endpoint still answers 200.
var ErrDuplicateEvent = errors.New("payment: duplicate gateway event")

// Status and window sentinels. Declared here, in the package both the
// repository and the use case already import, because errors.Is cannot match
// two separately declared errors with the same text — a mistake that turns a
// 409 into a 500 silently.
var (
	// ErrInvalidStatus: the booking is not in a state this endpoint permits.
	ErrInvalidStatus = errors.New("payment: booking status does not permit this")
	// ErrWindowClosed: the payment TTL elapsed, or too little of it remains to
	// hand out another instrument.
	ErrWindowClosed = errors.New("payment: payment window closed")
	// ErrMaxAttempts: MAX_PAYMENT_ATTEMPTS reached. Each attempt mints a new
	// VA or QR at the gateway.
	ErrMaxAttempts = errors.New("payment: max payment attempts exceeded")
	// ErrGatewayUnavailable: the breaker is open, or the gateway could not be
	// reached. Never treated as a declined payment.
	ErrGatewayUnavailable = errors.New("payment: gateway unavailable")
)

// ChargeFunc is the gateway call the repository invokes while it holds the
// bookings row under FOR UPDATE.
//
// Passed in as a closure rather than letting the repository depend on the
// gateway, and rather than handing a transaction handle out to the use case.
// The lock must span the charge: two concurrent "Pay" clicks have to serialise
// somewhere, and the row is the only thing both of them touch.
type ChargeFunc func(ctx context.Context, b entity.Booking, orderID string) (ChargeResult, error)

// CancelFunc voids the previous transaction during §5.3.
//
// Invoked BEFORE the commit. If the order were reversed and the cancel failed,
// the customer would hold a raised attempt counter AND a still-payable VA —
// two live instruments for one booking (api-contracts §5.3).
type CancelFunc func(ctx context.Context, orderID string) error

// InitiateInput is §5.2.
type InitiateInput struct {
	BookingID  string
	GrantToken string
	Method     string
	Bank       string
	Now        time.Time
}

// InitiateOutcome is what the use case renders.
type InitiateOutcome struct {
	Booking entity.Booking
	OrderID string
	// Instructions is the gateway's, or the replayed first charge's.
	Instructions map[string]string
	// Replayed means the booking was already AWAITING_PAYMENT and no charge
	// was sent. The response is identical to the first one: a double-clicked
	// Pay button must not look like a failure.
	Replayed bool
	// Declined means the gateway refused synchronously. payment_attempts has
	// been raised, so the next charge derives a fresh order_id.
	Declined bool
	// DeclineReason is the gateway's status_message, passed through for
	// support rather than for display.
	DeclineReason string
}

// RetryInput is §5.3.
type RetryInput struct {
	BookingID  string
	GrantToken string
	Now        time.Time
	// MinRemaining refuses a switch that would hand out an instrument with
	// almost no time on it: issuing a new VA with 15 seconds left only moves
	// the failure somewhere more painful.
	MinRemaining time.Duration
	MaxAttempts  int
}

// RetryOutcome is what §5.3 renders.
type RetryOutcome struct {
	Booking          entity.Booking
	CancelledOrderID string
	Attempt          int
}

// NotificationInput is §5.5, after the signature has been verified.
type NotificationInput struct {
	BookingID         string
	GatewayEventID    string
	OrderID           string
	TransactionStatus string
	FraudStatus       string
	PaymentType       string
	// GrossAmount in minor units, parsed only for storage. The signature used
	// the original string.
	GrossAmount int64
	Raw         []byte
	Now         time.Time
	MaxAttempts int
}

// NotificationOutcome reports what the transaction did, for logging, metrics
// and the post-commit cleanup.
type NotificationOutcome struct {
	Action     valueobject.Action
	Booking    entity.Booking
	Applied    bool
	QueueToken string
	AccountID  string
	EventID    string
}

// IBookingRepository owns every payment write.
//
// Each method is one transaction. That is the house pattern (user-service's
// RegisterAccount does the same) and it keeps pgx out of the ports: a port that
// hands out pgx.Tx makes every use case a database client.
type IBookingRepository interface {
	// GetOwned is the read-only, ownership-bound fetch behind §5.4.
	GetOwned(ctx context.Context, bookingID, grantToken string) (entity.Booking, error)

	// Initiate: BEGIN, lock owned row, replay or charge, write status, COMMIT.
	Initiate(ctx context.Context, in InitiateInput, charge ChargeFunc) (InitiateOutcome, error)

	// Retry: BEGIN, lock owned row, validate window and attempts, cancel at
	// the gateway, then write and COMMIT.
	Retry(ctx context.Context, in RetryInput, cancel CancelFunc) (RetryOutcome, error)

	// ApplyNotification: BEGIN, insert the idempotency row, lock by
	// payment_ref, apply the transition, issue tickets or release claims,
	// enqueue outbox rows, COMMIT. Needs no gateway call.
	ApplyNotification(ctx context.Context, in NotificationInput) (NotificationOutcome, error)
}

// ICredentialStore is the Redis cleanup that follows a settled booking.
//
// Queue COUNTERS are deliberately absent. ADR-006 §3.2 deleted slots:open and
// §4.1 removed the webhook's inline advance_now_serving + PUBLISH block "tanpa
// pengganti" — admission-ctl recounts from Aurora every tick, so a freed seat
// is picked up within 500 ms without anyone decrementing anything. What remains
// here is credential lifecycle, which is idempotent and still ours.
type ICredentialStore interface {
	// SettleEngagement releases the account's BR-07 hold so it may queue again,
	// deletes the queue token, and writes the settled tombstone that stops a
	// guest who refreshes between settlement and the e-ticket email from seeing
	// an expiry screen (api-contracts §3.3).
	SettleEngagement(ctx context.Context, in SettleInput) error
	// UnscheduleExpiry removes the booking from the sweep's scheduler ZSET.
	UnscheduleExpiry(ctx context.Context, eventID, bookingID string) error
}

// SettleInput is the credential cleanup for one confirmed booking.
type SettleInput struct {
	EventID      string
	BookingID    string
	QueueToken   string
	AccountID    string
	TombstoneTTL time.Duration
}
