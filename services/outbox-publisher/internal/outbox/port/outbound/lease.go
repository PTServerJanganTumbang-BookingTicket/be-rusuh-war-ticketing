package outbound

import "context"

// ILeaseGuard stops two publishers processing one row at the same time.
//
// At-least-once, not exactly-once (ADR-005 §9 step 3). It prevents concurrent
// duplication; it does not prevent a repeat after a crash mid-row. For email
// that is the right trade: a duplicate message is a nuisance, a lost one is a
// customer who never gets their ticket.
type ILeaseGuard interface {
	// Claim takes the row for this worker, reporting false when another holds
	// it or it is already done.
	Claim(ctx context.Context, outboxID string) (bool, error)
	// Complete marks the row finished for long enough that a redelivery is
	// recognised rather than reprocessed.
	Complete(ctx context.Context, outboxID string) error
	// Release drops a claim so the next pass can retry immediately, instead of
	// waiting out the lease.
	Release(ctx context.Context, outboxID string) error
}
