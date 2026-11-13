// Package valueobject holds the payment domain's value objects.
package valueobject

import "time"

// wib is Asia/Jakarta, fixed rather than loaded from the host tzdata: a
// Lambda image without zoneinfo would silently fall back to UTC and shift
// every timestamp by seven hours.
var wib = time.FixedZone("WIB", 7*60*60)

// MidtransTimeLayout is how Midtrans writes every timestamp it sends:
// no zone marker, WIB implied. Exported for the one place that needs to
// format a timestamp for the gateway (custom_expiry).
const MidtransTimeLayout = "2006-01-02 15:04:05"

// GatewayTime parses Midtrans settlement and transaction times into UTC.
//
// settlement_time first, transaction_time second. They differ, and the
// difference is the point: on a virtual account, transaction_time is when the
// VA was created and settlement_time is when the customer actually paid into
// it — up to ten minutes apart, and it is the second one a receipt is about.
//
// Nil when neither field is present or parseable. The caller substitutes its
// own clock then, because a confirmed booking with no payment time at all is
// worse than one whose time is a few seconds late: §5.4 has to answer paid_at
// with something.
func GatewayTime(settlement, transaction string) *time.Time {
	for _, raw := range []string{settlement, transaction} {
		if raw == "" {
			continue
		}
		t, err := time.ParseInLocation(MidtransTimeLayout, raw, wib)
		if err != nil {
			continue
		}
		utc := t.UTC()
		return &utc
	}
	return nil
}
