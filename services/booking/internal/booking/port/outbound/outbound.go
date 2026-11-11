// Package outbound holds the ports the booking use cases call.
package outbound

import (
	"context"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

// Bookings is the bookings table on the Aurora writer.
type Bookings interface {
	// ClaimForSelection moves the booking GRANTED → SELECTING (once, and only before its deadline; a reload changes
	// nothing) and returns the row. Bound to id AND grant_token. found is false when no such row exists.
	ClaimForSelection(ctx context.Context, bookingID, grantToken string) (b domain.Booking, found bool, err error)
}

// Catalog is the event catalog (events, ticket_categories).
type Catalog interface {
	// Page reads the event and its categories in display order (sort_order, then id) in one round trip. For a
	// logged-in grant (accountID not empty) the same round trip reads the account; acct is nil for a guest and when
	// the account row no longer exists. found is false when the event does not exist.
	Page(ctx context.Context, eventID, accountID string) (e domain.Event, cats []domain.Category, acct *domain.Account, found bool, err error)
}

// Stock reads the live seat counts in Redis.
type Stock interface {
	// Levels reads stock:{ev}:<cat> for every category in one call. missing lists the categories whose key does
	// not exist (unknown, not sold out).
	Levels(ctx context.Context, eventID string, categoryIDs []string) (levels map[string]int64, missing []string, err error)
}

// Metrics counts events by name.
type Metrics interface {
	Incr(name string)
}

// Details reads a booking for GET /bookings/{id} on the Aurora writer.
type Details interface {
	// Detail reads the booking, its event and its items (with ticket ids once issued), bound to id AND grant_token.
	// found is false when no such row exists: an unknown id and another grant's booking look the same.
	Detail(ctx context.Context, bookingID, grantToken string) (d domain.BookingDetail, found bool, err error)
}

// TicketLinks mints the token of an e-ticket link (pkg/eticket).
type TicketLinks interface {
	Mint(ticketID string, expiresAt time.Time) (string, error)
}
