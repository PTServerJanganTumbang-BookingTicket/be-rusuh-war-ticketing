package usecase

import (
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/port/inbound"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/port/outbound"
)

// PublisherUsecase drains the outbox onto the broker.
type PublisherUsecase struct {
	repo      portout.IOutboxRepository
	lease     portout.ILeaseGuard
	publisher portout.IEventPublisher
	batchSize int
}

// NewPublisher wires the use case and returns the inbound port.
func NewPublisher(
	repo portout.IOutboxRepository,
	lease portout.ILeaseGuard,
	publisher portout.IEventPublisher,
	batchSize int,
) portin.IPublisherUsecase {
	return &PublisherUsecase{
		repo:      repo,
		lease:     lease,
		publisher: publisher,
		batchSize: batchSize,
	}
}
