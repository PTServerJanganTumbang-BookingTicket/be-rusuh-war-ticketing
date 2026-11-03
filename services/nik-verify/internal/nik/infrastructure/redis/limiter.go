package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"

	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/outbound"
)

// tokenBucket refills lazily from the elapsed time since the last call, so no
// background job is needed and a cold Redis starts full.
//
// It runs as one Lua script because read-decide-write across three round trips
// is not atomic, and under a flash crowd the interleavings are not rare — they
// are the normal case. KEYS[1] holds "tokens:last_refill_ms".
const tokenBucket = `
local state = redis.call('GET', KEYS[1])
local capacity = tonumber(ARGV[1])
local refill_per_ms = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])

local tokens = capacity
local last = now_ms
if state then
  local sep = string.find(state, ':')
  tokens = tonumber(string.sub(state, 1, sep - 1))
  last = tonumber(string.sub(state, sep + 1))
  local elapsed = now_ms - last
  if elapsed > 0 then
    tokens = math.min(capacity, tokens + elapsed * refill_per_ms)
  end
end

local allowed = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
end

redis.call('SET', KEYS[1], tostring(tokens) .. ':' .. tostring(now_ms), 'PX', ttl)
return allowed
`

// RateLimiter caps Dukcapil calls at the contracted rate across every caller.
type RateLimiter struct {
	client   *goredis.Client
	rps      float64
	capacity float64
	script   *goredis.Script
}

// NewRateLimiter returns the limiter. capacity is the burst allowance: the
// contracted rate is a sustained figure, and a burst ceiling lets a flash crowd
// draw ahead briefly without exceeding the average.
func NewRateLimiter(client *goredis.Client, rps, burstCapacity float64) portout.IRateLimiter {
	if rps <= 0 {
		rps = 1
	}
	if burstCapacity < 1 {
		burstCapacity = 1
	}
	return &RateLimiter{
		client:   client,
		rps:      rps,
		capacity: burstCapacity,
		script:   goredis.NewScript(tokenBucket),
	}
}

// Allow consumes one token.
func (l *RateLimiter) Allow(ctx context.Context) (bool, error) {
	// A TTL comfortably longer than a full refill keeps idle keys from
	// lingering while never expiring a bucket that is still in use.
	ttlMs := int64(l.capacity/l.rps*1000) + 60_000
	res, err := l.script.Run(ctx, l.client,
		[]string{KeyRateLimit},
		l.capacity, l.rps/1000.0, time.Now().UnixMilli(), ttlMs,
	).Int64()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}
