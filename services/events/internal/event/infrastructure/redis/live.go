// Package redis is the events service's Redis adapter. It only reads.
package redis

import (
	"context"
	"fmt"
	"strconv"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

// Live implements outbound.Live.
type Live struct{ rdb goredis.UniversalClient }

// NewLive wraps the client.
func NewLive(rdb goredis.UniversalClient) *Live { return &Live{rdb: rdb} }

// Queues reads state, issued and now_serving of every event's queue:{ev} hash in one pipeline.
func (l *Live) Queues(ctx context.Context, eventIDs []string) (map[string]domain.Queue, error) {
	out := make(map[string]domain.Queue, len(eventIDs))
	if len(eventIDs) == 0 {
		return out, nil
	}
	pipe := l.rdb.Pipeline()
	cmds := make([]*goredis.SliceCmd, len(eventIDs))
	for i, id := range eventIDs {
		ev, err := rediskeys.NewEvent(id)
		if err != nil {
			return nil, err
		}
		cmds[i] = pipe.HMGet(ctx, ev.QueueHash(), rediskeys.FieldState, rediskeys.FieldIssued, rediskeys.FieldNowServing)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("read queues: %w", err)
	}
	for i, id := range eventIDs {
		v, err := cmds[i].Result()
		if err != nil {
			return nil, fmt.Errorf("read queue %s: %w", id, err)
		}
		q := domain.Queue{}
		if s, ok := v[0].(string); ok {
			q.State = s
		}
		q.Depth = max(0, num(v[1])-num(v[2]))
		out[id] = q
	}
	return out, nil
}

// Stock reads the category stock keys in one MGET (one cluster slot). A missing or non-numeric key is left out.
func (l *Live) Stock(ctx context.Context, eventID string, categoryIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(categoryIDs))
	if len(categoryIDs) == 0 {
		return out, nil
	}
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(categoryIDs))
	for i, c := range categoryIDs {
		keys[i] = ev.Stock(c)
	}
	vals, err := l.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read stock: %w", err)
	}
	for i, v := range vals {
		if s, ok := v.(string); ok {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				out[categoryIDs[i]] = n
			}
		}
	}
	return out, nil
}

func num(v any) int64 {
	s, _ := v.(string)
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
