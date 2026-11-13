// Package outbound holds the ports the booking use cases call.
package outbound

import (
	"context"
	"errors"
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

// HoldFunc is called by Orders.Place after the booking row is locked and found SELECTING and inside its time. It runs
// the Redis hold. An error means the answer is unknown.
type HoldFunc func(ctx context.Context) (domain.HoldAnswer, error)

// ErrCommitUnknown means COMMIT failed with an unknown result: the booking may or may not exist.
var ErrCommitUnknown = errors.New("booking: commit result unknown")

// Orders is the write side of POST /bookings on the Aurora writer.
type Orders interface {
	// Status reads the booking before the body is looked at (no lock, no transaction), bound to id AND grant_token.
	// found is false when no such row exists.
	Status(ctx context.Context, bookingID, grantToken string) (s domain.BookingStatus, found bool, err error)
	// Categories reads the ticket categories of an event (price, name), in display order.
	Categories(ctx context.Context, eventID string) ([]domain.Category, error)
	// Identities reads the account's own verified NIK and the saved NIKs among savedIDs, in one round trip.
	Identities(ctx context.Context, accountID string, savedIDs []string) (domain.Identities, error)
	// Place runs the booking transaction: lock the row, repeat the status checks under the lock, call hold, then write
	// HELD with its deadline, the seats and the NIK claims, and commit. A hold refusal commits nothing and is reported
	// as Refused. When the hold was taken and the transaction does not end Placed, the caller gives the hold back; an
	// error that wraps ErrCommitUnknown must not be compensated.
	Place(ctx context.Context, p domain.Placement, hold HoldFunc) (domain.PlaceResult, error)
}

// Holds is the Redis side of the hold: seats and NIK locks, all or nothing (docs/lua-scripts.md §4).
type Holds interface {
	// Hold takes the order. An error means the answer is unknown (Redis unreachable): never treat it as held.
	Hold(ctx context.Context, eventID string, o domain.HoldOrder) (domain.HoldAnswer, error)
	// Release gives a hold back, once. stockMissing counts stock keys that were gone and were not recreated.
	Release(ctx context.Context, eventID string, o domain.HoldOrder) (stockMissing int, err error)
}

// Queue reads the queue state for the sold-out answer.
type Queue interface {
	Snapshot(ctx context.Context, eventID string) (domain.QueueSnapshot, error)
}

// NIKVerifier checks one typed NIK with nik-verify, which alone holds NIK_PEPPER (ADR-005 §8).
type NIKVerifier interface {
	// Validate returns nik-verify's answer for the NIK as typed (nik-verify normalises it again). A NIK that is not
	// valid is an answer (Valid false). Every failure to get an answer is domain.ErrNIKUnavailable.
	Validate(ctx context.Context, nik string) (domain.NIKCheck, error)
}

// Metrics counts events by name.
type Metrics interface {
	Incr(name string)
	// IncrEvent counts one event's metric, kept per event and in the all-events total (docs/metrics.md).
	IncrEvent(name, eventID string)
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
