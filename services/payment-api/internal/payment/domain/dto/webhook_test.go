package dto

import (
	"encoding/json"
	"testing"
	"time"

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

// ── PaidAt (migration 0013) ─────────────────────────────────────────────────

// settlement_time and transaction_time differ, and the difference is the
// point: on a virtual account, transaction_time is when the VA was created and
// settlement_time is when the customer actually paid into it. A receipt is
// about the second one.
func TestNotificationPaidAt_PrefersSettlementTimeOverTransactionTime(t *testing.T) {
	n := Notification{
		TransactionTime: "2026-03-01 17:00:00",
		SettlementTime:  "2026-03-01 17:08:30",
	}
	got := n.PaidAt()
	require.NotNil(t, got)
	// WIB is UTC+7, so 17:08:30 WIB is 10:08:30 UTC.
	assert.Equal(t, time.Date(2026, 3, 1, 10, 8, 30, 0, time.UTC), *got)
	assert.Equal(t, time.UTC, got.Location(), "stored UTC, whatever the gateway sends")
}

// A pending notification carries no settlement_time. transaction_time is then
// the only clock on offer and is still better than ours.
func TestNotificationPaidAt_FallsBackToTransactionTime(t *testing.T) {
	n := Notification{TransactionTime: "2026-03-01 17:00:00"}
	got := n.PaidAt()
	require.NotNil(t, got)
	assert.Equal(t, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC), *got)
}

// Midtrans sends "YYYY-MM-DD HH:mm:ss" with NO zone marker, so the zone has to
// be supplied. Parsing it as UTC would record every Indonesian payment seven
// hours early — and on a ten-minute window that is not a rounding error, it is
// a timestamp before the booking was even held.
func TestNotificationPaidAt_ParsesAsJakartaNotUTC(t *testing.T) {
	n := Notification{SettlementTime: "2026-03-01 00:30:00"}
	got := n.PaidAt()
	require.NotNil(t, got)
	assert.Equal(t, "2026-02-28T17:30:00Z", got.Format(time.RFC3339),
		"00:30 WIB on 1 March is 17:30 UTC on 28 February — 2026 is not a leap year")
}

func TestNotificationPaidAt_NilWhenNeitherTimeIsUsable(t *testing.T) {
	assert.Nil(t, Notification{}.PaidAt())
	assert.Nil(t, Notification{SettlementTime: "not a timestamp"}.PaidAt())
	// A malformed settlement_time still lets a good transaction_time through.
	n := Notification{SettlementTime: "garbage", TransactionTime: "2026-03-01 17:00:00"}
	require.NotNil(t, n.PaidAt())
	// ISO-8601 is NOT what Midtrans sends, and accepting it would hide a
	// change in their format behind a timestamp that happens to parse.
	assert.Nil(t, Notification{SettlementTime: "2026-03-01T17:00:00Z"}.PaidAt())
}
