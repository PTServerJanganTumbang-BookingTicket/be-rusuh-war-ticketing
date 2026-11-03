package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/outbound"
)

// CircuitBreaker tracks Dukcapil health in Redis.
//
// Shared state, not private: admission-ctl reads dukcapil:breaker:state on
// every tick and pauses admission while it is OPEN (BR-13a). A breaker local to
// this process would block calls here while the queue carried on granting slots
// nobody could use — exactly the "jangan PHP" failure NFR-02 forbids.
//
// OPEN means Dukcapil is UNUSABLE and calls are cut off.
type CircuitBreaker struct {
	client           *goredis.Client
	failureThreshold int64
	openFor          time.Duration
}

// NewCircuitBreaker returns the breaker adapter.
func NewCircuitBreaker(client *goredis.Client, failureThreshold int, openFor time.Duration) portout.ICircuitBreaker {
	if failureThreshold < 1 {
		failureThreshold = 1
	}
	return &CircuitBreaker{
		client:           client,
		failureThreshold: int64(failureThreshold),
		openFor:          openFor,
	}
}

// State reads the current state.
//
// The OPEN marker carries its own TTL, so recovery needs no scheduler: when the
// key expires the breaker reads CLOSED again and the next call probes Dukcapil.
// A stuck OPEN would pause admission indefinitely, which is why expiry drives
// it rather than a timer in a process that may not survive.
func (b *CircuitBreaker) State(ctx context.Context) (entity.BreakerState, error) {
	val, err := b.client.Get(ctx, KeyBreakerState).Result()
	switch {
	case errors.Is(err, goredis.Nil):
		return entity.BreakerClosed, nil
	case err != nil:
		return entity.BreakerClosed, err
	}
	switch entity.BreakerState(val) {
	case entity.BreakerOpen:
		return entity.BreakerOpen, nil
	case entity.BreakerHalfOpen:
		return entity.BreakerHalfOpen, nil
	default:
		return entity.BreakerClosed, nil
	}
}

// RecordSuccess clears the failure run.
//
// Called when Dukcapil ANSWERS, including when the answer is that the NIK is
// invalid. A rejected NIK means the dependency is working; counting it as a
// failure would let a run of mistyped NIKs pause the entire sale.
func (b *CircuitBreaker) RecordSuccess(ctx context.Context) error {
	return b.client.Del(ctx, KeyBreakerFails, KeyBreakerState).Err()
}

// RecordFailure counts a failure and opens the breaker at the threshold.
func (b *CircuitBreaker) RecordFailure(ctx context.Context) error {
	failures, err := b.client.Incr(ctx, KeyBreakerFails).Result()
	if err != nil {
		return err
	}
	// Bound the counter's life so a handful of failures spread over hours
	// never accumulate into an open breaker.
	if failures == 1 {
		if err := b.client.Expire(ctx, KeyBreakerFails, b.openFor).Err(); err != nil {
			return err
		}
	}
	if failures >= b.failureThreshold {
		return b.client.SetEx(ctx, KeyBreakerState, string(entity.BreakerOpen), b.openFor).Err()
	}
	return nil
}
