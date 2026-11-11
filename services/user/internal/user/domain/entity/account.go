package entity

import "time"

// AccountIDPrefix is the human-readable prefix on an account's ULID.
const AccountIDPrefix = "USR-"

// MagicTokenTTL is how long a magic link token remains valid (FR-31).
const MagicTokenTTL = 15 * time.Minute

// MaxSavedNIKs is the maximum number of saved friend NIKs per account (BR-12).
const MaxSavedNIKs = 5

// Purpose distinguishes the two kinds of emailed link.
//
// They are not interchangeable. A verification link proves control of an
// address; a login link grants a session. Letting one be redeemed as the other
// turns "please confirm your email" into an authentication bypass.
type Purpose string

const (
	PurposeLogin       Purpose = "LOGIN"
	PurposeVerifyEmail Purpose = "VERIFY_EMAIL"
)

// Account is a registered user.
//
// NIKHmac is computed by nik-verify and handed here — this service never holds
// the pepper. NIKMasked is stored because a hash cannot be unmasked and the
// contract requires a masked form in responses (FR-32c).
//
// An account with NIKVerifiedAt nil exists but cannot purchase (BR-11).
type Account struct {
	ID              string
	Email           string
	EmailVerifiedAt *time.Time
	NIKHmac         string
	NIKMasked       string
	NIKVerifiedAt   *time.Time
	DukcapilRef     string
	CreatedAt       time.Time
}

// IsNIKVerified reports whether the account may purchase tickets (BR-11).
func (a Account) IsNIKVerified() bool {
	return a.NIKHmac != "" && a.NIKVerifiedAt != nil
}

// IsEmailVerified reports whether the address has been confirmed (FR-31).
func (a Account) IsEmailVerified() bool { return a.EmailVerifiedAt != nil }

// ConsentRecord is proof that the account agreed to third-party NIK processing.
//
// Append-only: no UPDATE, no DELETE (UU PDP No. 27/2022, FR-31d). The database
// enforces this with a trigger, because a repair script must not be able to
// rewrite the evidence either.
//
// What it records is WHO consented and WHEN. It no longer records which policy
// version they saw: that value arrived from the client, so the record could
// only ever be as reliable as whatever posted the form, and ConsentedAt plus a
// versioned policy document answers the same question from the server's side.
type ConsentRecord struct {
	ID          string
	AccountID   string
	ConsentedAt time.Time
}

// MagicToken is a single-use emailed link.
//
// Token holds the plaintext value only in memory, on the path between
// generation and the outbound email. What is persisted is its SHA-256 — a
// database read must not be enough to log in as anyone with a live link.
type MagicToken struct {
	ID        string
	AccountID string // empty when no account exists for the address yet
	Email     string
	Token     string
	TokenHash string
	Purpose   Purpose
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// IsExpired reports whether the token's TTL has passed.
func (m MagicToken) IsExpired() bool { return time.Now().UTC().After(m.ExpiresAt) }

// IsUsed reports whether the token has already been consumed.
func (m MagicToken) IsUsed() bool { return m.UsedAt != nil }
