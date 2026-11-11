// Package redis holds payment-api's shared-state adapters: the payment-gateway
// circuit breaker and the credential cleanup that follows a settled booking.
//
// The grant hash is NOT read here. pkg/grantauth reads it itself (ADR-006
// §8.13, §8.17), so there is one reader of grant:{token} across booking and
// payment rather than one per service.
//
// Deliberately absent: any queue counter, and any expiry scheduler. ADR-006
// §3.2 deleted slots:open and §4.1 removed this service's inline
// advance_now_serving + PUBLISH block without replacement; §8.12(c) deleted
// expiries:{ev} and moved the deadline sweep onto a partial index in Aurora.
// admission-ctl recounts from Aurora every tick and picks a freed seat up
// within 500 ms.
package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisutil"
)

// Breaker keys. Named here rather than in pkg/rediskeys because no other
// service reads them; everything shared (grant:, token:, acct:, settled:)
// comes from pkg/rediskeys so one rename reaches every reader.
//
// Distinct from the Dukcapil breaker on purpose: one upstream being down says
// nothing about the other, and sharing a key would couple two unrelated
// outages.
const (
	KeyBreakerState = "payment:breaker:state"
	KeyBreakerFails = "payment:breaker:failures"
)

// NewClient opens a Redis connection and verifies it answers.
func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	return redisutil.NewClient(ctx, addr, password, db)
}
