package entity

import "time"

// BookingStatus enumerates the lifecycle states of a booking.
type BookingStatus string

const (
	StatusPendingPayment BookingStatus = "PENDING_PAYMENT"
	StatusPaid           BookingStatus = "PAID"
	StatusExpired        BookingStatus = "EXPIRED"
)

// PaymentTTLSeconds is the fixed payment window opened when a booking is created (BR-05).
const PaymentTTLSeconds = 600

// BookingIDPrefix is the human-readable prefix on a booking's ULID.
const BookingIDPrefix = "RB-"

// Ticket is one held seat within a booking.
type Ticket struct {
	CategoryID   string
	CategoryName string
	Price        int64
	NIK          string
	HolderEmail  string
}

// Event is the embedded event snapshot carried on a booking.
type Event struct {
	ID    string
	Name  string
	Date  string
	Venue string
}

// Booking is the core business entity representing a ticket booking hold.
type Booking struct {
	ID         string
	Event      Event
	Tickets    []Ticket
	Subtotal   int64
	ServiceFee int64
	Total      int64
	Status     BookingStatus
	CreatedAt  time.Time
	ExpiresAt  time.Time
}
