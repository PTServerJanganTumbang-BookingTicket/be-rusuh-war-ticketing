package entity

import "time"

// SavedNIKIDPrefix is the human-readable prefix on a saved NIK's ULID.
const SavedNIKIDPrefix = "SNK-"

// SavedNIK is a verified friend NIK stored on an account for repeated use.
//
// A row cannot exist without proof of verification: the use case calls
// nik-verify first and inserts second, and the database backs that with
// CHECK (verified_at IS NOT NULL). Both are needed — an unverified entry would
// be used at checkout to SKIP Dukcapil, which moves the fail-closed hole one
// step back rather than closing it (ADR-005 §8).
type SavedNIK struct {
	ID          string
	AccountID   string
	NIKHmac     string
	NIKMasked   string
	Label       string
	VerifiedAt  time.Time
	DukcapilRef string
}
