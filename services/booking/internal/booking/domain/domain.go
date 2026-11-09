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

// Category is one ticket category of an event, in display order.
type Category struct {
	ID          string
	Name        string
	Description string
	Price       int64
	MaxPerOrder int
}
