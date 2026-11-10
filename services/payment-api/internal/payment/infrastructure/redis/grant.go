package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
)

// GrantStore reads grant:{token} for pkg/grantauth.
type GrantStore struct{ client *goredis.Client }

// NewGrantStore returns the grantauth.Store adapter.
func NewGrantStore(client *goredis.Client) grantauth.Store { return &GrantStore{client: client} }

// Grant returns the hash fields, or grantauth.ErrNotFound.
//
// HGETALL on a missing key returns an EMPTY MAP rather than an error, which is
// why the length is checked explicitly: without it an expired grant would look
// like a grant with no fields and sail past the credential check.
//
// The key's own TTL is the expiry mechanism, so there is no timestamp to
// compare. It is GRANT_TOKEN_TTL_SECONDS (1080) from granted_at, not the
// 300-second booking TTL — a hash dying at second 300 would cut off a customer
// who already holds a seat, mid-payment (ADR-006 §8.13(b)).
func (s *GrantStore) Grant(ctx context.Context, token string) (map[string]string, error) {
	fields, err := s.client.HGetAll(ctx, KeyGrantPrefix+token).Result()
	if err != nil && err != goredis.Nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, grantauth.ErrNotFound
	}
	return fields, nil
}
