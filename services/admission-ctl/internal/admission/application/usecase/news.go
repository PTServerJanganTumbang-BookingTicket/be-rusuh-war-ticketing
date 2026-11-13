package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// News messages. Key order and names are part of the contract with sse-server (02-queue-status-stream.md).
type broadcastMsg struct {
	EventID    string `json:"eventId"`
	NowServing int64  `json:"nowServing"`
	State      string `json:"state"`
	RatePerMin int64  `json:"ratePerMin"`
	ServerTime int64  `json:"serverTime"`
}

type statusMsg struct {
	EventID      string `json:"eventId"`
	State        string `json:"state"`
	FullBookedAt *int64 `json:"fullBookedAt"`
	ServerTime   int64  `json:"serverTime"`
}

// news closes a fully booked sale when its time is up, measures the speed, and tells everyone what changed:
// a status message when the sale state changed, a broadcast when the number moved or the heartbeat is due.
// It runs after FINISH, so nobody hears a number before the bookings it lets in exist.
func (t *Tick) news(ctx context.Context, ev domain.Event, st *eventState, nowServing int64) error {
	now := t.clock.Now()
	view, err := t.queue.View(ctx, ev.ID)
	if err != nil {
		return fmt.Errorf("read sale state: %w", err)
	}
	if view.State == "FULL_BOOKED" && view.FullBookedAt > 0 && now.Sub(time.Unix(view.FullBookedAt, 0)) >= t.cfg.CloseAfter {
		if err := t.queue.Close(ctx, ev.ID, view.FullBookedAt); err != nil {
			return fmt.Errorf("close the sale: %w", err)
		}
		view = domain.EventView{State: "CLOSED"}
	}

	rate := st.rate.observe(now, nowServing, view.State == "OPEN")
	// An unseeded event (no state yet) has no hash to hang a rate on: HSET would create a half-made one.
	if view.State != "" && (!st.rateWritten || rate != st.writtenRate) {
		if err := t.queue.SetRate(ctx, ev.ID, rate); err != nil {
			return fmt.Errorf("write rate_per_min: %w", err)
		}
		st.writtenRate, st.rateWritten = rate, true
	}

	if view.State != "" && view.State != st.lastState {
		msg := statusMsg{EventID: ev.ID, State: view.State, ServerTime: now.Unix()}
		if view.State == "FULL_BOOKED" && view.FullBookedAt > 0 {
			msg.FullBookedAt = &view.FullBookedAt
		}
		if err := t.publish(ctx, msg, t.queue.PublishStatus); err != nil {
			return err
		}
		st.lastState = view.State
	}

	if !st.published || nowServing != st.lastNS || now.Sub(st.lastBroadcast) >= t.cfg.Heartbeat {
		msg := broadcastMsg{EventID: ev.ID, NowServing: nowServing, State: view.State, RatePerMin: rate, ServerTime: now.Unix()}
		if err := t.publish(ctx, msg, t.queue.PublishBroadcast); err != nil {
			return err
		}
		st.published, st.lastNS, st.lastBroadcast = true, nowServing, now
	}
	return nil
}

func (t *Tick) publish(ctx context.Context, msg any, send func(context.Context, []byte) error) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode news: %w", err)
	}
	if err := send(ctx, b); err != nil {
		slog.Warn("publish failed", slog.Any("err", err))
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}

// writeGauges reports where one event's queue stands as read by this tick's SNAPSHOT, at most once per Heartbeat:
// CloudWatch keeps one-minute points anyway, and writing them every 500 ms tick would only add log lines. Every gauge
// carries the event, so each sale has its own series; the all-events series EMF also fills mixes them and is not read
// for gauges (the dashboard sums the per-event series). Queue depth comes from the two counters, never from
// counting the sorted set (ZCARD). The rate is the one the last tick published.
func (t *Tick) writeGauges(eventID string, st *eventState, nowServing, issued int64) {
	now := t.clock.Now()
	if !st.gaugesAt.IsZero() && now.Sub(st.gaugesAt) < t.cfg.Heartbeat {
		return
	}
	t.metrics.GaugeEvent(GaugeQueueDepth, eventID, float64(max(issued-nowServing, 0)))
	t.metrics.GaugeEvent(GaugeNowServing, eventID, float64(nowServing))
	t.metrics.GaugeEvent(GaugeIssued, eventID, float64(issued))
	t.metrics.GaugeEvent(GaugeRatePerMin, eventID, float64(st.writtenRate))
	st.gaugesAt = now
}
