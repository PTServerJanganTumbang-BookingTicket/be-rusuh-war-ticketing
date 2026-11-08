// Package redis is the QueueStore adapter on Redis (cluster mode in AWS).
package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

	// Written by admission-ctl when it grants the place (FR-05 wait).
	FieldGrantToken       = "grant_token"
	FieldBookingExpiresAt = "booking_expires_at"
)

// queue:{ev} hash fields: the names come from pkg/rediskeys.
const (
	FieldState        = rediskeys.FieldState
	FieldIssued       = rediskeys.FieldIssued
	FieldFullBookedAt = rediskeys.FieldFullBookedAt
	FieldNowServing   = rediskeys.FieldNowServing
	FieldFreeSlots    = rediskeys.FieldFreeSlots
	FieldFreeSlotsAt  = rediskeys.FieldFreeSlotsAt
	FieldName         = rediskeys.FieldName
	FieldSaleStartsAt = rediskeys.FieldSaleStartsAt
)

// Positions in the Snapshot HMGET. The field list is built from this table, so
// reordering or adding a field cannot shift the others.
const (
	snNowServing = iota
	snName
	snSaleStartsAt
	snFreeSlots
	snFreeSlotsAt
	snState
	snIssued
	snFullBookedAt
	snCount
)

var snapshotFields = [snCount]string{
	snNowServing: FieldNowServing, snName: FieldName, snSaleStartsAt: FieldSaleStartsAt,
	snFreeSlots: FieldFreeSlots, snFreeSlotsAt: FieldFreeSlotsAt, snState: FieldState,
	snIssued: FieldIssued, snFullBookedAt: FieldFullBookedAt,
}

// Store implements outbound.QueueStore.
type Store struct{ rdb goredis.UniversalClient }

// NewStore wraps a client (*redis.ClusterClient in AWS, *redis.Client locally).
func NewStore(rdb goredis.UniversalClient) *Store { return &Store{rdb: rdb} }

// Snapshot reads the event's state with one HMGET of queue:{ev}. A missing
// field reads as empty; any other error fails the snapshot (fail closed: a
// hidden error could open the capacity gate).
func (s *Store) Snapshot(ctx context.Context, ev rediskeys.Event) (domain.EventSnapshot, error) {
	// One HMGET: the whole event lives in one hash (ADR-006 §8.12). A missing
	// field reads as empty; any other error fails the snapshot (fail closed).
	vals, err := s.rdb.HMGet(ctx, ev.QueueHash(), snapshotFields[:]...).Result()
	if err != nil {
		return domain.EventSnapshot{}, err
	}
	var snap domain.EventSnapshot
	str := func(i int) string {
		if v, ok := vals[i].(string); ok {
			return v
		}
		return ""
	}
	if snap.NowServing, err = optionalInt(str(snNowServing)); err != nil {
		return domain.EventSnapshot{}, fmt.Errorf("%s now_serving: %w", ev.QueueHash(), err)
	}
	snap.Name, snap.SaleStartsAt = str(snName), str(snSaleStartsAt)
	snap.State = domain.SaleState(str(snState))
	if snap.Issued, err = optionalInt(str(snIssued)); err != nil {
		return domain.EventSnapshot{}, fmt.Errorf("%s issued: %w", ev.QueueHash(), err)
	}
	// full_booked_at is epoch seconds in Redis; the 409 shows ISO 8601. An
	// unreadable value only blanks that field of the error body, but is logged
	// so a writer using another format (the old key held RFC 3339) is noticed.
	if raw := str(snFullBookedAt); raw != "" {
		if at, err := optionalInt(raw); err == nil && at > 0 {
			snap.FullBookedAt = time.Unix(at, 0).UTC().Format(time.RFC3339)
		} else {
			slog.WarnContext(ctx, "queue: full_booked_at is not epoch seconds", slog.String("event_id", ev.ID()), slog.String("value", raw))
		}
	}
	// free_slots only lets join wait for a grant. A value that is not a number
	// reads as "not written": join then answers queued at once.
	if free, err := optionalInt(str(snFreeSlots)); err == nil {
		if at, err := optionalInt(str(snFreeSlotsAt)); err == nil {
			snap.FreeSlots, snap.FreeSlotsAt = free, at
		}
	}
	return snap, nil
}

// Grant reads the grant admission-ctl copied into token:<queueToken>.
func (s *Store) Grant(ctx context.Context, queueToken string) (domain.Grant, bool, error) {
	vals, err := s.rdb.HMGet(ctx, rediskeys.Token(queueToken), FieldGrantToken, FieldBookingExpiresAt).Result()
	if err != nil {
		return domain.Grant{}, false, err
	}
	gt, _ := vals[0].(string)
	if gt == "" {
		return domain.Grant{}, false, nil
	}
	exp, _ := vals[1].(string)
	if exp == "" {
		return domain.Grant{}, false, nil // not fully written yet: keep polling
	}
	at, err := time.Parse(time.RFC3339, exp)
	if err != nil {
		return domain.Grant{}, false, fmt.Errorf("%s booking_expires_at %q: %w", rediskeys.Token(queueToken), exp, err)
	}
	return domain.Grant{GrantToken: gt, BookingExpiresAt: at}, true, nil
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
	// No ticket_no = not a place join wrote. admission-ctl only adds grant
	// fields to an existing hash, but a stray one must not turn a rejoin into a 503.
	if len(m) == 0 || m[FieldTicketNo] == "" {
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
		for range casRetries {
			cur, err := s.rdb.Get(ctx, key).Result()
			if err == nil {
				return false, cur, nil
			}
			if !errors.Is(err, goredis.Nil) {
				return false, "", err
			}
			// Released between SETNX and GET: try again, and report the winner.
			if ok, err = s.rdb.SetNX(ctx, key, queueToken, ttl).Result(); err != nil || ok {
				return ok, queueToken, err
			}
		}
		return false, "", fmt.Errorf("%s: claim kept changing hands", key)
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

// IssueTicket is HINCRBY queue:{ev} issued 1.
func (s *Store) IssueTicket(ctx context.Context, ev rediskeys.Event) (int64, error) {
	return s.rdb.HIncrBy(ctx, ev.QueueHash(), FieldIssued, 1).Result()
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

// RepairLine is ZADD NX by_ticket, done only while ticketNo > now_serving.
// WATCH on queue:{ev} makes it atomic with admission-ctl's advance (same slot).
func (s *Store) RepairLine(ctx context.Context, ev rediskeys.Event, queueToken string, ticketNo int64) (bool, error) {
	for range casRetries {
		added := false
		err := s.rdb.Watch(ctx, func(tx *goredis.Tx) error {
			ns, err := tx.HGet(ctx, ev.QueueHash(), FieldNowServing).Int64()
			if err != nil && !errors.Is(err, goredis.Nil) {
				return err
			}
			if ticketNo <= ns {
				return nil
			}
			var zadd *goredis.IntCmd
			if _, err := tx.TxPipelined(ctx, func(p goredis.Pipeliner) error {
				zadd = p.ZAddNX(ctx, ev.ByTicket(), goredis.Z{Score: float64(ticketNo), Member: queueToken})
				return nil
			}); err != nil {
				return err
			}
			added = zadd.Val() == 1
			return nil
		}, ev.QueueHash())
		if errors.Is(err, goredis.TxFailedErr) {
			continue
		}
		return added, err
	}
	return false, fmt.Errorf("%s: repair kept racing the advance", ev.ByTicket())
}
