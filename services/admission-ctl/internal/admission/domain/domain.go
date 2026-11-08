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
}

// Admission is who is inside an event right now (ADR-006 §3.2).
type Admission struct {
	Open     int64 // every active status: GRANTED .. PENDING_CANCELLATION
	Browsing int64 // GRANTED and SELECTING only
}
