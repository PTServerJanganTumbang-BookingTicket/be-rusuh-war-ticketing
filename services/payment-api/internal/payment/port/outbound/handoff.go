package outbound

import (
	"context"
	"time"
)

// IHandoffStore keeps the WebView handoff credentials in Redis (ADR-007, docs/07-payment/07 and 08). Both keys hold
// the grant token and nothing else: the booking id lives in the grant hash.
type IHandoffStore interface {
	// PutCode stores paymenthandoff:<code> → grantToken for ttl, only if the key is new. false means it existed.
	PutCode(ctx context.Context, code, grantToken string, ttl time.Duration) (bool, error)
	// TakeCode reads and deletes the code atomically (GET + DEL in one MULTI). found is false for an unknown, used or expired code.
	TakeCode(ctx context.Context, code string) (grantToken string, found bool, err error)
	// GrantBooking reads the booking id and the remaining TTL of grant:<grantToken>. found is false when the grant is
	// gone. A grant without a TTL comes back with ttl <= 0.
	GrantBooking(ctx context.Context, grantToken string) (bookingID string, ttl time.Duration, found bool, err error)
	// PutSession stores paymentsession:<session> → grantToken for ttl, only if the key is new.
	PutSession(ctx context.Context, session, grantToken string, ttl time.Duration) (bool, error)
}

// IHandoffLimiter limits handoff codes per grant (10 / min). An error means the limiter could not decide.
type IHandoffLimiter interface {
	Allow(ctx context.Context, grantToken string) (allowed bool, retryAfter time.Duration, err error)
}
