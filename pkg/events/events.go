// Package events is the shared vocabulary of the transactional outbox.
//
// Producers (user, booking) write rows to the `outbox` table; outbox-publisher
// forwards them to SQS; notification consumes them and sends mail. All three
// sides depend on this package rather than on string literals, so a renamed
// type is a compile error instead of a silently undelivered email.
//
// The type list is ADR-005 §9 step 4, plus AuthMagicLink. That one is not in
// the ADR: the ADR covers account.verify (registration) but the API contract
// §1.1 also specifies a passwordless login link, which needs the same channel.
// Flagged in the notification README as a contract addition.
package events

import "encoding/json"

// Type identifies an outbox row's payload shape. Values are the wire format —
// they appear in the database and on the queue, so they are append-only.
type Type string

const (
	// TypeAccountVerify confirms ownership of an email address at registration
	// (FR-31). Payload: AccountVerifyPayload.
	TypeAccountVerify Type = "account.verify"

	// TypeAuthMagicLink is a passwordless login link (API contract §1.1).
	// Payload: AuthMagicLinkPayload.
	TypeAuthMagicLink Type = "auth.magic_link"

	// TypeTicketIssue delivers one e-ticket to one NIK holder (FR-26, FR-27).
	// Owned by booking-service. Payload: TicketIssuePayload.
	TypeTicketIssue Type = "ticket.issue"

	// TypeBookingExpired tells the buyer and each holder that payment lapsed
	// (FR-28). Owned by booking-service.
	TypeBookingExpired Type = "booking.expired"

	// TypePaymentFailedNotice covers the three failure conditions in FR-28a:
	// gateway failure, TTL plus grace with no callback, and a success callback
	// arriving after final cancellation (Disputed). Owned by payment-api.
	TypePaymentFailedNotice Type = "payment.failed_notice"
)

// IsKnown reports whether this type has a registered payload shape. An
// unknown type must be left on the queue rather than dropped: it usually means
// a producer was deployed ahead of its consumer.
func (t Type) IsKnown() bool {
	switch t {
	case TypeAccountVerify, TypeAuthMagicLink, TypeTicketIssue,
		TypeBookingExpired, TypePaymentFailedNotice:
		return true
	default:
		return false
	}
}

// Locale selects the language of a rendered email (FR-32).
type Locale string

const (
	LocaleID Locale = "id"
	LocaleEN Locale = "en"
)

// Normalize returns a supported locale, defaulting to Indonesian.
func (l Locale) Normalize() Locale {
	if l == LocaleEN {
		return LocaleEN
	}
	return LocaleID
}

// Envelope is what travels on the queue: the outbox row's identity plus its
// payload, still encoded.
//
// OutboxID is carried because the consumer dedupes on it. SQS is
// at-least-once and the publisher's lease guard only prevents two workers
// racing one row — it does not prevent redelivery after a crash (ADR-005 §9
// step 3), so the consumer needs a stable key of its own.
type Envelope struct {
	OutboxID    string          `json:"outbox_id"`
	AggregateID string          `json:"aggregate_id"`
	Type        Type            `json:"type"`
	Payload     json.RawMessage `json:"payload"`
}

// AccountVerifyPayload is the payload for TypeAccountVerify.
//
// Token is the plaintext link token. It exists on the queue and in the outbox
// row because the email cannot be built without it — which is precisely why
// the `outbox` table is not a general-purpose audit log and why its retention
// is short. The accounts table stores only the hash.
type AccountVerifyPayload struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
	Token     string `json:"token"`
	Locale    Locale `json:"locale,omitempty"`
}

// AuthMagicLinkPayload is the payload for TypeAuthMagicLink.
//
// AccountID is empty when no account exists for the address. The email is sent
// either way, so that a response cannot be used to enumerate which addresses
// are registered.
type AuthMagicLinkPayload struct {
	AccountID string `json:"account_id,omitempty"`
	Email     string `json:"email"`
	Token     string `json:"token"`
	Locale    Locale `json:"locale,omitempty"`
}

// TicketIssuePayload is the payload for TypeTicketIssue — one per seat, so one
// email per NIK holder (FR-26).
type TicketIssuePayload struct {
	TicketID    string `json:"ticket_id"`
	BookingID   string `json:"booking_id"`
	HolderEmail string `json:"holder_email"`
	EventName   string `json:"event_name"`
	EventDate   string `json:"event_date"`
	EventVenue  string `json:"event_venue"`
	Category    string `json:"category"`
	NIKMasked   string `json:"nik_masked"`
	QRValue     string `json:"qr_value"`
	Locale      Locale `json:"locale,omitempty"`
}

// BookingExpiredPayload is the payload for TypeBookingExpired.
type BookingExpiredPayload struct {
	BookingID    string   `json:"booking_id"`
	ContactEmail string   `json:"contact_email"`
	HolderEmails []string `json:"holder_emails,omitempty"`
	EventName    string   `json:"event_name"`
	Locale       Locale   `json:"locale,omitempty"`
}

// FailureReason distinguishes the three conditions of FR-28a. Condition (c)
// must state that money was received, the booking is cancelled, and a refund
// is being processed — FR-28b makes that an email, not just a log line.
type FailureReason string

const (
	FailureGatewayDeclined FailureReason = "gateway_declined"
	FailureTTLElapsed      FailureReason = "ttl_elapsed"
	FailureDisputed        FailureReason = "disputed"
)

// PaymentFailedNoticePayload is the payload for TypePaymentFailedNotice.
type PaymentFailedNoticePayload struct {
	BookingID    string        `json:"booking_id"`
	OrderID      string        `json:"order_id,omitempty"`
	ContactEmail string        `json:"contact_email"`
	EventName    string        `json:"event_name"`
	GrossAmount  int64         `json:"gross_amount,omitempty"`
	Reason       FailureReason `json:"reason"`
	Locale       Locale        `json:"locale,omitempty"`
}
