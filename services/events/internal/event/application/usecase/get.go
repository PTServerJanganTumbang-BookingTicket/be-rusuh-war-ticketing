package usecase

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

// CategoryView is a category with its live seat count.
type CategoryView struct {
	domain.Category
	Status string // AVAILABLE | SOLD_OUT
}

// Detail is the GET /events/{event_id} answer.
type Detail struct {
	Event          domain.Event
	QueueState     domain.QueueState
	Categories     []CategoryView
	CaptchaSiteKey string
	Degraded       bool // Redis could not be read
}

// Get is the GET /events/{event_id} use case.
type Get struct {
	d   Deps
	cfg Config
}

// NewGet builds the use case.
func NewGet(d Deps, cfg Config) *Get { return &Get{d: d, cfg: cfg} }

// Do returns one event. An id that cannot be a Redis key is a 404 without any I/O.
func (g *Get) Do(ctx context.Context, id string) (Detail, error) {
	if _, err := rediskeys.NewEvent(id); err != nil {
		return Detail{}, apperror.ErrNotFound("Event tidak ditemukan").WithCode(domain.CodeNotFound)
	}
	ev, cats, ok, err := g.d.Events.Get(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "get event: aurora failed", slog.Any("err", err))
		return Detail{}, apperror.New(http.StatusServiceUnavailable, "50300", "Layanan tidak tersedia").WithCode("INTERNAL_ERROR")
	}
	if !ok {
		return Detail{}, apperror.ErrNotFound("Event tidak ditemukan").WithCode(domain.CodeNotFound)
	}

	catIDs := make([]string, len(cats))
	for i, c := range cats {
		catIDs[i] = c.ID
	}
	queues, qerr := g.d.Live.Queues(ctx, []string{id})
	stock, serr := g.d.Live.Stock(ctx, id, catIDs)
	degraded := qerr != nil || serr != nil
	if degraded {
		slog.WarnContext(ctx, "get event: redis failed, using the snapshot", slog.Any("queue_err", qerr), slog.Any("stock_err", serr))
		g.d.Metrics.Incr(domain.MetricRedisFallback)
	}

	out := Detail{Event: ev, CaptchaSiteKey: g.cfg.CaptchaSiteKey, Degraded: degraded}
	out.QueueState = queueState(g.d, g.cfg, ev, queues[id], qerr == nil)
	for _, c := range cats {
		if serr == nil {
			if v, found := stock[c.ID]; found { // a missing key falls back to the snapshot for that category only
				c.Available = max(0, v)
			}
		}
		status := "AVAILABLE"
		if c.Available <= 0 {
			status = "SOLD_OUT"
		}
		out.Categories = append(out.Categories, CategoryView{Category: c, Status: status})
	}
	return out, nil
}
