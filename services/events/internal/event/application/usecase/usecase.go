// Package usecase holds the two read-only use cases of the events service.
package usecase

import (
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/port/outbound"
)

// Deps are the outside world of both use cases.
type Deps struct {
	Events  outbound.Events
	Live    outbound.Live
	Metrics outbound.Metrics
	Clock   func() time.Time
}

// Config is the part of the service config the use cases need.
type Config struct {
	QueueCapacity  int64 // QUEUE_CAPACITY, the value join enforces
	CaptchaSiteKey string
}

// queueState derives the client state from a Redis read. A Redis error is not fatal: the state comes from
// sale_starts_at.
func queueState(d Deps, cfg Config, ev domain.Event, q domain.Queue, redisOK bool) domain.QueueState {
	if !redisOK {
		return domain.ScheduleState(ev.SaleStartsAt, d.Clock())
	}
	if q.State != "" && !domain.Known(q.State) {
		d.Metrics.Incr(domain.MetricUnknownState)
	}
	return domain.DeriveQueueState(q.State, q.Depth, cfg.QueueCapacity)
}
