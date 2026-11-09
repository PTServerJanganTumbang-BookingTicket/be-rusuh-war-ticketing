package rediskeys

// Field names of the queue:{ev} hash (ADR-006 §8.12). Services share these
// constants so a typo is a compile error, not a silent "event not found".
const (
	FieldState        = "state"          // SCHEDULED | OPEN | FULL_BOOKED | CLOSED
	FieldIssued       = "issued"         // last ticket number handed out (HINCRBY)
	FieldNowServing   = "now_serving"    // highest ticket number admitted
	FieldFullBookedAt = "full_booked_at" // epoch seconds since stock first hit zero
	FieldRatePerMin   = "rate_per_min"   // ticket numbers now_serving passes per minute
	FieldName         = "name"           // event name, seeded by sale-ops
	FieldSaleStartsAt = "sale_starts_at" // RFC 3339, seeded by sale-ops
)

// Field names of the token:<queue_token> hash (join writes it, admission-ctl adds the grant).
const (
	FieldTicketNo         = "ticket_no"
	FieldAccountID        = "account_id"  // logged-in customer
	FieldDeviceHash       = "device_hash" // guest
	FieldGrantToken       = "grant_token"
	FieldBookingID        = "booking_id"
	FieldBookingExpiresAt = "booking_expires_at" // RFC 3339, UTC
)

// Field names of the grant:<grant_token> hash (admission-ctl writes it at GRANT, ADR-006 §8.13). It also uses
// FieldBookingID and exactly one of FieldAccountID (logged-in customer) or FieldDeviceHash (guest).
const (
	FieldEventID    = "event_id"
	FieldQueueToken = "queue_token"
)
