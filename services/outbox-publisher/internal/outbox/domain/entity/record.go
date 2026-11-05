package entity

import (
	"encoding/json"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// Record is an unpublished outbox row (ADR-005 §4).
type Record struct {
	ID          string
	AggregateID string
	Type        events.Type
	Payload     json.RawMessage
	CreatedAt   time.Time
}

// Envelope converts the row into the message that goes on the queue.
//
// The outbox ID travels with it because the consumer dedupes on that: SQS is
// at-least-once, and the lease below only stops two publishers racing one row,
// not redelivery after a crash.
func (r Record) Envelope() events.Envelope {
	return events.Envelope{
		OutboxID:    r.ID,
		AggregateID: r.AggregateID,
		Type:        r.Type,
		Payload:     r.Payload,
	}
}

// BatchSize is how many rows one pass claims. ADR-005 §9 step 4 fixes it at
// 500: large enough that a backlog drains in few passes, small enough that one
// pass holds its row locks briefly.
const BatchSize = 500

// LeaseTTL is how long a claimed row stays claimed.
//
// ADR-005 §9 step 3: 30 seconds against ~60ms of work is a ~500× margin. A
// permanent flag with no lease leaks — a worker that claims a row and then
// crashes makes every later pass skip it, and the event is lost forever. The
// lease expiring is what lets the next pass retry.
const LeaseTTL = 30 * time.Second

// LeaseDoneTTL is how long a completed marker is kept, so a redelivery inside
// the window is recognised rather than reprocessed.
const LeaseDoneTTL = 24 * time.Hour
