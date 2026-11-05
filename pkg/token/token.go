// Package token issues and verifies the compact JWTs that carry account
// identity between services.
//
// Why hand-rolled rather than a JWT library: pkg deliberately carries almost no
// dependencies, and this needs exactly one algorithm. Restricting to HS256 and
// rejecting every other `alg` outright removes the algorithm-confusion class of
// bug that general-purpose JWT libraries have to defend against. If the project
// later needs RS256, asymmetric keys or JWKS rotation, replace this package with
// github.com/golang-jwt/jwt — do not widen it in place.
//
// The signing secret is shared by every service that verifies these tokens. It
// is NOT the NIK pepper and must be a different secret: the pepper can never be
// rotated (ADR-005 §8), whereas this one should be.
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Errors returned by Verify. Callers map these onto TOKEN_EXPIRED and
// TOKEN_INVALID; the distinction matters because an expired token means
// "refresh", while an invalid one means "log in again".
var (
	ErrExpired = errors.New("token expired")
	ErrInvalid = errors.New("token invalid")
)

const (
	algHS256 = "HS256"
	typJWT   = "JWT"
)

// Claims is the payload. Only the registered claims this system actually uses
// are present — adding more means more to validate.
type Claims struct {
	Subject   string `json:"sub"` // account ID
	Email     string `json:"email,omitempty"`
	Issuer    string `json:"iss,omitempty"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	// NIKVerified lets a verifying service answer "may this account buy?"
	// without a database round trip. It mirrors accounts.nik_verified_at at
	// issue time and is therefore only as fresh as the token's TTL.
	NIKVerified bool `json:"nik_verified"`
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// Issuer signs and verifies tokens with a shared secret.
type Issuer struct {
	secret []byte
	issuer string
	ttl    time.Duration
	// now is injectable so tests can control expiry without sleeping.
	now func() time.Time
}

// NewIssuer builds an Issuer. An empty secret is a programming error the caller
// must catch at startup, so it is reported rather than defaulted — a default
// signing key is a forged-token generator.
func NewIssuer(secret, issuerName string, ttl time.Duration) (*Issuer, error) {
	if len(secret) == 0 {
		return nil, errors.New("token: signing secret must not be empty")
	}
	if ttl <= 0 {
		return nil, errors.New("token: ttl must be positive")
	}
	return &Issuer{secret: []byte(secret), issuer: issuerName, ttl: ttl, now: time.Now}, nil
}

// TTL reports the configured lifetime, for the expires_in field in responses.
func (i *Issuer) TTL() time.Duration { return i.ttl }

// Issue returns a signed token for the account.
func (i *Issuer) Issue(accountID, email string, nikVerified bool) (string, Claims, error) {
	now := i.now().UTC()
	claims := Claims{
		Subject:     accountID,
		Email:       email,
		Issuer:      i.issuer,
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(i.ttl).Unix(),
		NIKVerified: nikVerified,
	}

	headerJSON, err := json.Marshal(header{Alg: algHS256, Typ: typJWT})
	if err != nil {
		return "", Claims{}, err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", Claims{}, err
	}

	signingInput := encode(headerJSON) + "." + encode(claimsJSON)
	return signingInput + "." + encode(i.sign(signingInput)), claims, nil
}

// Verify checks the signature and expiry and returns the claims.
//
// The header's alg is compared against HS256 before the signature is checked,
// so a token presenting alg:none or alg:RS256 is rejected outright rather than
// being verified under an algorithm the attacker chose.
func (i *Issuer) Verify(raw string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalid
	}

	headerJSON, err := decode(parts[0])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var h header
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return Claims{}, ErrInvalid
	}
	if h.Alg != algHS256 {
		return Claims{}, ErrInvalid
	}

	signature, err := decode(parts[2])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	// Constant-time: a byte-by-byte compare leaks how much of the signature
	// matched, which is enough to forge one a byte at a time.
	if !hmac.Equal(signature, i.sign(parts[0]+"."+parts[1])) {
		return Claims{}, ErrInvalid
	}

	claimsJSON, err := decode(parts[1])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return Claims{}, ErrInvalid
	}
	if claims.Subject == "" || claims.ExpiresAt == 0 {
		return Claims{}, ErrInvalid
	}
	if i.now().UTC().Unix() >= claims.ExpiresAt {
		return Claims{}, ErrExpired
	}
	return claims, nil
}

func (i *Issuer) sign(signingInput string) []byte {
	mac := hmac.New(sha256.New, i.secret)
	mac.Write([]byte(signingInput))
	return mac.Sum(nil)
}

// JWT uses base64url without padding (RFC 7515 §2).
func encode(b []byte) string          { return base64.RawURLEncoding.EncodeToString(b) }
func decode(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
