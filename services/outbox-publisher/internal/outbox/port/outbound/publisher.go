package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// IEventPublisher hands an envelope to the broker.
type IEventPublisher interface {
	// Publish sends one envelope. Returning an error leaves the row
	// unpublished so a later pass retries it.
	Publish(ctx context.Context, envelope events.Envelope) error
}
