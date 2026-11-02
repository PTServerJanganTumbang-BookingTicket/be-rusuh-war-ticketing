package entity

import "time"

// SavedNIK is a verified friend NIK stored on an account for repeated use.
// A row can only exist if verified_at IS NOT NULL — the CHECK constraint in the
// database and the application logic (call Dukcapil first, INSERT after) together
// ensure no unverified NIK ever reaches persistent storage (FR-15a).
type SavedNIK struct {
	ID          string
	AccountID   string
	NIKHmac     string // HMAC-SHA256 of the raw NIK
	Label       string // user-provided nickname (e.g. "Budi")
	VerifiedAt  time.Time
	DukcapilRef string
}
