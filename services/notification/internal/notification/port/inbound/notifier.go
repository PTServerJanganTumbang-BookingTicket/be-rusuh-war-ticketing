package inbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// INotifierUsecase is the inbound port for delivering one queued event.
type INotifierUsecase interface {
	// Deliver renders and sends the email for one envelope.
	//
	// Returning an error means the message was not delivered and must be
	// retried, so the caller leaves it on the queue. Returning nil means it is
	// done — delivered, a duplicate, or unroutable with no prospect of ever
	// succeeding.
	Deliver(ctx context.Context, envelope events.Envelope) error
}
