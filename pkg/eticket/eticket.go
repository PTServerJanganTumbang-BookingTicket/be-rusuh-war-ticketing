// Package eticket signs and checks the token that opens one e-ticket without an account (api-contracts §6.2,
// impl-plan §3.10). Booking mints it into GET /bookings/{id} (eticket_url), notification into the email link,
// user-service verifies it on GET /tickets/{id}/eticket?t=.
//
// Token = base64url(exp) + "." + base64url(HMAC-SHA256(key, ticket_id + "." + exp)). Stateless: the ticket id is
// bound but not carried (it comes from the URL path). Revocation is only by rotating ETICKET_SIGNING_KEY.
package eticket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalid is returned for any token that is malformed, expired, forged or for another ticket. One error for all:
// telling them apart would tell a guesser which part was nearly right.
var ErrInvalid = errors.New("eticket: invalid token")

// ErrNoKey is returned by NewSigner for an empty key.
var ErrNoKey = errors.New("eticket: signing key is empty")

// Signer mints and verifies tokens with one key.
type Signer struct{ key []byte }

// maxTokenLen bounds the work done on input from a URL: a real token is about 60 characters.
const maxTokenLen = 256

var b64 = base64.RawURLEncoding

// jakarta is WIB (UTC+7, no daylight saving); fixed so the result does not depend on the host's tzdata.
var jakarta = time.FixedZone("WIB", 7*60*60)

// NewSigner builds a Signer. An empty key is an error, never a default.
func NewSigner(key []byte) (*Signer, error) {
	if len(key) == 0 {
		return nil, ErrNoKey
	}
	return &Signer{key: append([]byte(nil), key...)}, nil
}

// Mint signs ticketID until expiresAt (whole seconds).
func (s *Signer) Mint(ticketID string, expiresAt time.Time) (string, error) {
	if ticketID == "" {
		return "", errors.New("eticket: empty ticket id")
	}
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	return b64.EncodeToString([]byte(exp)) + "." + b64.EncodeToString(s.mac(ticketID, exp)), nil
}

// Verify checks token for ticketID at now: well formed, signed with this key for this ticket, not expired.
func (s *Signer) Verify(token, ticketID string, now time.Time) error {
	if len(token) > maxTokenLen || ticketID == "" {
		return ErrInvalid
	}
	expPart, macPart, ok := strings.Cut(token, ".")
	if !ok {
		return ErrInvalid
	}
	expRaw, err1 := b64.DecodeString(expPart)
	mac, err2 := b64.DecodeString(macPart)
	if err1 != nil || err2 != nil {
		return ErrInvalid
	}
	exp, err := strconv.ParseInt(string(expRaw), 10, 64)
	if err != nil {
		return ErrInvalid
	}
	// The MAC is checked before the expiry, so a forged token learns nothing from the answer.
	if !hmac.Equal(mac, s.mac(ticketID, string(expRaw))) {
		return ErrInvalid
	}
	if now.Unix() >= exp {
		return ErrInvalid
	}
	return nil
}

func (s *Signer) mac(ticketID, exp string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(ticketID))
	m.Write([]byte{'.'})
	m.Write([]byte(exp))
	return m.Sum(nil)
}

// ExpiresAt is when a ticket's link stops working: 00:00 Asia/Jakarta on the day after the event (the gate opens
// on event day). eventDate is YYYY-MM-DD.
func ExpiresAt(eventDate string) (time.Time, error) {
	d, err := time.ParseInLocation(time.DateOnly, eventDate, jakarta)
	if err != nil {
		return time.Time{}, fmt.Errorf("eticket: event date %q: %w", eventDate, err)
	}
	return d.AddDate(0, 0, 1).UTC(), nil
}
