// Package redis is the admission-ctl Queue adapter on the Redis cluster.
package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// Queue talks to Redis. All keys come from pkg/rediskeys.
type Queue struct {
	rdb     goredis.UniversalClient
	advance *redisscript.Script
}

// NewQueue builds the adapter on a Redis (cluster) client.
func NewQueue(rdb goredis.UniversalClient) *Queue {
	return &Queue{rdb: rdb, advance: redisscript.New(rdb)}
}

func event(id string) (rediskeys.Event, error) { return rediskeys.NewEvent(id) }

// Snapshot reads now_serving and issued with one HMGET; a missing field is 0.
func (q *Queue) Snapshot(ctx context.Context, eventID string) (int64, int64, error) {
	ev, err := event(eventID)
	if err != nil {
		return 0, 0, err
	}
	vals, err := q.rdb.HMGet(ctx, ev.QueueHash(), rediskeys.FieldNowServing, rediskeys.FieldIssued).Result()
	if err != nil {
		return 0, 0, err
	}
	num := func(v any) int64 {
		s, _ := v.(string)
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	}
	return num(vals[0]), num(vals[1]), nil
}

// Pending lists the tokens with a ticket number <= upTo.
func (q *Queue) Pending(ctx context.Context, eventID string, upTo int64) ([]string, error) {
	ev, err := event(eventID)
	if err != nil {
		return nil, err
	}
	return q.rdb.ZRangeByScore(ctx, ev.ByTicket(), &goredis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(upTo, 10)}).Result()
}

// PendingCount counts the tokens with a ticket number <= upTo.
func (q *Queue) PendingCount(ctx context.Context, eventID string, upTo int64) (int64, error) {
	ev, err := event(eventID)
	if err != nil {
		return 0, err
	}
	return q.rdb.ZCount(ctx, ev.ByTicket(), "-inf", strconv.FormatInt(upTo, 10)).Result()
}

// Advance runs advance_now_serving.lua.
func (q *Queue) Advance(ctx context.Context, e domain.Event, a redisscript.Args) (redisscript.Result, error) {
	ev, err := event(e.ID)
	if err != nil {
		return redisscript.Result{}, err
	}
	return q.advance.Advance(ctx, ev, e.Categories, a)
}

// Token reads account_id and device_hash from token:<queue_token>. ticket_no is read only to tell
// "the hash is gone" from "a guest with no account": join always writes it.
func (q *Queue) Token(ctx context.Context, queueToken string) (domain.TokenInfo, bool, error) {
	vals, err := q.rdb.HMGet(ctx, rediskeys.Token(queueToken), rediskeys.FieldAccountID, rediskeys.FieldDeviceHash, rediskeys.FieldTicketNo).Result()
	if err != nil {
		return domain.TokenInfo{}, false, err
	}
	str := func(v any) string { s, _ := v.(string); return s }
	if vals[0] == nil && vals[1] == nil && vals[2] == nil {
		return domain.TokenInfo{}, false, nil
	}
	return domain.TokenInfo{AccountID: str(vals[0]), DeviceHash: str(vals[1])}, true, nil
}

// PutGrant writes grant:<grant_token> and its TTL in one MULTI, so it can never live without a TTL.
func (q *Queue) PutGrant(ctx context.Context, eventID, queueToken string, b domain.Booking, info domain.TokenInfo, ttl time.Duration) error {
	fields := map[string]any{"event_id": eventID, "booking_id": b.ID, "queue_token": queueToken}
	if info.AccountID != "" {
		fields["account_id"] = info.AccountID
	} else if info.DeviceHash != "" {
		fields["device_hash"] = info.DeviceHash
	}
	key := rediskeys.Grant(b.GrantToken)
	_, err := q.rdb.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.HSet(ctx, key, fields)
		p.Expire(ctx, key, ttl)
		return nil
	})
	return err
}

