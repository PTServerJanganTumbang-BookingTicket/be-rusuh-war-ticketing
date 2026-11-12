package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// HandoffLimit is 10 handoff codes per minute per grant (docs/07-payment/07 §3 step 2).
var HandoffLimit = ratelimit.Limit{Capacity: 10, Refill: 10, Per: time.Minute}

// HandoffStore implements portout.IHandoffStore. Every command touches one key, so it works on a cluster.
type HandoffStore struct{ rdb goredis.UniversalClient }

// NewHandoffStore returns the handoff store on rdb.
func NewHandoffStore(rdb goredis.UniversalClient) portout.IHandoffStore {
	return &HandoffStore{rdb: rdb}
}

// PutCode is SET paymenthandoff:<code> <grant> NX EX.
func (s *HandoffStore) PutCode(ctx context.Context, code, grantToken string, ttl time.Duration) (bool, error) {
	return s.rdb.SetNX(ctx, rediskeys.PaymentHandoff(code), grantToken, ttl).Result()
}

// TakeCode is GET + DEL of paymenthandoff:<code> in one MULTI: atomic, so two concurrent exchanges cannot both read
// the code. Not GETDEL, which needs Redis 6.2+ and would break on an older local Redis for no gain.
func (s *HandoffStore) TakeCode(ctx context.Context, code string) (string, bool, error) {
	key := rediskeys.PaymentHandoff(code)
	var get *goredis.StringCmd
	_, err := s.rdb.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		get = p.Get(ctx, key)
		p.Del(ctx, key)
		return nil
	})
	if err != nil && !errors.Is(err, goredis.Nil) {
		return "", false, err
	}
	v, err := get.Result()
	if errors.Is(err, goredis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, v != "", nil
}

// GrantBooking reads booking_id and PTTL of grant:<token> in one round trip.
func (s *HandoffStore) GrantBooking(ctx context.Context, grantToken string) (string, time.Duration, bool, error) {
	key := rediskeys.Grant(grantToken)
	var booking *goredis.StringCmd
	var ttl *goredis.DurationCmd
	_, err := s.rdb.Pipelined(ctx, func(p goredis.Pipeliner) error {
		booking = p.HGet(ctx, key, rediskeys.FieldBookingID)
		ttl = p.PTTL(ctx, key)
		return nil
	})
	if err != nil && !errors.Is(err, goredis.Nil) {
		return "", 0, false, err
	}
	id, err := booking.Result()
	if errors.Is(err, goredis.Nil) || (err == nil && id == "") {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	d, err := ttl.Result()
	if err != nil {
		return "", 0, false, err
	}
	// go-redis reports "no TTL" (-1) and "no key" (-2) as tiny negative durations: both are <= 0 here.
	return id, d, true, nil
}

// PutSession is SET paymentsession:<session> <grant> NX PX.
func (s *HandoffStore) PutSession(ctx context.Context, session, grantToken string, ttl time.Duration) (bool, error) {
	return s.rdb.SetNX(ctx, rediskeys.PaymentSession(session), grantToken, ttl).Result()
}

// HandoffLimiter implements portout.IHandoffLimiter on pkg/ratelimit.
type HandoffLimiter struct{ l *ratelimit.Limiter }

// NewHandoffLimiter returns the limiter on rdb.
func NewHandoffLimiter(rdb goredis.Scripter) portout.IHandoffLimiter {
	return &HandoffLimiter{l: ratelimit.New(rdb)}
}

// Allow takes one token from rl:handoff:<grant>.
func (h *HandoffLimiter) Allow(ctx context.Context, grantToken string) (bool, time.Duration, error) {
	res, err := h.l.Allow(ctx, rediskeys.HandoffRateLimit(grantToken), HandoffLimit)
	if err != nil {
		return false, 0, err
	}
	return res.Allowed, res.RetryAfter, nil
}
