// Package grantauth verifies the grant credential that guards every endpoint
// in API contract §4 (booking) and §5 (payment).
//
// Three separate credentials exist in this system and they are deliberately
// not interchangeable (ADR-005 "Empat kredensial, empat slot"):
//
//   - Authorization: Bearer — the account session. NOT sent to §4/§5 at all.
//   - X-Grant-Token          — proves the holder owns a slot. What this package checks.
//   - queue_token cookie     — proves a position in line. Waiting room only.
//
// A valid grant proves the bearer holds *a* slot. It does NOT prove the slot
// belongs to the booking_id in the URL, so every caller must still bind the row
// in SQL (WHERE id = $1 AND grant_token = $2). This package does the credential
// half; the ownership half is the repository's.
package grantauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
)

// Header names. X-Device-Id is required of guests only; see Grant.IsGuest.
const (
	HeaderGrantToken = "X-Grant-Token"
	HeaderDeviceID   = "X-Device-Id"
)

// Redis hash fields of grant:{token}, fixed by ADR-006 §8.13(a).
//
// Exactly one of FieldAccountID or FieldDeviceHash is written; the other is
// absent, never empty. That is what makes DEVICE_MISMATCH a guest-only
// condition — an account grant travels between devices by design, because the
// account is what binds it.
//
// There is deliberately no "token" field (that is the key's own name) and no
// "rebinds" field (ADR-006 §8.9 removed rebinding entirely).
const (
	FieldEventID    = "event_id"
	FieldBookingID  = "booking_id"
	FieldQueueToken = "queue_token"
	FieldAccountID  = "account_id"
	FieldDeviceHash = "device_hash"
)

// Grant is a verified grant credential.
type Grant struct {
	// Token is the raw X-Grant-Token value. Repositories match it against
	// bookings.grant_token; it is the ownership key, so it travels onward.
	Token string

	EventID    string
	BookingID  string
	QueueToken string

	// Exactly one of these is set, mirroring the hash.
	AccountID  string
	DeviceHash string
}

// IsGuest reports whether this grant is bound to a device rather than an
// account. Only guests are subject to the device check.
func (g Grant) IsGuest() bool { return g.AccountID == "" }

// Store reads the grant hash. Implemented by a Redis adapter; an interface so
// that callers can be tested without a Redis.
type Store interface {
	// Grant returns the fields of grant:{token}. A missing or expired key must
	// return ErrNotFound rather than an empty map, so that "revoked" and
	// "Redis is broken" cannot be confused.
	Grant(ctx context.Context, token string) (map[string]string, error)
}

// ErrNotFound reports that grant:{token} does not exist — unknown, expired, or
// revoked at a terminal state. All three are one answer to the caller, because
// telling them apart maps which grants are live.
var ErrNotFound = errors.New("grantauth: grant not found")

// Verifier checks grant credentials.
type Verifier struct {
	store  Store
	pepper []byte
}

// NewVerifier builds a Verifier. The pepper is DEVICE_PEPPER and is required
// whenever any guest grant exists, which in practice is always: without it the
// device check cannot run and every guest would be rejected.
//
// DEVICE_PEPPER is NOT the NIK pepper and not JWT_SECRET. It may be rotated
// between sales but never during one — rotation invalidates every live binding
// at once (api-contracts §9).
func NewVerifier(store Store, pepper string) (*Verifier, error) {
	if store == nil {
		return nil, errors.New("grantauth: store is required")
	}
	if pepper == "" {
		return nil, errors.New("grantauth: DEVICE_PEPPER is required: without it every guest grant fails the device check")
	}
	return &Verifier{store: store, pepper: []byte(pepper)}, nil
}

// DeviceHash is HMAC-SHA256(pepper, deviceID), hex encoded.
//
// The raw device id is never stored. It is a quasi-identifier — it links one
// device's visits — so a Redis dump must not yield a cross-event correlator
// (api-contracts §9).
func DeviceHash(pepper, deviceID string) string {
	m := hmac.New(sha256.New, []byte(pepper))
	m.Write([]byte(deviceID))
	return hex.EncodeToString(m.Sum(nil))
}

// Verify checks the grant token and, for guests, the device binding.
//
// Order matters and is not arbitrary: the token is resolved before the device
// is considered, because a device check against an unknown grant would answer
// DEVICE_MISMATCH and so confirm that some *other* grant exists for that
// device. Unknown grant is always GRANT_TOKEN_INVALID.
func (v *Verifier) Verify(ctx context.Context, grantToken, deviceID string) (Grant, error) {
	if grantToken == "" {
		return Grant{}, apperror.
			ErrUnauthorized("X-Grant-Token is required").
			WithCode(apperror.CodeGrantTokenMissing)
	}

	fields, err := v.store.Grant(ctx, grantToken)
	if errors.Is(err, ErrNotFound) {
		// Unknown, expired, or revoked. One answer for all three.
		return Grant{}, apperror.
			ErrUnauthorized("grant is not valid").
			WithCode(apperror.CodeGrantTokenInvalid)
	}
	if err != nil {
		// A broken Redis is NOT an invalid grant. Answering 401 here would log
		// customers out of a paid-for seat during an infrastructure blip.
		return Grant{}, apperror.ErrServiceUnavailable("cannot verify grant right now")
	}

	g := Grant{
		Token:      grantToken,
		EventID:    fields[FieldEventID],
		BookingID:  fields[FieldBookingID],
		QueueToken: fields[FieldQueueToken],
		AccountID:  fields[FieldAccountID],
		DeviceHash: fields[FieldDeviceHash],
	}

	// booking_id is what §4/§5 bind on. A hash without it is a malformed grant
	// written by a producer that predates ADR-006 §8.13, not a client problem —
	// fail closed rather than let the request match a NULL column.
	if g.BookingID == "" {
		return Grant{}, apperror.
			ErrUnauthorized("grant is not valid").
			WithCode(apperror.CodeGrantTokenInvalid)
	}

	if !g.IsGuest() {
		// Account-bound: valid from any device. On §4/§5 no Authorization is
		// sent, so there is no Bearer to compare account_id against; ownership
		// comes from the SQL clause instead (ADR-006 §8.9).
		return g, nil
	}

	if deviceID == "" {
		return Grant{}, apperror.
			ErrBadRequest("X-Device-Id is required for guest checkout").
			WithCode(apperror.CodeDeviceIDRequired)
	}
	// Constant time: this value can be retried from a script, and a
	// byte-by-byte comparison that exits early leaks the correct prefix.
	want := DeviceHash(string(v.pepper), deviceID)
	if !hmac.Equal([]byte(want), []byte(g.DeviceHash)) {
		return Grant{}, apperror.
			ErrUnauthorized("grant was issued to a different device").
			WithCode(apperror.CodeDeviceMismatch)
	}
	return g, nil
}

type ctxKey struct{}

// NewContext stores a verified grant.
func NewContext(ctx context.Context, g Grant) context.Context {
	return context.WithValue(ctx, ctxKey{}, g)
}

// FromContext returns the grant placed by Middleware.
func FromContext(ctx context.Context) (Grant, bool) {
	g, ok := ctx.Value(ctxKey{}).(Grant)
	return g, ok
}

// Middleware verifies the grant and stores it on the request context. onError
// renders an *apperror.AppError in the service's own envelope, so this package
// does not depend on a response writer.
func (v *Verifier) Middleware(onError func(http.ResponseWriter, *http.Request, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			g, err := v.Verify(r.Context(), r.Header.Get(HeaderGrantToken), r.Header.Get(HeaderDeviceID))
			if err != nil {
				onError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), g)))
		})
	}
}
