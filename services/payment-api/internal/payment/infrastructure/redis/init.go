// Package redis holds payment-api's shared-state adapters: the grant store the
// credential guard reads, the payment-gateway circuit breaker, and the
// credential cleanup that follows a settled booking.
//
// Deliberately absent: any queue counter. ADR-006 §3.2 deleted slots:open and
// §4.1 removed this service's inline advance_now_serving + PUBLISH block
// without replacement, because admission-ctl recounts open slots from Aurora
// every tick and picks a freed seat up within 500 ms.
package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisutil"
)

// Keys this service touches. The breaker state is read by other services, so
// it cannot be renamed unilaterally.
const (
	// KeyGrantPrefix + token is a HASH. Fields are fixed by ADR-006 §8.13:
	// event_id, booking_id, queue_token, plus exactly one of account_id or
	// device_hash.
	KeyGrantPrefix = "grant:"
	// KeyBreakerState / KeyBreakerFails guard the payment gateway. Distinct
	// from the Dukcapil breaker: one upstream being down says nothing about
	// the other, and sharing a key would couple two unrelated outages.
	KeyBreakerState = "payment:breaker:state"
	KeyBreakerFails = "payment:breaker:failures"
	// KeyTokenPrefix + uuid is the queue engagement hash.
	KeyTokenPrefix = "token:"
	// KeyAcctPrefix enforces BR-07, one active engagement per account.
	KeyAcctPrefix = "acct:"
	// KeySettledPrefix is the CONFIRMED tombstone. It holds booking_id and
	// nothing else, so §3.3 can answer CONFIRMED after the recovery-capable
	// credential is gone.
	KeySettledPrefix = "settled:"
	// KeyExpiriesPrefix is the sweep's scheduler ZSET, scored by deadline.
	KeyExpiriesPrefix = "expiries:"
)

// NewClient opens a Redis connection and verifies it answers.
func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	return redisutil.NewClient(ctx, addr, password, db)
}
