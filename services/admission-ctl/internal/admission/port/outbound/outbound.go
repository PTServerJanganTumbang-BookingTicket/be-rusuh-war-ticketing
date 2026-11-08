// Package outbound holds the ports the admission use cases call: Aurora, Redis, the clock and the metrics.
package outbound

import (
	"context"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// Bookings is the Aurora side (writer pool).
type Bookings interface {
	CountAdmission(ctx context.Context, eventID string) (domain.Admission, error)
	InsertGranted(ctx context.Context, g domain.Grant) (domain.Booking, error)
	// LapseDue moves every GRANTED booking whose booking_expires_at <= now to LAPSED, once, and returns them.
	LapseDue(ctx context.Context, now time.Time) ([]domain.Lapsed, error)
}

// Queue is the Redis side. Every method is one Redis round trip (or one atomic script).
type Queue interface {
	// Snapshot reads now_serving and issued in one call. Missing fields are 0.
	Snapshot(ctx context.Context, eventID string) (nowServing, issued int64, err error)
	// Pending lists tokens in by_ticket whose ticket number is <= upTo: admitted but not yet finished.
	Pending(ctx context.Context, eventID string, upTo int64) ([]string, error)
	PendingCount(ctx context.Context, eventID string, upTo int64) (int64, error)
	Advance(ctx context.Context, ev domain.Event, a redisscript.Args) (redisscript.Result, error)
	// Token reads the join-time facts of a queue token. found is false when the hash is gone.
	Token(ctx context.Context, queueToken string) (info domain.TokenInfo, found bool, err error)
	PutGrant(ctx context.Context, eventID, queueToken string, b domain.Booking, info domain.TokenInfo, ttl time.Duration) error
	// AttachGrant copies the grant into token:<queue_token>, only if that hash still exists.
	AttachGrant(ctx context.Context, queueToken string, b domain.Booking) error
	Dequeue(ctx context.Context, eventID, queueToken string) error
	View(ctx context.Context, eventID string) (domain.EventView, error)
	SetRate(ctx context.Context, eventID string, ratePerMin int64) error
	// Close moves a fully booked sale to CLOSED, only if it is still FULL_BOOKED since fullBookedAt.
	Close(ctx context.Context, eventID string, fullBookedAt int64) error
	// DropGrant deletes grant:<grant_token>, so the credential dies at once.
	DropGrant(ctx context.Context, grantToken string) error
	PublishBroadcast(ctx context.Context, payload []byte) error
	PublishStatus(ctx context.Context, payload []byte) error
}

// Events finds the sales to tick: every queue:{ev} hash that has a state and is not CLOSED, with its stock categories.
type Events interface {
	Discover(ctx context.Context) ([]domain.Event, error)
}

// Clock is the time source (frozen in tests).
type Clock interface{ Now() time.Time }

// Metrics counts events and raises alerts by name (CountFailures, TickOverrun, ...).
type Metrics interface {
	Inc(name string)
	Gauge(name string, value float64)
}
