package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Derived, because Midtrans sends no event id in any form. The v2.4 contract
// mapped idempotency onto an `event_id` field that does not exist, which would
// have left the column NULL and the UNIQUE constraint guarding nothing.
func TestGatewayEventID(t *testing.T) {
	n := Notification{OrderID: "RB-00912:1", TransactionStatus: "settlement", StatusCode: "200"}
	assert.Equal(t, "RB-00912:1:settlement:200", n.GatewayEventID())
}

// Not transaction_id: that is stable for the LIFETIME of one transaction, so
// the pending and settlement notifications of the same payment carry an
// identical value. Keying on it would swallow every later legitimate event.
func TestGatewayEventID_DistinguishesEventsOfOneTransaction(t *testing.T) {
	pending := Notification{OrderID: "RB-00912:1", TransactionStatus: "pending", StatusCode: "201", TransactionID: "tx-1"}
	settled := Notification{OrderID: "RB-00912:1", TransactionStatus: "settlement", StatusCode: "200", TransactionID: "tx-1"}

	assert.NotEqual(t, pending.GatewayEventID(), settled.GatewayEventID())
}

// One row per EVENT, not per booking: two attempts on one booking are
// different keys, so neither is mistaken for the other's redelivery.
func TestGatewayEventID_IsPerAttempt(t *testing.T) {
	first := Notification{OrderID: "RB-00912:1", TransactionStatus: "deny", StatusCode: "202"}
	second := Notification{OrderID: "RB-00912:2", TransactionStatus: "deny", StatusCode: "202"}

	assert.NotEqual(t, first.GatewayEventID(), second.GatewayEventID())
}

// Every field is a string because that is how Midtrans sends them, and two of
// them must survive round-tripping byte for byte or the signature never
// matches.
func TestNotification_MoneyAndCodesDecodeAsStrings(t *testing.T) {
	const body = `{
      "order_id": "RB-00912:1",
      "status_code": "200",
      "gross_amount": "3000000.00",
      "transaction_status": "settlement",
      "transaction_time": "2026-03-01 10:07:12",
      "settlement_time": "2026-03-01 10:07:30",
      "va_numbers": [{"bank": "bca", "va_number": "8277081234567890"}]
    }`

	var n Notification
	require.NoError(t, json.Unmarshal([]byte(body), &n))

	assert.Equal(t, "3000000.00", n.GrossAmount)
	assert.Equal(t, "200", n.StatusCode)
	// Zone-less WIB, not ISO-8601: kept verbatim so whoever parses it knows
	// it needs Asia/Jakarta rather than assuming UTC.
	assert.Equal(t, "2026-03-01 10:07:12", n.TransactionTime)
	assert.Equal(t, "2026-03-01 10:07:30", n.SettlementTime)
	require.Len(t, n.VANumbers, 1)
	assert.Equal(t, "8277081234567890", n.VANumbers[0].VANumber)
}

// Re-encoding must not reformat the money: payment_events.raw is what a later
// reconciliation recomputes a signature from.
func TestNotification_RoundTripsGrossAmountUnchanged(t *testing.T) {
	n := Notification{OrderID: "RB-00912:1", StatusCode: "200", GrossAmount: "3000000.00"}

	raw, err := json.Marshal(n)
	require.NoError(t, err)

	var back Notification
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, "3000000.00", back.GrossAmount)
}
