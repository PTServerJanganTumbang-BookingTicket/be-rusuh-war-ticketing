package entity

// EngagementKind is what a returning customer was in the middle of (BR-09).
type EngagementKind string

const (
	// EngagementNone means the account has nothing in flight.
	EngagementNone EngagementKind = ""
	// EngagementQueue means a live queue position, no booking yet.
	EngagementQueue EngagementKind = "queue"
	// EngagementBooking means a booking that has not reached a terminal state.
	EngagementBooking EngagementKind = "booking"
)

// Engagement is the account's one live involvement.
//
// "One" is BR-07: a logged-in customer holds at most a single queue position
// or booking per event, released when it goes terminal. So this is a lookup,
// not a list.
type Engagement struct {
	Kind      EngagementKind
	EventID   string
	BookingID string
}

// IsActive reports whether there is anywhere to send the customer back to.
func (e Engagement) IsActive() bool { return e.Kind != EngagementNone }
