package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
)

// IMethodSwitches reads the operator kill switch.
//
// One method, and it returns the whole set rather than answering per method:
// §5.1 needs every switch to build its list, and one read that is cached is
// cheaper than a question per row on an endpoint every customer hits.
type IMethodSwitches interface {
	// Disabled returns the methods and banks currently switched off.
	//
	// An empty set with a nil error means nothing is switched off. An error
	// means the state could not be read — callers treat that as "nothing is
	// switched off" and say so in the log, because a kill switch that hides
	// every payment method when its own storage hiccups is a worse outage
	// than the one it was built for.
	Disabled(ctx context.Context) (entity.Disabled, error)
}
