package outbound

import (
	"context"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/entity"
)

// IVerificationCache is the nik:valid:{hmac} cache from ADR-005 §8. A hit is a
// Dukcapil "valid" that is still inside its 24-hour window.
type IVerificationCache interface {
	// IsVerified reports whether a live cache entry exists for this hash.
	IsVerified(ctx context.Context, nikHmac string) (bool, error)
	// MarkVerified writes the entry with the given TTL. Only ever called after
	// a successful, positive Dukcapil answer — a negative answer writes nothing,
	// so a rejected NIK is retried rather than cached as rejected.
	MarkVerified(ctx context.Context, nikHmac string, ttl time.Duration) error
}

// IRateLimiter caps calls to Dukcapil at the contracted rate.
//
// Shared, not per-process: DUKCAPIL_RATE_LIMIT_RPS is the ceiling on the whole
// sale (ADR-005 §8 notes Dukcapil becomes the throughput cap), so a limiter
// local to one Lambda container enforces nothing.
type IRateLimiter interface {
	// Allow consumes one token, reporting whether the call may proceed.
	Allow(ctx context.Context) (bool, error)
}

// ICircuitBreaker tracks Dukcapil health in shared storage.
//
// The state is published rather than kept private because admission-ctl reads
// it every tick to decide whether to pause admission (BR-13a). Recording
// outcomes here is therefore not bookkeeping — it is how the queue learns to
// stop granting slots nobody could use.
type ICircuitBreaker interface {
	State(ctx context.Context) (entity.BreakerState, error)
	RecordSuccess(ctx context.Context) error
	RecordFailure(ctx context.Context) error
}
