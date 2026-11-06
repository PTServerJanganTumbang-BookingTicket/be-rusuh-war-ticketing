// Package ratelimit is a token-bucket rate limiter in Redis.
//
// One atomic Lua call per check refills the bucket by elapsed time and takes
// one token. Time comes from Redis TIME, not the caller, so Lambdas with skewed
// clocks agree. Each bucket is one key, so it is safe in Redis cluster.
//
// The limiter returns Redis errors as they are: whether to fail open or closed
// is the caller's decision.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Bucket hash fields. FieldTimestamp is exported for tests that simulate time.
const (
	FieldTokens    = "tokens"
	FieldTimestamp = "ts" // last refill, Redis TIME in microseconds
)

// Limit refills Refill tokens every Per, up to Capacity.
type Limit struct {
	Capacity int
	Refill   int
	Per      time.Duration
}

func (l Limit) validate() error {
	if l.Capacity <= 0 || l.Refill <= 0 || l.Per <= 0 {
		return fmt.Errorf("ratelimit: invalid limit %+v", l)
	}
	return nil
}

// Result of one check. RetryAfter is set only when not allowed: the time until
// one token is available again.
type Result struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Limiter checks buckets in Redis.
type Limiter struct{ rdb goredis.Scripter }

// New returns a Limiter on rdb (a *redis.Client or *redis.ClusterClient).
func New(rdb goredis.Scripter) *Limiter { return &Limiter{rdb: rdb} }

// KEYS[1] bucket. ARGV: capacity, refill tokens, period in microseconds.
// Returns {allowed (0|1), retry_after_us}.
var allowScript = goredis.NewScript(`
local cap = tonumber(ARGV[1])
local refill = tonumber(ARGV[2])
local per_us = tonumber(ARGV[3])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local b = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(b[1])
local ts = tonumber(b[2])
if tokens == nil or ts == nil then
  tokens = cap
  ts = now
end
if now > ts then
  tokens = math.min(cap, tokens + (now - ts) * refill / per_us)
end
local allowed = 0
local retry_us = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry_us = math.ceil((1 - tokens) * per_us / refill)
end
redis.call('HSET', KEYS[1], 'tokens', string.format('%.17g', tokens), 'ts', now)
redis.call('PEXPIRE', KEYS[1], math.ceil(cap * per_us / refill / 1000))
return {allowed, retry_us}
`)

// Allow takes one token from the bucket at key, if there is one.
func (l *Limiter) Allow(ctx context.Context, key string, lim Limit) (Result, error) {
	if err := lim.validate(); err != nil {
		return Result{}, err
	}
	res, err := allowScript.Run(ctx, l.rdb, []string{key},
		lim.Capacity, lim.Refill, lim.Per.Microseconds()).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit %s: %w", key, err)
	}
	if len(res) != 2 {
		return Result{}, errors.New("ratelimit: unexpected script reply")
	}
	if res[0] == 1 {
		return Result{Allowed: true}, nil
	}
	return Result{RetryAfter: time.Duration(res[1]) * time.Microsecond}, nil
}
