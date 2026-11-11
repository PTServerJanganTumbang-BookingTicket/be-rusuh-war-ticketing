package usecase

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

// ListInput is the validated query of GET /events.
type ListInput struct {
	Limit  int
	Offset int
}

// Card is one entry of the list.
type Card struct {
	Event      domain.Event
	QueueState domain.QueueState
}

// ListResult is the page plus the facts the handler needs for pagination and caching.
type ListResult struct {
	Cards    []Card
	Total    int  // all events, not only this page
	Degraded bool // Redis could not be read: states come from sale_starts_at
}

// List is the GET /events use case.
type List struct {
	d   Deps
	cfg Config
}

// NewList builds the use case.
func NewList(d Deps, cfg Config) *List { return &List{d: d, cfg: cfg} }

// Do reads one page of events from Aurora, then asks Redis for the state of those events only.
func (l *List) Do(ctx context.Context, in ListInput) (ListResult, error) {
	events, total, err := l.d.Events.List(ctx, in.Limit, in.Offset)
	if err != nil {
		slog.ErrorContext(ctx, "list events: aurora failed", slog.Any("err", err))
		return ListResult{}, apperror.New(http.StatusServiceUnavailable, "50300", "Layanan tidak tersedia").WithCode("INTERNAL_ERROR")
	}
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	queues, rerr := l.d.Live.Queues(ctx, ids)
	if rerr != nil {
		slog.WarnContext(ctx, "list events: redis failed, using sale_starts_at", slog.Any("err", rerr))
		l.d.Metrics.Incr(domain.MetricRedisFallback)
	}
	res := ListResult{Total: total, Degraded: rerr != nil}
	for _, e := range events {
		res.Cards = append(res.Cards, Card{Event: e, QueueState: queueState(l.d, l.cfg, e, queues[e.ID], rerr == nil)})
	}
	return res, nil
}
