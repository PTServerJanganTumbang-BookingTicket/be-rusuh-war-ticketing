package valueobject

import "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"

// Action is what a webhook notification does to a booking.
//
// The table is api-contracts §5.5 "Perlakuan per tipe event". It is expressed
// as data rather than as branches in the handler because the handler also owns
// a transaction, an idempotency insert and an outbox write — mixing the
// decision into that is how a status ends up half-applied.
type Action int

const (
	// ActionRecordOnly stores the event and changes nothing. Normal for an
	// unpaid VA or e-wallet (`pending`), and the out-of-scope money-movement
	// events (`refund`, `partial_refund`, `chargeback`) which are recorded and
	// alarmed but not acted on in the POC.
	ActionRecordOnly Action = iota

	// ActionConfirm settles the booking: CONFIRMED, one ticket per
	// booking_item, one e-ticket email per holder_email (FR-26).
	ActionConfirm

	// ActionDecline returns the booking to HELD, clears payment_ref and raises
	// payment_attempts. The stock stays with the customer until the TTL — the
	// payment failed, not the claim. The raised attempt is what lets the next
	// charge use a fresh order_id, so the customer simply picks another method
	// and calls §5.2 again.
	ActionDecline

	// ActionExpire releases the seats: EXPIRED, nik_claims deleted so the NIK
	// is free, and a booking.expired email.
	ActionExpire

	// ActionLateSettlement is money arriving for a booking that already
	// expired — the seats have been resold. Nothing transitions. The booking is
	// marked payment_disputed, a payment.failed_notice carrying
	// FailureDisputed goes out (FR-28a condition (c), FR-28b), and
	// LatePaymentRefund alarms for an operator.
	ActionLateSettlement

	// ActionIgnoreUnfunded is `capture` whose fraud_status is not `accept`:
	// authorised but NOT funded. Issuing tickets here would hand out seats for
	// money never received. Stays AWAITING_PAYMENT.
	ActionIgnoreUnfunded
)

// Midtrans transaction_status values.
const (
	TxSettlement    = "settlement"
	TxCapture       = "capture"
	TxPending       = "pending"
	TxDeny          = "deny"
	TxCancel        = "cancel"
	TxFailure       = "failure"
	TxExpire        = "expire"
	TxRefund        = "refund"
	TxPartialRefund = "partial_refund"
	TxChargeback    = "chargeback"
)

// FraudAccept is the only fraud_status that means funded.
const FraudAccept = "accept"

// Decide maps a notification onto an action.
//
// bookingStatus participates because the same notification means different
// things depending on where the booking already is: a settlement for a live
// booking confirms it, and the identical settlement for one that already
// EXPIRED is money owed back.
func Decide(transactionStatus, fraudStatus string, bookingStatus entity.BookingStatus) Action {
	switch transactionStatus {
	case TxSettlement, TxCapture:
		// Checked before the fraud rules: once the seats are gone, the funding
		// question is moot — what matters is that money arrived too late.
		if bookingStatus == entity.StatusExpired || bookingStatus == entity.StatusLapsed {
			return ActionLateSettlement
		}
		if bookingStatus == entity.StatusConfirmed {
			// Already settled, e.g. capture followed by settlement next day.
			// Recorded, not re-applied: re-issuing would duplicate tickets.
			return ActionRecordOnly
		}
		if transactionStatus == TxCapture && fraudStatus != FraudAccept {
			return ActionIgnoreUnfunded
		}
		// settlement with an absent fraud_status is funded: Midtrans omits the
		// field for some non-card channels, and bank_transfer/gopay are the
		// only methods open (§5.1). Requiring it would reject real payments.
		if transactionStatus == TxSettlement && fraudStatus != "" && fraudStatus != FraudAccept {
			return ActionIgnoreUnfunded
		}
		return ActionConfirm

	case TxDeny, TxCancel, TxFailure:
		if bookingStatus.IsTerminal() {
			return ActionRecordOnly
		}
		return ActionDecline

	case TxExpire:
		if bookingStatus.IsTerminal() {
			return ActionRecordOnly
		}
		return ActionExpire

	default:
		// pending, refund, partial_refund, chargeback and anything Midtrans
		// adds later. Recorded so reconciliation can see it; never guessed at.
		return ActionRecordOnly
	}
}

// AlarmsOperator reports whether an action needs a human to look at it.
func (a Action) AlarmsOperator() bool { return a == ActionLateSettlement }
