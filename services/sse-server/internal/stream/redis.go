package stream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

// RedisStore reads the gates and the snapshot from Redis.
type RedisStore struct{ rdb goredis.UniversalClient }

// NewRedisStore wraps a Redis client.
func NewRedisStore(rdb goredis.UniversalClient) *RedisStore { return &RedisStore{rdb: rdb} }

// State implements Store.
func (s *RedisStore) State(ctx context.Context, ev rediskeys.Event) (string, bool, error) {
	v, err := s.rdb.Get(ctx, ev.QueueState()).Result()
	if errors.Is(err, goredis.Nil) {
		return "", false, nil
	}
	return v, err == nil, err
}

// Numbers implements Store.
func (s *RedisStore) Numbers(ctx context.Context, ev rediskeys.Event) (int64, int64, error) {
	vals, err := s.rdb.HMGet(ctx, ev.QueueHash(), "now_serving", "rate_per_min").Result()
	if err != nil {
		return 0, 0, err
	}
	n := func(i int) (int64, error) {
		str, ok := vals[i].(string)
		if !ok || str == "" {
			return 0, nil
		}
		v, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s field %d: %w", ev.QueueHash(), i, err)
		}
		return v, nil
	}
	ns, err := n(0)
	if err != nil {
		return 0, 0, err
	}
	rate, err := n(1)
	return ns, rate, err
}

// TokenEvent implements Store.
func (s *RedisStore) TokenEvent(ctx context.Context, queueToken string) (string, bool, error) {
	v, err := s.rdb.HGet(ctx, rediskeys.Token(queueToken), "event_id").Result()
	if errors.Is(err, goredis.Nil) {
		return "", false, nil
	}
	return v, err == nil, err
}

// pingEvery is how long the subscription may stay silent before it is probed.
const pingEvery = 5 * time.Second

// RedisSource subscribes with classic pub/sub on both channels.
type RedisSource struct{ rdb goredis.UniversalClient }

// NewRedisSource wraps a Redis client.
func NewRedisSource(rdb goredis.UniversalClient) *RedisSource { return &RedisSource{rdb: rdb} }

type redisSub struct {
	ps *goredis.PubSub
	ch chan Message
}

func (r *redisSub) Messages() <-chan Message { return r.ch }
func (r *redisSub) Close() error             { return r.ps.Close() }

// Subscribe implements Source. Any receive error ends the subscription: the
// server then reports it, backs off and subscribes again on a fresh connection.
func (s *RedisSource) Subscribe(ctx context.Context) (Subscription, error) {
	ps := s.rdb.Subscribe(ctx, rediskeys.ChannelBroadcast, rediskeys.ChannelStatus)
	sub := &redisSub{ps: ps, ch: make(chan Message, 1024)}
	for confirmed := 0; confirmed < 2; {
		m, err := ps.ReceiveTimeout(ctx, 5*time.Second)
		if err != nil {
			_ = ps.Close()
			return nil, err
		}
		switch t := m.(type) {
		case *goredis.Subscription:
			confirmed++
		case *goredis.Message: // published between the two confirmations: keep it
			sub.ch <- Message{Channel: t.Channel, Payload: []byte(t.Payload)}
		}
	}
	go func() {
		defer close(sub.ch)
		silent := false
		for {
			// A silent link is probed with PING; if even the PONG does not come, it is dead
			// (a failover or a NAT drop leaves a socket that never errors on its own).
			m, err := ps.ReceiveTimeout(ctx, pingEvery)
			if err != nil {
				var ne net.Error
				if !errors.As(err, &ne) || !ne.Timeout() || silent || ps.Ping(ctx) != nil {
					return
				}
				silent = true
				continue
			}
			silent = false
			if msg, ok := m.(*goredis.Message); ok {
				select {
				case sub.ch <- Message{Channel: msg.Channel, Payload: []byte(msg.Payload)}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return sub, nil
}
