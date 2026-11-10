package valueobject

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
)

func TestDecide_Settlement_Confirms(t *testing.T) {
	for _, st := range []entity.BookingStatus{
		entity.StatusAwaitingPayment,
		entity.StatusPendingCancellation,
		entity.StatusHeld,
	} {
		assert.Equal(t, ActionConfirm, Decide(TxSettlement, FraudAccept, st), string(st))
	}
}

// Midtrans omits fraud_status on some non-card channels, and bank_transfer and
// gopay are the only methods opened. Requiring the field would reject real,
// funded payments.
func TestDecide_SettlementWithoutFraudStatus_IsFunded(t *testing.T) {
	assert.Equal(t, ActionConfirm, Decide(TxSettlement, "", entity.StatusAwaitingPayment))
}

func TestDecide_SettlementWithRejectedFraud_IsNotFunded(t *testing.T) {
	assert.Equal(t, ActionIgnoreUnfunded, Decide(TxSettlement, "deny", entity.StatusAwaitingPayment))
	assert.Equal(t, ActionIgnoreUnfunded, Decide(TxSettlement, "challenge", entity.StatusAwaitingPayment))
}

// capture is an authorisation. Without fraud_status=accept the money has NOT
// moved, so issuing tickets would hand out seats for funds never received.
func TestDecide_Capture_RequiresAccept(t *testing.T) {
	assert.Equal(t, ActionConfirm, Decide(TxCapture, FraudAccept, entity.StatusAwaitingPayment))
	assert.Equal(t, ActionIgnoreUnfunded, Decide(TxCapture, "", entity.StatusAwaitingPayment))
	assert.Equal(t, ActionIgnoreUnfunded, Decide(TxCapture, "challenge", entity.StatusAwaitingPayment))
}

// Money arriving after the seats were resold. Nothing transitions; an operator
// is alarmed and the customer is routed to support.
func TestDecide_LateSettlement(t *testing.T) {
	for _, st := range []entity.BookingStatus{entity.StatusExpired, entity.StatusLapsed} {
		assert.Equal(t, ActionLateSettlement, Decide(TxSettlement, FraudAccept, st), string(st))
		assert.Equal(t, ActionLateSettlement, Decide(TxCapture, FraudAccept, st), string(st))
	}
}

// Checked before the fraud rules: once the seats are gone the funding question
// is moot, so an unfunded capture for an expired booking is still reported to
// an operator rather than quietly ignored.
func TestDecide_LateSettlementBeatsFraudCheck(t *testing.T) {
	assert.Equal(t, ActionLateSettlement, Decide(TxCapture, "challenge", entity.StatusExpired))
}

// capture one day, settlement the next, for a booking already CONFIRMED.
// Re-applying would issue a second set of tickets for the same seats.
func TestDecide_AlreadyConfirmed_RecordsOnly(t *testing.T) {
	assert.Equal(t, ActionRecordOnly, Decide(TxSettlement, FraudAccept, entity.StatusConfirmed))
	assert.Equal(t, ActionRecordOnly, Decide(TxCapture, FraudAccept, entity.StatusConfirmed))
}

func TestDecide_Declines(t *testing.T) {
	for _, tx := range []string{TxDeny, TxCancel, TxFailure} {
		assert.Equal(t, ActionDecline, Decide(tx, "", entity.StatusAwaitingPayment), tx)
	}
}

func TestDecide_Expire(t *testing.T) {
	assert.Equal(t, ActionExpire, Decide(TxExpire, "", entity.StatusAwaitingPayment))
}

// A terminal booking cannot be moved by a decline or an expiry: the seats have
// already been released or sold, and a second release would be a double free.
func TestDecide_TerminalBooking_RecordsOnly(t *testing.T) {
	for _, st := range []entity.BookingStatus{
		entity.StatusConfirmed, entity.StatusExpired, entity.StatusLapsed, entity.StatusAbandoned,
	} {
		for _, tx := range []string{TxDeny, TxCancel, TxFailure, TxExpire} {
			assert.Equal(t, ActionRecordOnly, Decide(tx, "", st), string(st)+"/"+tx)
		}
	}
}

// pending is the normal state of an unpaid VA, and the money-movement events
// are out of POC scope: all are stored for reconciliation and acted on by
// nobody. Anything Midtrans adds later lands here too rather than being
// guessed at.
func TestDecide_RecordOnlyStatuses(t *testing.T) {
	for _, tx := range []string{
		TxPending, TxRefund, TxPartialRefund, TxChargeback, "something_new",
	} {
		assert.Equal(t, ActionRecordOnly, Decide(tx, "", entity.StatusAwaitingPayment), tx)
	}
}

func TestAction_AlarmsOperator(t *testing.T) {
	assert.True(t, ActionLateSettlement.AlarmsOperator())
	for _, a := range []Action{
		ActionRecordOnly, ActionConfirm, ActionDecline, ActionExpire, ActionIgnoreUnfunded,
	} {
		assert.False(t, a.AlarmsOperator())
	}
}
