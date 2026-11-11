// Package outbound lists what the events use cases need from the outside.
package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

// Events reads the catalog from the Aurora reader.
type Events interface {
	// List returns one page, newest event date first, then id, and the total number of events.
	List(ctx context.Context, limit, offset int) (events []domain.Event, total int, err error)
	// Get returns one event and its categories in sort order. ok is false when there is no such event.
	Get(ctx context.Context, id string) (ev domain.Event, cats []domain.Category, ok bool, err error)
}

// Live reads the live state from Redis. Any error makes the use case fall back to the Aurora snapshot.
type Live interface {
	// Queues reads queue:{ev} for every id in one round trip. An event without a hash is present with an empty State.
	Queues(ctx context.Context, eventIDs []string) (map[string]domain.Queue, error)
	// Stock reads the stock keys of an event in one MGET. A missing key is absent from the map.
	Stock(ctx context.Context, eventID string, categoryIDs []string) (map[string]int64, error)
}

// Metrics counts events worth an alarm.
type Metrics interface{ Incr(name string) }
