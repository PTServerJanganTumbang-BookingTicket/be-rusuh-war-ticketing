package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/outbound"
)

// VerificationCache implements nik:valid:{hmac} from ADR-005 §8.
type VerificationCache struct {
	client *goredis.Client
}

// NewVerificationCache returns the cache adapter.
func NewVerificationCache(client *goredis.Client) portout.IVerificationCache {
	return &VerificationCache{client: client}
}

// IsVerified reports whether a live entry exists for this hash.
func (c *VerificationCache) IsVerified(ctx context.Context, nikHmac string) (bool, error) {
	err := c.client.Get(ctx, KeyVerifiedPrefix+nikHmac).Err()
	switch {
	case errors.Is(err, goredis.Nil):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}

// MarkVerified writes the entry.
//
// The value is a constant, not the NIK: only the key's existence carries
// meaning, and storing anything derived from the identity here would put a
// second copy of it in a store with a weaker access boundary than Aurora.
func (c *VerificationCache) MarkVerified(ctx context.Context, nikHmac string, ttl time.Duration) error {
	return c.client.SetEx(ctx, KeyVerifiedPrefix+nikHmac, "1", ttl).Err()
}
