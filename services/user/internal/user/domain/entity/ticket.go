package entity

import "time"

// TicketIDPrefix is the human-readable prefix on a ticket's ULID.
const TicketIDPrefix = "TKT-"

// TicketStatus enumerates the lifecycle states of a ticket.
type TicketStatus string

const (
	TicketStatusValid   TicketStatus = "VALID"
	TicketStatusUsed    TicketStatus = "USED"
	TicketStatusExpired TicketStatus = "EXPIRED"
)

// UserTicket is a denormalised view of a ticket from the perspective of its holder.
// In production this would be read from the tickets + booking_items + bookings + events
// tables. For the POC the in-memory repository stores the same shape directly.
type UserTicket struct {
	TicketID      string
	BookingID     string
	EventID       string
	EventName     string
	EventDate     string
	EventTime     string
	EventVenue    string
	EventImageURL string
	Category      string
	Price         int64
	NIKHmac       string
	QRPayload     string
	IssuedAt      time.Time
	Status        TicketStatus
}
