package redis

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// Repair implements outbound.StockRepair: the Redis side of stock reconciliation
// (docs/05-booking/04-stock-reconciliation.md). It writes only stock counters and NIK locks, and never deletes a key.
type Repair struct{ rdb goredis.UniversalClient }

var _ outbound.StockRepair = (*Repair)(nil)

// NewRepair wraps the client.
func NewRepair(rdb goredis.UniversalClient) *Repair { return &Repair{rdb: rdb} }

// Levels reads every category's stock key in one MGET (one shard, the {ev} tag). A key that is gone, or holds anything but a
// plain integer, is reported as missing: the hold script treats both as STOCK_UNINITIALIZED.
func (r *Repair) Levels(ctx context.Context, eventID string, categories []string) (map[string]int64, []string, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return nil, nil, err
	}
	levels := make(map[string]int64, len(categories))
	if len(categories) == 0 {
		return levels, nil, nil
	}
	keys := make([]string, len(categories))
	for i, c := range categories {
		keys[i] = ev.Stock(c)
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("read stock: %w", err)
	}
	var missing []string
	for i, v := range vals {
		s, ok := v.(string)
		n, perr := strconv.ParseInt(s, 10, 64)
		if !ok || perr != nil || strconv.FormatInt(n, 10) != s {
			missing = append(missing, categories[i])
			continue
		}
		levels[categories[i]] = n
	}
	return levels, missing, nil
}

// Restore puts a stock key back: a key that is gone is set with SET NX (never over one that came back in the meantime), and
// a key that holds anything but a plain integer is overwritten. The latter matters: the hold script treats it as
// STOCK_UNINITIALIZED, but the key exists, so SET NX alone would leave the category dead without a word. A readable value,
// even a low one, is never overwritten. Between the read of an unreadable value and its overwrite nothing else writes it
// (a hold's DECRBY on a non-integer fails), so the overwrite is safe without a script.
func (r *Repair) Restore(ctx context.Context, eventID, category string, value int64) (bool, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return false, err
	}
	key := ev.Stock(category)
	set, err := r.rdb.SetNX(ctx, key, value, 0).Result()
	if err != nil || set {
		return set, err
	}
	cur, err := r.rdb.Get(ctx, key).Result()
	if err == goredis.Nil { // it vanished between the two calls
		return r.rdb.SetNX(ctx, key, value, 0).Result()
	}
	if err != nil {
		return false, err
	}
	if n, perr := strconv.ParseInt(cur, 10, 64); perr == nil && strconv.FormatInt(n, 10) == cur {
		return false, nil // a readable value: leave it to the drift rules
	}
	return r.rdb.SetXX(ctx, key, value, 0).Result()
}

// Adjust runs adjust_if_unchanged.lua: delta is added only if the key still holds observed, and never below zero.
func (r *Repair) Adjust(ctx context.Context, eventID, category string, observed, delta int64) (domain.AdjustResult, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return domain.AdjustResult{}, err
	}
	res, err := redisscript.AdjustIfUnchanged(ctx, r.rdb, ev, category, observed, delta)
	if err != nil {
		return domain.AdjustResult{}, err
	}
	return domain.AdjustResult{Code: string(res.Code), Value: res.Value}, nil
}

// MissingLocks returns the hashes whose nik:used key does not exist, with one pipelined EXISTS per hash (all on the event's
// shard).
func (r *Repair) MissingLocks(ctx context.Context, eventID string, hmacs []string) ([]string, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return nil, err
	}
	if len(hmacs) == 0 {
		return nil, nil
	}
	pipe := r.rdb.Pipeline()
	cmds := make([]*goredis.IntCmd, len(hmacs))
	for i, h := range hmacs {
		cmds[i] = pipe.Exists(ctx, ev.NIKUsed(h))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("check NIK locks: %w", err)
	}
	var missing []string
	for i, c := range cmds {
		if c.Val() == 0 {
			missing = append(missing, hmacs[i])
		}
	}
	return missing, nil
}

// RestoreLock sets nik:used:{ev}:<hmac> with an expiry, only if it is absent: a lock that is alive keeps its own TTL, and a
// lock without an expiry would leak for ever.
func (r *Repair) RestoreLock(ctx context.Context, eventID, hmac string, ttl time.Duration) (bool, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return false, err
	}
	return r.rdb.SetNX(ctx, ev.NIKUsed(hmac), "1", ttl).Result()
}

// CountOrphans counts the event's nik:used keys whose hash is not in claimed. It deletes nothing: a lock with no claim may
// belong to a hold that has not committed yet, and it dies on its own TTL.
func (r *Repair) CountOrphans(ctx context.Context, eventID string, claimed map[string]struct{}) (int, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return 0, err
	}
	prefix := ev.NIKUsed("")
	keys, err := scanKeysOf(ctx, r.rdb, prefix, escapeGlob(prefix)+"*")
	if err != nil {
		return 0, fmt.Errorf("scan NIK locks: %w", err)
	}
	orphans := 0
	for _, k := range keys {
		h, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		if _, ok := claimed[h]; !ok {
			orphans++
		}
	}
	return orphans, nil
}

// escapeGlob escapes the characters that SCAN's MATCH treats as a pattern, so an event id is matched literally.
func escapeGlob(s string) string {
	var b strings.Builder
	for _, c := range s {
		if strings.ContainsRune(`*?[]\`, c) {
			b.WriteRune('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}
