package inbound

import "context"

// Result is what one drain pass did, for logging and metrics.
type Result struct {
	Claimed   int
	Published int
	Skipped   int
	Failed    int
	Remaining int
}

// IPublisherUsecase is the inbound port for draining the outbox.
type IPublisherUsecase interface {
	// Drain makes one pass over the unpublished rows.
	Drain(ctx context.Context) (Result, error)
}
