// Package redis is booking's Redis adapter.
package redis

import (
	"context"
	"fmt"
	"strconv"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

// Stock implements outbound.Stock.
type Stock struct{ rdb goredis.UniversalClient }

// NewStock wraps the client.
func NewStock(rdb goredis.UniversalClient) *Stock { return &Stock{rdb: rdb} }

// Levels reads every category's stock key in one MGET. The keys share the {ev} hash tag, so they sit on one
// cluster shard. A missing key is reported, never read as 0; a value that is not a number is an error.
func (s *Stock) Levels(ctx context.Context, eventID string, categoryIDs []string) (map[string]int64, []string, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return nil, nil, err
	}
	levels := make(map[string]int64, len(categoryIDs))
	if len(categoryIDs) == 0 {
		return levels, nil, nil
	}
	keys := make([]string, len(categoryIDs))
	for i, c := range categoryIDs {
		keys[i] = ev.Stock(c)
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("read stock: %w", err)
	}
	var missing []string
	for i, v := range vals {
		str, ok := v.(string)
		if !ok {
			missing = append(missing, categoryIDs[i])
			continue
		}
		n, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("stock %s is %q, not a number: %w", keys[i], str, err)
		}
		levels[categoryIDs[i]] = n
	}
	return levels, missing, nil
}
