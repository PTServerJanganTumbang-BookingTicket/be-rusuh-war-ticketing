// Package redis is the QueueStore adapter on Redis (cluster mode in AWS).
package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

// Token hash fields (token:<queue_token>).
const (
	FieldTicketNo   = "ticket_no"
	FieldEventID    = "event_id"
	FieldAccountID  = "account_id"
	FieldDeviceHash = "device_hash"
	FieldJoinedAt   = "joined_at"
)

// queue:{ev} hash fields.
const (
	FieldNowServing   = "now_serving"
	FieldName         = "name"
	FieldSaleStartsAt = "sale_starts_at"
)

// Store implements outbound.QueueStore.
type Store struct{ rdb goredis.UniversalClient }

// NewStore wraps a client (*redis.ClusterClient in AWS, *redis.Client locally).
func NewStore(rdb goredis.UniversalClient) *Store { return &Store{rdb: rdb} }

// Snapshot reads the event's state in one pipelined round trip. All keys share
// the {ev} slot.
func (s *Store) Snapshot(ctx context.Context, ev rediskeys.Event) (domain.EventSnapshot, error) {
	var state, issued, soldOut *goredis.StringCmd
	var meta *goredis.SliceCmd
	_, err := s.rdb.Pipelined(ctx, func(p goredis.Pipeliner) error {
		state = p.Get(ctx, ev.QueueState())
		meta = p.HMGet(ctx, ev.QueueHash(), FieldNowServing, FieldName, FieldSaleStartsAt)
		issued = p.Get(ctx, ev.QueueIssued())
		soldOut = p.Get(ctx, ev.SoldOutAt())
		return nil
	})
	if err != nil && !errors.Is(err, goredis.Nil) {
		return domain.EventSnapshot{}, err
	}
	snap := domain.EventSnapshot{
		State:     domain.SaleState(optional(state)),
		SoldOutAt: optional(soldOut),
	}
	if snap.Issued, err = optionalInt(optional(issued)); err != nil {
		return domain.EventSnapshot{}, fmt.Errorf("%s: %w", ev.QueueIssued(), err)
	}
	vals, err := meta.Result()
	if err != nil {
		return domain.EventSnapshot{}, err
	}
	str := func(i int) string {
		if v, ok := vals[i].(string); ok {
			return v
		}
		return ""
	}
	if snap.NowServing, err = optionalInt(str(0)); err != nil {
		return domain.EventSnapshot{}, fmt.Errorf("%s now_serving: %w", ev.QueueHash(), err)
	}
	snap.Name, snap.SaleStartsAt = str(1), str(2)
	return snap, nil
}

func optional(c *goredis.StringCmd) string {
	v, err := c.Result()
	if err != nil {
		return ""
	}
	return v
}

