package entity

import "time"

// AccountIDPrefix is the human-readable prefix on an account's ULID.
const AccountIDPrefix = "USR-"

// MagicTokenTTL is how long a magic link token remains valid (FR-31).
const MagicTokenTTL = 15 * time.Minute

// AccessTokenTTL is how long an access token remains valid (seconds, per API contract).
const AccessTokenTTLSeconds = 300

// MaxSavedNIKs is the maximum number of saved friend NIKs per account (FR-15a).
const MaxSavedNIKs = 5

// Account is the core entity representing a registered user.
// nik_hmac is nil/empty until the user completes registration with their NIK.
// nik_verified_at IS NULL means the account cannot purchase tickets.
type Account struct {
	ID              string
	Email           string
	EmailVerifiedAt *time.Time
	VerifyToken     string // pending email-verify token (magic link)
	NIKHmac         string // HMAC-SHA256 of the raw NIK — never stores raw NIK
	NIKVerifiedAt   *time.Time
	DukcapilRef     string
	CreatedAt       time.Time
}

// IsNIKVerified returns true when the account has a verified NIK allowing purchases.
func (a Account) IsNIKVerified() bool {
	return a.NIKHmac != "" && a.NIKVerifiedAt != nil
}

// ConsentRecord captures user consent to the privacy policy.
// Append-only — no UPDATE, no DELETE (UU PDP No. 27/2022).
type ConsentRecord struct {
	ID            string
	AccountID     string
	PolicyVersion string
	ConsentedAt   time.Time
}

// MagicToken is a short-lived token delivered by email for passwordless login.
type MagicToken struct {
	ID        string
	AccountID string // may be empty if account does not exist yet
	Email     string
	Token     string // random opaque value sent in the link
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// IsExpired reports whether the token's TTL has passed.
func (m MagicToken) IsExpired() bool {
	return time.Now().UTC().After(m.ExpiresAt)
}

// IsUsed reports whether the token has already been consumed.
func (m MagicToken) IsUsed() bool {
	return m.UsedAt != nil
}

// AccessToken associates a bearer token with an account for the duration of a session.
type AccessToken struct {
	Token     string
	AccountID string
	ExpiresAt time.Time
}

// IsExpired reports whether the access token has lapsed.
func (t AccessToken) IsExpired() bool {
	return time.Now().UTC().After(t.ExpiresAt)
}
