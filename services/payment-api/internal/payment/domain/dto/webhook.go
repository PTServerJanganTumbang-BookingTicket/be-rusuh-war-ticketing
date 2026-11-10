package dto

// Notification is a Midtrans Core API HTTP notification (§5.5).
//
// Every field is a string, including the money and the timestamps, because
// that is how Midtrans sends them and two of them must survive round-tripping
// byte-for-byte:
//
//   - GrossAmount is a decimal string ("3000000.00") and enters the signature
//     formula exactly as received. Decoding it into a number here and
//     re-rendering it later is the single most likely way to make every
//     signature fail.
//   - StatusCode is "200"/"201"/"202"/"407", also part of the signature.
//
// TransactionTime and SettlementTime are "YYYY-MM-DD HH:mm:ss" in WIB with no
// zone marker — not ISO-8601. They must be parsed as Asia/Jakarta.
type Notification struct {
	TransactionTime   string `json:"transaction_time"`
	TransactionStatus string `json:"transaction_status"`
	TransactionID     string `json:"transaction_id"`
	StatusMessage     string `json:"status_message"`
	StatusCode        string `json:"status_code"`
	SignatureKey      string `json:"signature_key"`
	SettlementTime    string `json:"settlement_time"`
	PaymentType       string `json:"payment_type"`
	OrderID           string `json:"order_id"`
	MerchantID        string `json:"merchant_id"`
	GrossAmount       string `json:"gross_amount"`
	FraudStatus       string `json:"fraud_status"`
	Currency          string `json:"currency"`
	VANumbers         []struct {
		Bank     string `json:"bank"`
		VANumber string `json:"va_number"`
	} `json:"va_numbers"`
}

// GatewayEventID is the derived idempotency key.
//
//	order_id + ':' + transaction_status + ':' + status_code
//
// Derived because Midtrans sends no event id in any form — the v2.4 contract
// mapped idempotency onto an `event_id` field that does not exist, which would
// have left the column NULL and the UNIQUE constraint guarding nothing.
//
// Not transaction_id alone: that is stable for the LIFETIME of one transaction,
// so the pending and settlement notifications of the same payment carry an
// identical value. Keying on it would swallow every subsequent legitimate
// event — the exact mistake avoided by not keying on booking_id.
//
// One row per EVENT, not per booking: pending -> settlement -> refund produces
// three rows and all three are processed.
func (n Notification) GatewayEventID() string {
	return n.OrderID + ":" + n.TransactionStatus + ":" + n.StatusCode
}
