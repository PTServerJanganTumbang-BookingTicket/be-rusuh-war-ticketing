package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// CredentialStore is the post-settlement cleanup.
//
// Everything here is idempotent, which is why there is no lease around it. The
// lease ADR-005 specified existed to protect one non-idempotent operation —
// DECR slots:open — and ADR-006 §3.2 removed that counter entirely.
type CredentialStore struct{ client *goredis.Client }

// NewCredentialStore returns the adapter.
func NewCredentialStore(client *goredis.Client) portout.ICredentialStore {
	return &CredentialStore{client: client}
}

// SettleEngagement releases the queue credentials of a confirmed booking.
//
// Three things, in one pipeline:
//
//   - acct:{ev}:{account_id} is deleted so the account may queue again. BR-07
//     allows one ACTIVE engagement, and a confirmed purchase is not active —
//     a customer who wants a second pair of tickets must not be locked out.
//   - token:{uuid} is deleted because the engagement is over.
//   - settled:{uuid} is written in its place, holding ONLY the booking id.
//     That tombstone is what stops a guest who refreshes in the gap between
//     settlement and the e-ticket email from being shown an expiry screen —
//     telling someone who just paid that their session died is the "jangan
//     PHP" failure in its purest form, and it is factually wrong rather than
//     merely unhelpful (api-contracts §3.3).
//
// A tombstone deliberately cannot recover anything: no ticket_no, no
// account_id, no device_hash.
func (s *CredentialStore) SettleEngagement(ctx context.Context, in portout.SettleInput) error {
	event, err := rediskeys.NewEvent(in.EventID)
	if err != nil {
		return err
	}
	pipe := s.client.TxPipeline()
	if in.AccountID != "" {
		pipe.Del(ctx, event.Acct(in.AccountID))
	}
	if in.QueueToken != "" {
		pipe.Del(ctx, rediskeys.Token(in.QueueToken))
		pipe.Set(ctx, rediskeys.Settled(in.QueueToken), in.BookingID, in.TombstoneTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != goredis.Nil {
		return err
	}
	return nil
}
