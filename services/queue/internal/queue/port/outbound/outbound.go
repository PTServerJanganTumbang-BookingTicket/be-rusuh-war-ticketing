// Package outbound declares what the join use case needs from the outside.
package outbound

import (
	"context"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

// QueueStore is the Redis side of the queue. Every write is a single key or a
// single {ev} slot; join never runs the admission Lua and never publishes.
type QueueStore interface {
	// Snapshot reads state, now_serving, issued, name, sale_starts_at and
	// sold_out_at in one round trip.
	Snapshot(ctx context.Context, ev rediskeys.Event) (domain.EventSnapshot, error)
	// Engagement reads token:<queueToken>. found is false if it does not exist.
	Engagement(ctx context.Context, queueToken string) (e domain.Engagement, found bool, err error)
	// AccountToken reads acct:{ev}:<accountID>.
	AccountToken(ctx context.Context, ev rediskeys.Event, accountID string) (queueToken string, found bool, err error)
	// ClaimAccount sets acct:{ev}:<accountID> to queueToken if it is unset, or
	// if it still holds replace (a stale pointer; "" means none). It returns the
	// value the key holds afterwards.
	ClaimAccount(ctx context.Context, ev rediskeys.Event, accountID, queueToken, replace string, ttl time.Duration) (claimed bool, current string, err error)
	// ReleaseAccount deletes acct:{ev}:<accountID> if it still holds queueToken.
	ReleaseAccount(ctx context.Context, ev rediskeys.Event, accountID, queueToken string) error
	// IssueTicket is INCR queue:{ev}:issued.
	IssueTicket(ctx context.Context, ev rediskeys.Event) (int64, error)
	// SaveEngagement writes token:<qt> with a TTL.
	SaveEngagement(ctx context.Context, e domain.Engagement, ttl time.Duration) error
	// AddToLine is ZADD queue:{ev}:by_ticket <ticketNo> <qt>.
	AddToLine(ctx context.Context, ev rediskeys.Event, queueToken string, ticketNo int64) error
	// RepairLine puts qt back into by_ticket (ZADD NX) only while ticketNo is
	// still ahead of now_serving, atomically with admission-ctl's advance.
	// added is false if it was already there or its turn has passed.
	RepairLine(ctx context.Context, ev rediskeys.Event, queueToken string, ticketNo int64) (added bool, err error)
}

// BookingReader reads bookings (Aurora writer pool, SELECT only).
type BookingReader interface {
	ByQueueToken(ctx context.Context, eventID, queueToken string) (b domain.Booking, found bool, err error)
}

// Captcha verifies an invisible CAPTCHA token server-side.
type Captcha interface {
	Verify(ctx context.Context, captchaToken, remoteIP string) domain.CaptchaResult
}

// RateLimiter is pkg/ratelimit's Limiter.
type RateLimiter interface {
	Allow(ctx context.Context, key string, lim ratelimit.Limit) (ratelimit.Result, error)
}

// TokenVerifier verifies an access token (pkg/token).
type TokenVerifier interface {
	Verify(raw string) (token.Claims, error)
}

// Metrics records operational metrics.
type Metrics interface {
	Incr(name string)
	Record(name string, value float64)
}
