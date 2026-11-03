package outbound

import "context"

// NIKVerification is what nik-verify returns about a NIK.
//
// The hash comes back rather than being computed here. That keeps the pepper
// inside nik-verify: this service stores what it is handed and never holds the
// secret, so compromising it does not enable offline enumeration of every
// stored NIK — a risk ADR-005 §8 accepts for each process that holds the
// pepper, and which cannot be remediated because rotation is impossible.
type NIKVerification struct {
	// NIKHmac is the keyed hash to persist and index on.
	NIKHmac string
	// NIKMasked is the display form to persist alongside it. A hash cannot be
	// unmasked, so this is the only way to satisfy FR-32c later.
	NIKMasked string
	Valid     bool
	Status    string // "ACTIVE" | "INACTIVE" | "NOT_FOUND"
	Source    string // "registered" | "saved" | "verified"
	Ref       string
}

// INikVerifier is the outbound port for POST /nik/validate on nik-verify.
//
// This service never calls Dukcapil directly. nik-verify owns the rate limiter,
// the circuit breaker whose state pauses admission (BR-13a), and the 24-hour
// verification cache; a second path to Dukcapil would defeat all three.
//
// An error means no answer was obtainable, and callers must fail closed on it
// (BR-13). A NIK that Dukcapil rejects is NOT an error — it comes back as a
// result with Valid false.
type INikVerifier interface {
	VerifyNIK(ctx context.Context, nik string) (NIKVerification, error)
}
