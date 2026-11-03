// Package redis remembers which outbox rows have already been emailed.
package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisutil"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/port/outbound"
)

// KeySentPrefix namespaces the dedupe keys. Distinct from the publisher's
// `processed:` lease: that one means "a publisher is handling this row", this
// one means "an email for it has gone out".
const KeySentPrefix = "email:sent:"

// NewClient opens a Redis connection and verifies it answers.
func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	return redisutil.NewClient(ctx, addr, password, db)
}

// DedupeStore is the Redis-backed IDedupeStore.
type DedupeStore struct {
	client *goredis.Client
}

// NewDedupeStore returns the dedupe adapter.
func NewDedupeStore(client *goredis.Client) portout.IDedupeStore {
	return &DedupeStore{client: client}
}

// MarkSentIfNew records the id and reports whether it was new.
//
// SET NX makes the check and the claim one operation. Checking then setting
// would let two concurrent deliveries of the same message both pass the check
// and both send.
func (d *DedupeStore) MarkSentIfNew(ctx context.Context, outboxID string, ttl time.Duration) (bool, error) {
	return d.client.SetNX(ctx, KeySentPrefix+outboxID, "1", ttl).Result()
}

// Unmark releases a claim after a failed send.
//
// Without this a failed attempt would make its own redelivery look like a
// duplicate, and the message would be dropped precisely because the first try
// failed.
func (d *DedupeStore) Unmark(ctx context.Context, outboxID string) error {
	return d.client.Del(ctx, KeySentPrefix+outboxID).Err()
}
