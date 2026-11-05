// Package redis implements the outbox lease guard (ADR-005 §9 step 3).
package redis

import (
	"context"
	"os"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/port/outbound"
)

// KeyProcessedPrefix is the lease key. Shared with payment-api, which claims
// the same rows on its inline fast path — do not rename without updating it.
const KeyProcessedPrefix = "processed:"

// leaseDone is the value a finished row carries, distinguishing "someone is
// working on this" from "this is already done".
const leaseDone = "done"

// NewClient opens a Redis connection and verifies it answers.
func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	return redisutil.NewClient(ctx, addr, password, db)
}

// LeaseGuard is the Redis-backed ILeaseGuard.
type LeaseGuard struct {
	client   *goredis.Client
	workerID string
	ttl      time.Duration
	doneTTL  time.Duration
}

// NewLeaseGuard returns the lease adapter.
//
// workerID is recorded in the key so an operator can see which worker is
// holding a row that is taking too long.
func NewLeaseGuard(client *goredis.Client, ttl, doneTTL time.Duration) portout.ILeaseGuard {
	if ttl <= 0 {
		ttl = entity.LeaseTTL
	}
	if doneTTL <= 0 {
		doneTTL = entity.LeaseDoneTTL
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return &LeaseGuard{client: client, workerID: host, ttl: ttl, doneTTL: doneTTL}
}

// Claim takes the row for this worker.
//
// SET NX is the whole mechanism: exactly one caller gets the key, and the TTL
// means a crashed holder's claim lapses instead of stranding the row forever.
// A permanent flag would make every later pass skip it and lose the event.
func (l *LeaseGuard) Claim(ctx context.Context, outboxID string) (bool, error) {
	ok, err := l.client.SetNX(ctx, KeyProcessedPrefix+outboxID, l.workerID, l.ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Complete marks the row finished for long enough that a redelivery inside the
// window is recognised rather than reprocessed.
func (l *LeaseGuard) Complete(ctx context.Context, outboxID string) error {
	return l.client.Set(ctx, KeyProcessedPrefix+outboxID, leaseDone, l.doneTTL).Err()
}

// Release drops a claim after a failed publish, so the next pass can retry at
// once rather than waiting out the lease.
func (l *LeaseGuard) Release(ctx context.Context, outboxID string) error {
	return l.client.Del(ctx, KeyProcessedPrefix+outboxID).Err()
}
