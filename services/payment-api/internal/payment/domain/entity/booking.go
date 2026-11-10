package entity

import "time"

// BookingStatus is the bookings.status enum (migration 0004 CHECK constraint).
type BookingStatus string

const (
	StatusGranted             BookingStatus = "GRANTED"
	StatusSelecting           BookingStatus = "SELECTING"
	StatusHeld                BookingStatus = "HELD"
	StatusAwaitingPayment     BookingStatus = "AWAITING_PAYMENT"
	StatusPendingCancellation BookingStatus = "PENDING_CANCELLATION"
	StatusConfirmed           BookingStatus = "CONFIRMED"
	StatusExpired             BookingStatus = "EXPIRED"
	StatusLapsed              BookingStatus = "LAPSED"
	StatusAbandoned           BookingStatus = "ABANDONED"
)

// IsTerminal reports whether no further transition is possible.
func (s BookingStatus) IsTerminal() bool {
	switch s {
	case StatusConfirmed, StatusExpired, StatusLapsed, StatusAbandoned:
		return true
	default:
		return false
	}
}

// Booking is the subset of a bookings row payment-api reads.
type Booking struct {
	ID               string
	EventID          string
	AccountID        string
	ContactEmail     string
	Status           BookingStatus
	HeldAt           *time.Time
	PaymentExpiresAt *time.Time
	PaymentRef       string
	PaymentAttempts  int
	PaymentMethod    string
	PaymentDisputed  bool
	GrantToken       string

	// Total is the sum of booking_items.unit_price, computed per request
	// rather than denormalised onto the row. booking_items is the authority
	// on price because unit_price is copied at purchase and must not follow a
	// later catalogue change.
	Total int64
}

// RemainingPaymentWindow is how long is left before payment_expires_at.
//
// Always derived from the stored deadline, which is itself derived from
// held_at — never recomputed from "now + TTL". A customer who switches method
// at minute 8 gets a new instrument with 2 minutes left, not a fresh 10
// (BR-05, api-contracts §5.3).
func (b Booking) RemainingPaymentWindow(now time.Time) time.Duration {
	if b.PaymentExpiresAt == nil {
		return 0
	}
	d := b.PaymentExpiresAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// BookingItem is one seat. One row per NIK (BR-02).
type BookingItem struct {
	ID           string
	BookingID    string
	EventID      string
	CategoryID   string
	CategoryName string
	UnitPrice    int64
	NIKHmac      string
	NIKMasked    string
	HolderEmail  string
}

// Ticket is an issued ticket row.
type Ticket struct {
	ID            string
	BookingID     string
	BookingItemID string
	EventID       string
	PurchaserID   string
	NIKHmac       string
	NIKMasked     string
	HolderEmail   string
	CategoryID    string
	CategoryName  string
	Price         int64
	QRNonce       string
}
