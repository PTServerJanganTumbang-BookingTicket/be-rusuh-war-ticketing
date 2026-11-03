package entity

import "time"

// Source records where a "valid" answer came from. The three are equivalent in
// authority — each is a Dukcapil answer that genuinely happened — and differ
// only in where it was stored and how long it survives (ADR-005 §8).
type Source string

const (
	// SourceRegistered: accounts.nik_verified_at, written at registration.
	SourceRegistered Source = "registered"
	// SourceSaved: saved_niks.verified_at, written when the NIK was added.
	SourceSaved Source = "saved"
	// SourceVerified: a live Dukcapil call, or its 24-hour cache entry.
	SourceVerified Source = "verified"
)

// Status mirrors the answer Dukcapil gives about a NIK.
type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusInactive Status = "INACTIVE"
	StatusNotFound Status = "NOT_FOUND"
)

// CacheTTL is how long a successful Dukcapil answer is reused. ADR-005 §8 fixes
// this at 24 hours: long enough to absorb retries within one sale, short enough
// that it is not a second store of verification evidence.
const CacheTTL = 24 * time.Hour

// BreakerState is the Dukcapil circuit state, shared through Redis because
// admission-ctl reads it to pause admission under BR-13a. A breaker private to
// this process would not satisfy that rule.
//
// OPEN means the dependency is UNUSABLE and calls are being cut off — as in an
// open electrical circuit. It does not mean "available" (ADR-005 §8).
type BreakerState string

const (
	BreakerClosed   BreakerState = "CLOSED"
	BreakerOpen     BreakerState = "OPEN"
	BreakerHalfOpen BreakerState = "HALF_OPEN"
)

// Verification is the outcome of a validate call.
type Verification struct {
	NIKHmac   string
	NIKMasked string
	Valid     bool
	Status    Status
	Source    Source
	Ref       string
}
