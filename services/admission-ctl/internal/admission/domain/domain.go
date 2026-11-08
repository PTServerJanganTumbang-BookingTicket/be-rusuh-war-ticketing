// Package domain holds the admission-ctl types shared by its ports and adapters.
package domain

import "time"

// Grant is one customer's turn, written to bookings as a GRANTED row.
type Grant struct {
	EventID    string
	QueueToken string
	AccountID  string // empty for a guest
	BookingID  string
	GrantToken string
	GrantedAt  time.Time
	ExpiresAt  time.Time // booking_expires_at: granted_at + the booking TTL
}

// Booking is what the store gives back after a grant is saved. When the grant
// was already saved, it is the first row's id and grant_token, not the new ones.
type Booking struct {
	ID         string
	GrantToken string
	GrantedAt  time.Time
	ExpiresAt  time.Time
	Status     string // GRANTED unless the row already moved on (lapsed, selecting, ...)
}

// Lapsed is a booking the sweep just moved GRANTED → LAPSED: the customer did not start choosing seats in time.
type Lapsed struct {
	BookingID  string
	EventID    string
	QueueToken string
	GrantToken string
}

// Event is one sale the controller ticks.
type Event struct {
	ID         string
	Categories []string // stock categories: the Lua sums stock:{ev}:<category>
}

// TokenInfo is what join stored in token:<queue_token> that a grant needs.
type TokenInfo struct {
	AccountID  string // logged-in customer
	DeviceHash string // guest
}

// EventView is the sale state admission-ctl reads for the news.
type EventView struct {
	State        string
	FullBookedAt int64 // epoch seconds, 0 when not fully booked
}

// Admission is who is inside an event right now (ADR-006 §3.2).
type Admission struct {
	Open     int64 // every active status: GRANTED .. PENDING_CANCELLATION
	Browsing int64 // GRANTED and SELECTING only
}
