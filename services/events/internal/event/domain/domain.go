// Package domain holds the events service's entities and the pure rules for the sale state shown on the home page.
package domain

import "time"

// QueueState is where the sale is right now, as the client sees it (api-contracts §2.1).
type QueueState string

// The five states. Join's error data uses the same names.
const (
	StateScheduled  QueueState = "SCHEDULED"   // not open yet
	StateOpen       QueueState = "OPEN"        // open, the customer can join
	StateFullQueue  QueueState = "FULL_QUEUE"  // the queue reached its maximum length
	StateFullBooked QueueState = "FULL_BOOKED" // every seat is held; seats may come back
	StateClosed     QueueState = "CLOSED"      // sold out, final
)

// Internal states as written in queue:{ev}.state (pkg/rediskeys fields).
const (
	internalOpen       = "OPEN"
	internalFullBooked = "FULL_BOOKED"
	internalClosed     = "CLOSED"
)

// DeriveQueueState maps queue:{ev}.state and the queue depth to the client state. The checks run in the same order as
// join's gates, so a card and join agree: closed, then fully booked, then a full queue. A missing, SCHEDULED or unknown
// state shows SCHEDULED (join answers 503 QUEUE_NOT_READY for an unknown one).
func DeriveQueueState(internal string, depth, capacity int64) QueueState {
	switch internal {
	case internalClosed:
		return StateClosed
	case internalFullBooked:
		return StateFullBooked
	case internalOpen:
		if depth >= capacity {
			return StateFullQueue
		}
		return StateOpen
	default:
		return StateScheduled
	}
}

// Known reports whether internal is one of the four states Redis may hold.
func Known(internal string) bool {
	switch internal {
	case "SCHEDULED", internalOpen, internalFullBooked, internalClosed:
		return true
	}
	return false
}

// ScheduleState is the fallback when Redis cannot be read: SCHEDULED before sale_starts_at (or when it is not set),
// OPEN after it. Join's atomic check stays the real gate.
func ScheduleState(saleStartsAt *time.Time, now time.Time) QueueState {
	if saleStartsAt == nil || now.Before(*saleStartsAt) {
		return StateScheduled
	}
	return StateOpen
}

// Enabled is the button: a customer can join while the sale is OPEN or FULL_BOOKED.
func (s QueueState) Enabled() bool { return s == StateOpen || s == StateFullBooked }

// Event is one row of the catalog.
type Event struct {
	ID           string
	Name         string
	Category     string
	Venue        string
	City         string
	Date         time.Time // event_date; only the day is used
	Time         string    // "HH:MM"
	ImageURL     string
	Description  string
	SaleStartsAt *time.Time // nil until the sale is scheduled
}

// Category is one ticket category of an event.
type Category struct {
	ID          string
	Name        string
	Description string
	Price       int64
	Available   int64 // Aurora snapshot; the live count is stock:{ev}:<category>
}

// Queue is what queue:{ev} says about an event. State is empty when the hash is missing.
type Queue struct {
	State string
	Depth int64 // max(0, issued − now_serving)
}

// Metrics.
const (
	MetricRedisFallback = "EventsRedisFallback"
	MetricUnknownState  = "EventsUnknownQueueState"
)

// Error codes (api-contracts Appendix A).
const (
	CodeValidation = "VALIDATION_ERROR"
	CodeNotFound   = "NOT_FOUND"
)
