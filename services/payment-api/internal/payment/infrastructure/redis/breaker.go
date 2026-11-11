package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

const stateOpen = "OPEN"

// CircuitBreaker tracks payment-gateway health in Redis.
//
// Shared rather than per-process: a breaker local to one Lambda container
// learns nothing from the other hundred containers timing out, so under a real
// Midtrans outage every container would independently discover it, slowly,
// while customers waited.
type CircuitBreaker struct {
	client    *goredis.Client
	threshold int64
	openFor   time.Duration
}

// NewCircuitBreaker returns the breaker adapter.
func NewCircuitBreaker(client *goredis.Client, threshold int, openFor time.Duration) portout.ICircuitBreaker {
	if threshold < 1 {
		threshold = 1
	}
	return &CircuitBreaker{client: client, threshold: int64(threshold), openFor: openFor}
}

// Allow reports whether a charge may be attempted.
func (b *CircuitBreaker) Allow(ctx context.Context) (bool, error) {
	val, err := b.client.Get(ctx, KeyBreakerState).Result()
	if errors.Is(err, goredis.Nil) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return val != stateOpen, nil
}

// RecordSuccess closes the breaker.
//
// Both keys are cleared, not just the state: leaving the failure counter near
// its threshold would let a single later failure trip the breaker immediately,
// so a recovered gateway would keep flapping.
func (b *CircuitBreaker) RecordSuccess(ctx context.Context) error {
	return b.client.Del(ctx, KeyBreakerState, KeyBreakerFails).Err()
}

// RecordFailure counts a failure and opens the breaker at the threshold.
//
// The OPEN marker carries its own TTL, so recovery needs no scheduler: when the
// key expires the next call probes the gateway again. A breaker reset by a
// timer in a process that may not survive would stay open forever.
func (b *CircuitBreaker) RecordFailure(ctx context.Context) error {
	n, err := b.client.Incr(ctx, KeyBreakerFails).Result()
	if err != nil {
		return err
	}
	if n == 1 {
		// Bound the counting window, so failures spread over hours do not
		// accumulate into an outage that never happened.
		_ = b.client.Expire(ctx, KeyBreakerFails, b.openFor).Err()
	}
	if n >= b.threshold {
		return b.client.Set(ctx, KeyBreakerState, stateOpen, b.openFor).Err()
	}
	return nil
}
