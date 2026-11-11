// Package domain holds the booking entities the use cases work with.
package domain

import (
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
)

// Contract codes used by booking (api-contracts Appendix A).
const (
	CodeNotFound           = "NOT_FOUND"
	CodeStockUninitialized = "STOCK_UNINITIALIZED"
)

// Metric names.
const (
	// MetricLostGrant counts a live grant hash whose booking row does not exist: admission-ctl writes the row first,
	// so this means data loss or a bug.
	MetricLostGrant = "LostGrant"
	// MetricStockUninitialized counts a ticket page that found a stock key missing in Redis.
	MetricStockUninitialized = "StockUninitialized"
	// MetricUnknownPaymentMethod counts a paid booking whose payment_method has no display label yet.
	MetricUnknownPaymentMethod = "UnknownPaymentMethod"
	// MetricPaymentDeadlineMissing counts a pending-payment booking without payment_expires_at (a data bug).
	MetricPaymentDeadlineMissing = "PaymentDeadlineMissing"
)

// Booking is the part of a bookings row the ticket page needs.
type Booking struct {
	ID               string
	EventID          string
	Status           bookingstate.Status
	BookingExpiresAt time.Time
	PaymentExpiresAt time.Time // zero before HELD
}

// Event is the catalog row rendered at the top of the page.
type Event struct {
	ID       string
	Name     string
	Date     string // YYYY-MM-DD, as the contract shows it
	Venue    string
	ImageURL string
}

// Account is what the ticket page shows of a logged-in customer: the email that pre-fills contact_email and the
// masked NIK offered for the first seat. NIKMasked is nil when the account has no verified NIK. The full NIK and its
// hash are never read (NFR-11).
type Account struct {
	Email     string  `json:"email"`
	NIKMasked *string `json:"nik_masked"`
}

// Category is one ticket category of an event, in display order.
type Category struct {
	ID          string
	Name        string
	Description string
	Price       int64
	MaxPerOrder int
}

// BookingDetail is everything GET /bookings/{id} renders: the booking, its event and one item per seat.
type BookingDetail struct {
	ID               string
	EventID          string
	Status           bookingstate.Status
	PaymentExpiresAt time.Time // zero before HELD
	PaymentMethod    string    // bank_transfer | gopay | "" (set by payment-api)
	Event            Event
	Items            []Item
}

// Item is one seat (booking_items), with its ticket once issued.
type Item struct {
	ID           string
	CategoryName string
	UnitPrice    int64
	NIKMasked    string
	TicketID     string // "" until the ticket is issued
	TicketStatus string // VALID | USED | EXPIRED | REVOKED; "" until issued
}
