// Package redis holds the shared-state adapters for the user domain.
package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisutil"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// NewClient opens a Redis connection and verifies it answers.
func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	return redisutil.NewClient(ctx, addr, password, db)
}

// fixedWindow increments a counter and sets its expiry on first use, in one
// round trip. Split into INCR then EXPIRE, a crash between them leaves a key
// with no TTL — a counter that never resets, locking the address out forever.
const fixedWindow = `
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
return {current, ttl}
`

// RateLimiter is a fixed-window limiter in shared storage.
type RateLimiter struct {
	client *goredis.Client
	script *goredis.Script
}

// NewRateLimiter returns the limiter adapter.
func NewRateLimiter(client *goredis.Client) portout.IRateLimiter {
	return &RateLimiter{client: client, script: goredis.NewScript(fixedWindow)}
}

// Allow consumes one unit against the key's window.
func (l *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	res, err := l.script.Run(ctx, l.client, []string{"ratelimit:" + key}, window.Milliseconds()).Slice()
	if err != nil {
		return false, 0, err
	}
	if len(res) != 2 {
		return true, 0, nil
	}

	current, _ := res[0].(int64)
	ttlMs, _ := res[1].(int64)
	if current > int64(limit) {
		retryAfter := time.Duration(ttlMs) * time.Millisecond
		if retryAfter < 0 {
			retryAfter = window
		}
		return false, retryAfter, nil
	}
	return true, 0, nil
}
