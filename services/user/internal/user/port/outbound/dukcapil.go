package outbound

import "context"

// DukcapilResult holds the outcome of a NIK verification call.
type DukcapilResult struct {
	Valid  bool
	Name   string // name returned by Dukcapil (ADR: only NIK is sent, name is returned)
	Status string // "ACTIVE" | "INACTIVE"
	Ref    string // opaque reference ID from Dukcapil, stored for audit
}

// IDukcapilAdapter is the outbound port for the national identity verification service.
//
// Design constraints from ADR-005 §8:
//   - Fail closed: any error (network, timeout, rejection) blocks the operation.
//   - Only the NIK is sent — no name, no date of birth, no additional PII (FR-15a).
//   - The POC uses a stub implementation; production uses the real Dukcapil HTTP API.
type IDukcapilAdapter interface {
	VerifyNIK(ctx context.Context, nik string) (DukcapilResult, error)
}
