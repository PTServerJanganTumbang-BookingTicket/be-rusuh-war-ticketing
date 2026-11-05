package outbound

import (
	"context"
	"time"
)

// IRateLimiter caps how often an identity may trigger an action.
//
// Shared storage, not per-process: the contract's 429 on POST /auth/magic-link
// is meaningless if each Lambda container keeps its own counter.
type IRateLimiter interface {
	// Allow reports whether the action may proceed for this key, and when the
	// caller may retry if not.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}
