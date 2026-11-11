package redis

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// scanCount is how many keys one SCAN call asks for. Small enough not to block a busy shard.
const scanCount = 1000

// EventSource finds the sales to tick by scanning Redis: every hash queue:{ev} that has a state and is not CLOSED
// is a sale, and its stock categories are the stock:{ev}:<category> keys. It is slow on a big keyspace (the scan
// walks every key), so the use case calls it every 30 s, not every tick.
type EventSource struct{ rdb goredis.UniversalClient }

// NewEventSource builds the source on a Redis (cluster) client.
func NewEventSource(rdb goredis.UniversalClient) *EventSource { return &EventSource{rdb: rdb} }

// Discover returns the open sales, sorted by id, each with its sorted stock categories.
func (s *EventSource) Discover(ctx context.Context) ([]domain.Event, error) {
	queueKeys, err := s.scan(ctx, "queue:{*}")
	if err != nil {
		return nil, fmt.Errorf("scan queue hashes: %w", err)
	}
	stockKeys, err := s.scan(ctx, "stock:{*}:*")
	if err != nil {
		return nil, fmt.Errorf("scan stock keys: %w", err)
	}
	categories := map[string][]string{}
	for _, k := range stockKeys {
		if id, cat, ok := splitStockKey(k); ok {
			categories[id] = append(categories[id], cat)
		}
	}
	ids := map[string]bool{}
	for _, k := range queueKeys {
		if id, ok := eventOfQueueKey(k); ok {
			ids[id] = true
		}
	}
	var events []domain.Event
	for id := range ids {
		ev, err := rediskeys.NewEvent(id)
		if err != nil {
			continue
		}
		state, err := s.rdb.HGet(ctx, ev.QueueHash(), rediskeys.FieldState).Result()
		if err != nil || state == "" || state == "CLOSED" {
			continue // no state: a stray write, not a sale. CLOSED: finished, costs nothing
		}
		cats := categories[id]
		sort.Strings(cats)
		events = append(events, domain.Event{ID: id, Categories: cats})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
	return events, nil
}

// scan lists the keys matching pattern on every master (a cluster client) or on the one node.
func (s *EventSource) scan(ctx context.Context, pattern string) ([]string, error) {
	return scanKeys(ctx, s.rdb, pattern)
}

// scanKeys lists the keys matching pattern on every master (a cluster client) or on the one node. A cluster client's own
// SCAN would visit one node only.
func scanKeys(ctx context.Context, rdb goredis.UniversalClient, pattern string) ([]string, error) {
	var (
		mu   sync.Mutex
		keys []string
	)
	one := func(ctx context.Context, c *goredis.Client) error {
		local, err := scanNode(ctx, c, pattern)
		if err != nil {
			return err
		}
		mu.Lock()
		keys = append(keys, local...)
		mu.Unlock()
		return nil
	}
	switch c := rdb.(type) {
	case *goredis.ClusterClient:
		if err := c.ForEachMaster(ctx, one); err != nil {
			return nil, err
		}
	case *goredis.Client:
		if err := one(ctx, c); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("key scan does not support %T", rdb)
	}
	return keys, nil
}

// scanKeysOf lists the keys matching pattern on the one node that owns ownerKey's slot. Every key of an event carries the
// {ev} hash tag, so they all live on one node, and scanning only that node avoids walking every master's keyspace.
func scanKeysOf(ctx context.Context, rdb goredis.UniversalClient, ownerKey, pattern string) ([]string, error) {
	switch c := rdb.(type) {
	case *goredis.ClusterClient:
		node, err := c.MasterForKey(ctx, ownerKey)
		if err != nil {
			return nil, err
		}
		return scanNode(ctx, node, pattern)
	case *goredis.Client:
		return scanNode(ctx, c, pattern)
	default:
		return nil, fmt.Errorf("key scan does not support %T", rdb)
	}
}

// scanNode lists the keys matching pattern on one node.
func scanNode(ctx context.Context, c *goredis.Client, pattern string) ([]string, error) {
	var keys []string
	it := c.Scan(ctx, 0, pattern, scanCount).Iterator()
	for it.Next(ctx) {
		keys = append(keys, it.Val())
	}
	return keys, it.Err()
}

// eventOfQueueKey reads the id out of "queue:{id}". It rejects "queue:{id}:by_ticket" and anything else.
func eventOfQueueKey(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "queue:{")
	if !ok || !strings.HasSuffix(rest, "}") {
		return "", false
	}
	id := strings.TrimSuffix(rest, "}")
	return id, id != "" && !strings.ContainsAny(id, "{}")
}

// splitStockKey reads "stock:{id}:category".
func splitStockKey(key string) (id, category string, ok bool) {
	rest, ok := strings.CutPrefix(key, "stock:{")
	if !ok {
		return "", "", false
	}
	id, category, ok = strings.Cut(rest, "}:")
	return id, category, ok && id != "" && category != ""
}
