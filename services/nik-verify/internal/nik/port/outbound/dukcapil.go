package outbound

import "context"

// DukcapilResult holds the outcome of a NIK verification call.
//
// Only the NIK is ever sent. Name and date of birth are neither collected nor
// transmitted (FR-15a, requirements §12). Dukcapil returns a name; this struct
// deliberately has no field for it, so there is no path by which it reaches
// storage or a response.
type DukcapilResult struct {
	Valid  bool
	Status string // "ACTIVE" | "INACTIVE" | "NOT_FOUND"
	Ref    string // opaque reference to this answer, stored for audit
}

// IDukcapilAdapter is the outbound port for the national identity service.
//
// It returns an error only when Dukcapil could not be reached or did not
// answer. A NIK that Dukcapil rejects is a successful call with Valid false —
// the distinction drives the circuit breaker, which must not open because
// customers are mistyping their NIKs.
type IDukcapilAdapter interface {
	VerifyNIK(ctx context.Context, nik string) (DukcapilResult, error)
}
