package outbound

import (
	"context"
	"time"
)

// IDedupeStore remembers which outbox rows have already been emailed.
//
// SQS is at-least-once, and the publisher's lease guard does not prevent a
// repeat after a crash mid-row (ADR-005 §9 step 3), so the consumer needs its
// own key. SES has no deduplication of its own.
//
// Best-effort by nature: if the store is unavailable or has been flushed, a
// customer may receive one email twice. That is the right failure direction —
// a duplicate ticket email is a nuisance, a missing one is a customer who
// cannot get into the venue.
type IDedupeStore interface {
	// MarkSentIfNew records the id and reports whether it was new. False means
	// another delivery already handled it.
	MarkSentIfNew(ctx context.Context, outboxID string, ttl time.Duration) (bool, error)
	// Unmark removes the record after a failed send, so the redelivery is not
	// mistaken for a duplicate and silently dropped.
	Unmark(ctx context.Context, outboxID string) error
}
