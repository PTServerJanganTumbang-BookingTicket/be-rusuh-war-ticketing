package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

// servingRetryAfter is the Retry-After of a 503: the client polls about every second anyway.
const servingRetryAfter = time.Second

// ServingResult is the answer of GET /queue/serving (api-contracts §3.4). It is the same for every caller.
type ServingResult struct {
	domain.Serving
	ServerTime time.Time
	PollAfter  time.Duration
	PollTiers  []domain.PollTier
}

// GetServing is the GET /queue/serving use case: the polling fallback of the SSE stream. It reads Redis only.
type GetServing struct {
	store outbound.ServingReader
	clock func() time.Time
}

// NewGetServing builds the use case. A nil clock is time.Now.
func NewGetServing(store outbound.ServingReader, clock func() time.Time) *GetServing {
	if clock == nil {
		clock = time.Now
	}
	return &GetServing{store: store, clock: clock}
}

// Get answers every seeded event with 200 data, whatever its state: polling is how the client learns the state.
// An unknown or unseeded event is NOT_FOUND; a Redis error is 503.
func (g *GetServing) Get(ctx context.Context, eventID string) (ServingResult, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return ServingResult{}, errEventNotFound()
	}
	s, found, err := g.store.Serving(ctx, ev)
	if err != nil {
		slog.ErrorContext(ctx, "serving: read failed", slog.String("event_id", eventID), slog.Any("err", err))
		return ServingResult{}, errUnavailable().WithRetryAfter(servingRetryAfter)
	}
	if !found {
		return ServingResult{}, errEventNotFound()
	}
	if !s.State.Known() { // a data fault, not a missing event: tell the client to retry
		slog.ErrorContext(ctx, "serving: unknown sale state", slog.String("event_id", eventID), slog.String("state", string(s.State)))
		return ServingResult{}, errUnavailable().WithRetryAfter(servingRetryAfter)
	}
	return ServingResult{
		Serving:    s,
		ServerTime: g.clock(),
		PollAfter:  domain.DefaultPollAfter,
		PollTiers:  domain.PollTiers(),
	}, nil
}

func errEventNotFound() *apperror.AppError {
	return apperror.ErrNotFound("Event tidak ditemukan").WithCode(domain.CodeNotFound)
}
