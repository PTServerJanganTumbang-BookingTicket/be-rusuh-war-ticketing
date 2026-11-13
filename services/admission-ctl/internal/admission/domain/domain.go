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

// Ended is a booking the sweep just ended: GRANTED → LAPSED (the customer never opened the ticket page) or
// SELECTING → EXPIRED (opened it, but did not press Pay within the 5 minutes).
type Ended struct {
	BookingID  string
	EventID    string
	QueueToken string
	GrantToken string
}

// SeatCount is the number of seats a held booking took in one category.
type SeatCount struct {
	CategoryID string
	Qty        int64
}

// EndedHold is a HELD booking the sweep just expired (nobody started paying within 10 minutes), with what its hold took
// and must give back: the seats per category from booking_items and the NIK hashes of the claims that were deleted.
type EndedHold struct {
	Ended
	Seats []SeatCount
	NIKs  []string
}

// StockExpectation is what Aurora says one category should hold in Redis: its capacity (immutable during a sale) minus the
// seats of the bookings that still take them (HELD, AWAITING_PAYMENT, PENDING_CANCELLATION, CONFIRMED).
type StockExpectation struct {
	CategoryID string
	Capacity   int64
	Taken      int64
}

// Expected is the stock Redis should hold. Never negative: more seats taken than capacity is an Aurora-side problem that
// reconciliation does not paper over by driving the counter below zero.
func (e StockExpectation) Expected() int64 { return max(e.Capacity-e.Taken, 0) }

// AdjustResult is what the compare-and-adjust script answered (docs/lua-scripts.md §5).
type AdjustResult struct {
	Code  string // ADJUSTED, CHANGED, MISSING or BELOW_ZERO
	Value int64
}

// Answers of the compare-and-adjust script.
const (
	AdjustAdjusted  = "ADJUSTED"
	AdjustChanged   = "CHANGED"
	AdjustMissing   = "MISSING"
	AdjustBelowZero = "BELOW_ZERO"
)

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

// SweepResult is the outcome of one backstop sweep invocation on payment-api.
type SweepResult struct {
	Expired   int // bookings moved to EXPIRED
	Settled   int // bookings confirmed by the sweep (alarm: webhook is broken)
	Skipped   int // bookings skipped (gateway unreachable or no payment_ref)
	Duplicate int // bookings already processed by webhook or previous sweep
}
