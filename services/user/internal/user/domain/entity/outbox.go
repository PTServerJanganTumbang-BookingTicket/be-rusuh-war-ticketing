package entity

import (
	"encoding/json"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// OutboxEvent is a row of the transactional outbox (ADR-005 §4, §9).
//
// It is always written in the same transaction as the state change it
// describes. Publishing is a separate, retryable step, which is what lets a
// broker outage leave a registration intact (NFR-10).
type OutboxEvent struct {
	ID          string
	AggregateID string
	Type        events.Type
	Payload     json.RawMessage
	CreatedAt   time.Time
	PublishedAt *time.Time
}

// NewOutboxEvent marshals a payload into an event row.
//
// Encoding happens here rather than in the adapter so a payload that cannot be
// marshalled fails before the transaction opens, not halfway through it.
func NewOutboxEvent(id, aggregateID string, eventType events.Type, payload any) (OutboxEvent, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return OutboxEvent{}, err
	}
	return OutboxEvent{
		ID:          id,
		AggregateID: aggregateID,
		Type:        eventType,
		Payload:     encoded,
		CreatedAt:   time.Now().UTC(),
	}, nil
}
