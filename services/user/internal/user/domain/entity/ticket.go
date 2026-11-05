package entity

import "time"

// TicketStatus enumerates the lifecycle states of a ticket.
type TicketStatus string

const (
	TicketStatusValid   TicketStatus = "VALID"
	TicketStatusUsed    TicketStatus = "USED"
	TicketStatusExpired TicketStatus = "EXPIRED"
	TicketStatusRevoked TicketStatus = "REVOKED"
)

// UserTicket is a ticket as its owner sees it, joined across tickets and events.
// booking-service owns the writes; this service reads.
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
	// NIKMasked is the holder's display form. The hash is deliberately not
	// carried into this read model: nothing in the ticket views needs it, and
	// not carrying it means it cannot leak into a response by accident.
	NIKMasked string
	QRNonce   string
	IssuedAt  time.Time
	Status    TicketStatus
}
