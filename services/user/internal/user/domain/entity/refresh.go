package entity

import "time"

// RefreshTokenIDPrefix is the human-readable prefix on a refresh token's ULID.
const RefreshTokenIDPrefix = "RFT-"

// RefreshTokenTTL is how long a refresh token remains redeemable.
//
// Long by design: the access token lives five minutes (contract §1.3), so this
// is what keeps a customer logged in across a sale. Rotation plus family
// revocation is what makes a long-lived credential acceptable.
const RefreshTokenTTL = 30 * 24 * time.Hour

// RefreshToken is one link in a rotation chain.
//
// Token holds the plaintext only in memory, between generation and the
// response. TokenHash is what persists — a database read must not be enough to
// mint sessions.
type RefreshToken struct {
	ID         string
	AccountID  string
	FamilyID   string
	Token      string
	TokenHash  string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	UsedAt     *time.Time
	RevokedAt  *time.Time
	ReplacedBy string
}

// IsExpired reports whether the token's TTL has passed.
func (r RefreshToken) IsExpired() bool { return time.Now().UTC().After(r.ExpiresAt) }

// IsUsed reports whether the token has already been redeemed. A used token
// presented again is the signature of a stolen credential, not a mistake.
func (r RefreshToken) IsUsed() bool { return r.UsedAt != nil }

// IsRevoked reports whether the token was invalidated before use.
func (r RefreshToken) IsRevoked() bool { return r.RevokedAt != nil }
