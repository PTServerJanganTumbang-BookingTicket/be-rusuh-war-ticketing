package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// UseEvents makes the tick find its events in Redis instead of using Config.Events. The list is refreshed at most
// every `every`: finding events scans the keyspace, which is too slow for every 500 ms tick.
func (t *Tick) UseEvents(src outbound.Events, every time.Duration) {
	t.source, t.refreshEvery = src, every
}

// currentEvents returns the events to tick now, refreshing the list when it is due. A failed refresh keeps the
// previous list and is retried on the next tick, so a Redis blip never stops a running sale.
func (t *Tick) currentEvents(ctx context.Context) []domain.Event {
	if t.source == nil {
		return t.cfg.Events
	}
	now := t.clock.Now()
	if t.discovered && now.Sub(t.lastRefresh) < t.refreshEvery {
		return t.found
	}
	events, err := t.source.Discover(ctx)
	if err != nil {
		t.metrics.Inc(MetricEventDiscoveryFailures)
		slog.Warn("event discovery failed; keeping the previous list", slog.Int("events", len(t.found)), slog.Any("err", err))
		return t.found
	}
	t.found, t.lastRefresh, t.discovered = events, now, true
	return t.found
}