func optionalInt(v string) (int64, error) {
	if v == "" {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

// Engagement reads token:<queueToken>.
func (s *Store) Engagement(ctx context.Context, queueToken string) (domain.Engagement, bool, error) {
	m, err := s.rdb.HGetAll(ctx, rediskeys.Token(queueToken)).Result()
	if err != nil {
		return domain.Engagement{}, false, err
	}
	if len(m) == 0 {
		return domain.Engagement{}, false, nil
	}
	ticketNo, err := strconv.ParseInt(m[FieldTicketNo], 10, 64)
	if err != nil {
		return domain.Engagement{}, false, fmt.Errorf("token hash ticket_no: %w", err)
	}
	joinedAt, _ := time.Parse(time.RFC3339, m[FieldJoinedAt])
	return domain.Engagement{
		QueueToken: queueToken,
		EventID:    m[FieldEventID],
		TicketNo:   ticketNo,
		AccountID:  m[FieldAccountID],
		DeviceHash: m[FieldDeviceHash],
		JoinedAt:   joinedAt,
	}, true, nil
}

// AccountToken reads acct:{ev}:<accountID>.
func (s *Store) AccountToken(ctx context.Context, ev rediskeys.Event, accountID string) (string, bool, error) {
	v, err := s.rdb.Get(ctx, ev.Acct(accountID)).Result()
	if errors.Is(err, goredis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

const casRetries = 5

// ClaimAccount sets acct:{ev}:<accountID> if unset (SET NX), or replaces the
// stale pointer `replace` (WATCH/MULTI compare-and-set).
func (s *Store) ClaimAccount(ctx context.Context, ev rediskeys.Event, accountID, queueToken, replace string, ttl time.Duration) (bool, string, error) {
	key := ev.Acct(accountID)
	if replace == "" {
		ok, err := s.rdb.SetNX(ctx, key, queueToken, ttl).Result()
		if err != nil || ok {
			return ok, queueToken, err
		}
		cur, err := s.rdb.Get(ctx, key).Result()
		if errors.Is(err, goredis.Nil) {
			// Released between SETNX and GET: try once more.
			ok, err = s.rdb.SetNX(ctx, key, queueToken, ttl).Result()
			return ok, queueToken, err
		}
		return false, cur, err
	}
	var claimed bool
	current := ""
	for range casRetries {
		err := s.rdb.Watch(ctx, func(tx *goredis.Tx) error {
			cur, err := tx.Get(ctx, key).Result()
			if err != nil && !errors.Is(err, goredis.Nil) {
				return err
			}
			if err == nil && cur != replace {
				current, claimed = cur, false
				return nil
			}
			_, err = tx.TxPipelined(ctx, func(p goredis.Pipeliner) error {
				p.Set(ctx, key, queueToken, ttl)
				return nil
			})
			if err == nil {
				current, claimed = queueToken, true
			}
			return err
		}, key)
		if errors.Is(err, goredis.TxFailedErr) {
			continue
		}
		return claimed, current, err
	}
	return false, "", fmt.Errorf("%s: compare-and-set kept losing", key)
}

// ReleaseAccount deletes acct:{ev}:<accountID> only if it still holds queueToken.
func (s *Store) ReleaseAccount(ctx context.Context, ev rediskeys.Event, accountID, queueToken string) error {
	key := ev.Acct(accountID)
	return s.rdb.Watch(ctx, func(tx *goredis.Tx) error {
		cur, err := tx.Get(ctx, key).Result()
		if errors.Is(err, goredis.Nil) || (err == nil && cur != queueToken) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(p goredis.Pipeliner) error {
			p.Del(ctx, key)
			return nil
		})
		return err
	}, key)
}

// IssueTicket is INCR queue:{ev}:issued.
func (s *Store) IssueTicket(ctx context.Context, ev rediskeys.Event) (int64, error) {
	return s.rdb.Incr(ctx, ev.QueueIssued()).Result()
}

// SaveEngagement writes token:<qt> and its TTL atomically (one key).
func (s *Store) SaveEngagement(ctx context.Context, e domain.Engagement, ttl time.Duration) error {
	fields := map[string]any{
		FieldTicketNo: e.TicketNo,
		FieldEventID:  e.EventID,
		FieldJoinedAt: e.JoinedAt.UTC().Format(time.RFC3339),
	}
	if e.AccountID != "" {
		fields[FieldAccountID] = e.AccountID
	}
	if e.DeviceHash != "" {
		fields[FieldDeviceHash] = e.DeviceHash
	}
	key := rediskeys.Token(e.QueueToken)
	_, err := s.rdb.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.HSet(ctx, key, fields)
		p.Expire(ctx, key, ttl)
		return nil
	})
	return err
}

// AddToLine is ZADD NX queue:{ev}:by_ticket <ticketNo> <qt>. NX keeps an
// existing score: a repair never moves anyone.
func (s *Store) AddToLine(ctx context.Context, ev rediskeys.Event, queueToken string, ticketNo int64) error {
	return s.rdb.ZAddNX(ctx, ev.ByTicket(), goredis.Z{Score: float64(ticketNo), Member: queueToken}).Err()
}

// InLine reports whether queueToken is in queue:{ev}:by_ticket.
func (s *Store) InLine(ctx context.Context, ev rediskeys.Event, queueToken string) (bool, error) {
	err := s.rdb.ZScore(ctx, ev.ByTicket(), queueToken).Err()
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}
	return err == nil, err
}
