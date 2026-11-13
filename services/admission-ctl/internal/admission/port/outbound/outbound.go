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
	LapseDue(ctx context.Context, now time.Time) ([]domain.Ended, error)
	// ExpireSelectingDue moves every SELECTING booking whose booking_expires_at <= now to EXPIRED, once, and returns
	// them. Planned in docs/05-booking (B2): without it a closed ticket page holds an admission place for ever.
	ExpireSelectingDue(ctx context.Context, now time.Time) ([]domain.Ended, error)
}

// HeldBookings is the Aurora side of the HELD → EXPIRED sweep (docs/05-booking/02-create-booking.md §3.4).
type HeldBookings interface {
	// ExpireHeldDue moves every HELD booking whose payment_expires_at <= now to EXPIRED and deletes its nik_claims, in one
	// transaction, and returns each with what it held. Once: a row another sweeper or the payment service already
	// moved no longer matches. AWAITING_PAYMENT and PENDING_CANCELLATION (a payment is in flight) are never touched.
	ExpireHeldDue(ctx context.Context, now time.Time) ([]domain.EndedHold, error)
}

// Holds is the Redis side of the same sweep: it gives an expired booking's seats and NIK locks back, once.
type Holds interface {
	// Release adds the held seats back to stock:{ev}:<category> and deletes the nik:used keys, atomically. A stock key
	// that is gone is not recreated; stockMissing counts them (stock reconciliation restores them).
	Release(ctx context.Context, h domain.EndedHold) (stockMissing int, err error)
}

// ReconcileBookings is the Aurora side of stock reconciliation (docs/05-booking/04-stock-reconciliation.md). Writer pool:
// a replica's lag would read a fresh booking as missing and look like a leak.
type ReconcileBookings interface {
	// StockExpectations returns, per category of the event (from ticket_categories, not from Redis), its capacity and the
	// seats taken by bookings in HELD, AWAITING_PAYMENT, PENDING_CANCELLATION or CONFIRMED.
	StockExpectations(ctx context.Context, eventID string) ([]domain.StockExpectation, error)
	// ClaimedNIKs lists the nik_hmac of every nik_claims row of the event.
	ClaimedNIKs(ctx context.Context, eventID string) ([]string, error)
}

// StockRepair is the Redis side of stock reconciliation.
type StockRepair interface {
	// Levels reads stock:{ev}:<category> for every category in one call. missing lists the categories whose key is gone or
	// is not a plain integer.
	Levels(ctx context.Context, eventID string, categories []string) (levels map[string]int64, missing []string, err error)
	// Restore sets a stock key that is gone to value, only if it is still absent (SET NX). restored is false when the key
	// came back in the meantime.
	Restore(ctx context.Context, eventID, category string, value int64) (restored bool, err error)
	// Adjust adds delta only if the key still holds observed, never below zero (adjust_if_unchanged.lua).
	Adjust(ctx context.Context, eventID, category string, observed, delta int64) (domain.AdjustResult, error)
	// MissingLocks returns the hashes among hmacs whose nik:used key does not exist.
	MissingLocks(ctx context.Context, eventID string, hmacs []string) ([]string, error)
	// RestoreLock sets nik:used:{ev}:<hmac> with the TTL, only if it is absent (SET NX EX).
	RestoreLock(ctx context.Context, eventID, hmac string, ttl time.Duration) (restored bool, err error)
	// CountOrphans counts the event's nik:used keys whose hash is not in claimed. They are never deleted: one may belong to
	// a hold that has not committed yet.
	CountOrphans(ctx context.Context, eventID string, claimed map[string]struct{}) (int, error)
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

// PaymentReconciler invokes the payment-api Lambda to sweep AWAITING_PAYMENT
// bookings whose webhook never arrived. The result reports how many were
// expired, settled (alarm: the webhook is broken), or skipped (gateway
// unreachable).
type PaymentReconciler interface {
	// SweepAwaitingPayment invokes {"op":"sweep_awaiting_payment"}.
	SweepAwaitingPayment(ctx context.Context) (domain.SweepResult, error)
}

// Clock is the time source (frozen in tests).
type Clock interface{ Now() time.Time }

// Metrics counts events and raises alerts by name (CountFailures, TickOverrun, ...). The *Event variants are for
// metrics that belong to one event (QueueDepth, Granted, ...): they are kept per event and in the all-events total.
type Metrics interface {
	Inc(name string)
	Gauge(name string, value float64)
	IncEvent(name, eventID string)
	GaugeEvent(name, eventID string, value float64)
}
