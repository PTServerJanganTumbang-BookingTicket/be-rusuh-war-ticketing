// Package redis holds the shared-state adapters: the verification cache, the
// Dukcapil rate limiter and the Dukcapil circuit breaker.
//
// All three are in Redis rather than in process memory because they are shared
// facts, not local ones. The rate limit is a ceiling on the whole sale, and the
// breaker state is read by admission-ctl to pause admission (BR-13a) — neither
// means anything if each Lambda container keeps its own copy.
package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Keys used by this service. Shared with admission-ctl, which reads
// dukcapil:breaker:state every tick — do not rename without updating it.
const (
	KeyVerifiedPrefix = "nik:valid:"
	KeyBreakerState   = "dukcapil:breaker:state"
	KeyBreakerFails   = "dukcapil:breaker:failures"
	KeyRateLimit      = "dukcapil:ratelimit"
)

// NewClient opens a Redis connection and verifies it answers.
//
// Pinging at startup is deliberate: a Lambda that starts healthy against an
// unreachable Redis would serve traffic with its guards silently degraded.
func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}