// AttachGrant copies the grant into token:<queue_token> only if that hash still exists.
func (q *Queue) AttachGrant(ctx context.Context, queueToken string, b domain.Booking) error {
	_, err := redisscript.HSetIfExists(ctx, q.rdb, rediskeys.Token(queueToken),
		rediskeys.FieldGrantToken, b.GrantToken,
		rediskeys.FieldBookingID, b.ID,
		rediskeys.FieldBookingExpiresAt, b.ExpiresAt.UTC().Format(time.RFC3339))
	return err
}

// Dequeue removes the token from by_ticket. A second ZREM is harmless.
func (q *Queue) Dequeue(ctx context.Context, eventID, queueToken string) error {
	ev, err := event(eventID)
	if err != nil {
		return err
	}
	return q.rdb.ZRem(ctx, ev.ByTicket(), queueToken).Err()
}

// View reads the sale state and when it became fully booked.
func (q *Queue) View(ctx context.Context, eventID string) (domain.EventView, error) {
	ev, err := event(eventID)
	if err != nil {
		return domain.EventView{}, err
	}
	vals, err := q.rdb.HMGet(ctx, ev.QueueHash(), rediskeys.FieldState, rediskeys.FieldFullBookedAt).Result()
	if err != nil {
		return domain.EventView{}, err
	}
	v := domain.EventView{}
	v.State, _ = vals[0].(string)
	if s, ok := vals[1].(string); ok {
		v.FullBookedAt, _ = strconv.ParseInt(s, 10, 64)
	}
	return v, nil
}

// SetRate stores the whole-number rate that /queue/me and the stream both read.
func (q *Queue) SetRate(ctx context.Context, eventID string, ratePerMin int64) error {
	ev, err := event(eventID)
	if err != nil {
		return err
	}
	return q.rdb.HSet(ctx, ev.QueueHash(), rediskeys.FieldRatePerMin, ratePerMin).Err()
}

// Close moves a fully booked sale to CLOSED for good, but only if it is still FULL_BOOKED since the same moment:
// a hold that expired and reopened the sale between the tick's read and this write must win. WATCH makes the
// check and the write one compare-and-set.
func (q *Queue) Close(ctx context.Context, eventID string, fullBookedAt int64) error {
	ev, err := event(eventID)
	if err != nil {
		return err
	}
	key := ev.QueueHash()
	for attempt := 0; attempt < 3; attempt++ {
		err = q.rdb.Watch(ctx, func(tx *goredis.Tx) error {
			vals, err := tx.HMGet(ctx, key, rediskeys.FieldState, rediskeys.FieldFullBookedAt).Result()
			if err != nil {
				return err
			}
			state, _ := vals[0].(string)
			at, _ := vals[1].(string)
			if state != "FULL_BOOKED" || at != strconv.FormatInt(fullBookedAt, 10) {
				return nil // reopened or already closed: leave it
			}
			_, err = tx.TxPipelined(ctx, func(p goredis.Pipeliner) error {
				p.HSet(ctx, key, rediskeys.FieldState, "CLOSED")
				p.HDel(ctx, key, rediskeys.FieldFullBookedAt)
				return nil
			})
			return err
		}, key)
		if !errors.Is(err, goredis.TxFailedErr) {
			return err
		}
	}
	return err
}

// DropGrant deletes the grant hash. Only the grant: the queue token and acct stay until the sale window ends.
func (q *Queue) DropGrant(ctx context.Context, grantToken string) error {
	return q.rdb.Del(ctx, rediskeys.Grant(grantToken)).Err()
}

// PublishBroadcast sends the numbers message to every sse-server.
func (q *Queue) PublishBroadcast(ctx context.Context, payload []byte) error {
	return q.rdb.Publish(ctx, rediskeys.ChannelBroadcast, payload).Err()
}

// PublishStatus sends a sale-state change to every sse-server.
func (q *Queue) PublishStatus(ctx context.Context, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("empty status payload")
	}
	return q.rdb.Publish(ctx, rediskeys.ChannelStatus, payload).Err()
}
