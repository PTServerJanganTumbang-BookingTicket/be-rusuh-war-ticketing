// Package grantauth checks the grant on every booking and payment endpoint (api-contracts §4, §5).
//
// X-Grant-Token proves the caller holds a slot. A guest also sends X-Device-Id, which proves it is the device that
// received the slot (ADR-006 §8.9, no rebind). A grant that belongs to an account also needs that account's access
// token in Authorization: Bearer, so a leaked grant alone is not enough (ADR-006 §8.17). The middleware reads
// grant:<token> (written by admission-ctl, ADR-006 §8.13) and puts the Grant in the request context.
//
// It does not decide access alone: the hash can outlive its booking, so every use case also checks the
// booking row (docs/05-booking/README.md §3).
package grantauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
)

// Contract codes (api-contracts Appendix A).
const (
	CodeGrantTokenMissing = "GRANT_TOKEN_MISSING"
	CodeGrantTokenInvalid = "GRANT_TOKEN_INVALID"
	CodeDeviceIDRequired  = "DEVICE_ID_REQUIRED"
	CodeDeviceMismatch    = "DEVICE_MISMATCH"
	CodeUnauthorized      = "UNAUTHORIZED"
	CodeTokenExpired      = "TOKEN_EXPIRED"
	CodeTokenInvalid      = "TOKEN_INVALID"
)

// Request headers.
const (
	HeaderGrantToken = "X-Grant-Token"
	HeaderDeviceID   = "X-Device-Id"
)

// Grant is the content of grant:<token>. Exactly one of AccountID (logged-in) and DeviceHash (guest) is set.
type Grant struct {
	Token      string
	EventID    string
	BookingID  string
	QueueToken string
	AccountID  string
	DeviceHash string
}

// TokenVerifier checks an access token; *token.Issuer is one. It errors with token.ErrExpired or token.ErrInvalid.
type TokenVerifier interface {
	Verify(raw string) (token.Claims, error)
}

// Config holds the device pepper (the same DEVICE_PEPPER queue's join uses) and the verifier of access tokens for
// account grants (the same JWT_SECRET user-service signs with).
type Config struct {
	DevicePepper []byte
	Tokens       TokenVerifier
}

type ctxKey struct{}

// ErrMissing is the 401 for a request without X-Grant-Token.
func ErrMissing() error {
	return apperror.ErrUnauthorized("X-Grant-Token wajib").WithCode(CodeGrantTokenMissing)
}

// ErrInvalid is the 401 for a grant that is unknown, expired, malformed, or no longer usable (its booking ended, or it
// belongs to another event). One answer for all of them: the client gets a fresh state from GET /queue/me.
func ErrInvalid() error {
	return apperror.ErrUnauthorized("Grant tidak dikenal atau kedaluwarsa").WithCode(CodeGrantTokenInvalid)
}

// Middleware verifies the grant and calls next with the Grant in the context. Checks, in order:
// missing header → 401 GRANT_TOKEN_MISSING; no hash, or a hash with both or neither identity → 401
// GRANT_TOKEN_INVALID; guest without X-Device-Id → 400 DEVICE_ID_REQUIRED; guest from another device →
// 401 DEVICE_MISMATCH (a guest grant ignores Authorization); account grant without Authorization, or with the
// session of another account → 401 UNAUTHORIZED, an expired token → 401 TOKEN_EXPIRED, an unreadable or badly signed
// one → 401 TOKEN_INVALID. Redis unreachable → 503: no credential check, no access.
//
// cfg.Tokens is required: it panics at construction without it, so a missing JWT_SECRET fails at start, not on the
// first account request.
func Middleware(rdb goredis.UniversalClient, cfg Config) func(http.Handler) http.Handler {
	if cfg.Tokens == nil {
		panic("grantauth: Config.Tokens is required (account grants need their access token checked)")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			g, err := verify(r, rdb, cfg)
			if err != nil {
				apinizer.WriteError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithGrant(r.Context(), g)))
		})
	}
}

func verify(r *http.Request, rdb goredis.UniversalClient, cfg Config) (Grant, error) {
	token := r.Header.Get(HeaderGrantToken)
	if token == "" {
		return Grant{}, ErrMissing()
	}
	h, err := rdb.HGetAll(r.Context(), rediskeys.Grant(token)).Result()
	if err != nil {
		slog.Warn("grantauth: cannot read the grant", slog.Any("err", err))
		return Grant{}, apperror.ErrServiceUnavailable("Layanan sedang sibuk, coba lagi").WithCode(apinizer.ErrorCodeInternal)
	}
	invalid := ErrInvalid()
	if len(h) == 0 {
		return Grant{}, invalid
	}
	g := Grant{
		Token:      token,
		EventID:    h[rediskeys.FieldEventID],
		BookingID:  h[rediskeys.FieldBookingID],
		QueueToken: h[rediskeys.FieldQueueToken],
		AccountID:  h[rediskeys.FieldAccountID],
		DeviceHash: h[rediskeys.FieldDeviceHash],
	}
	if g.EventID == "" || g.BookingID == "" || g.QueueToken == "" || (g.AccountID == "") == (g.DeviceHash == "") {
		// admission-ctl writes all three ids and exactly one identity; anything else is a bug or tampering.
		slog.Error("grantauth: malformed grant hash", slog.Bool("account", g.AccountID != ""), slog.Bool("device", g.DeviceHash != ""))
		return Grant{}, invalid
	}
	if g.DeviceHash != "" {
		device := r.Header.Get(HeaderDeviceID)
		if device == "" {
			return Grant{}, apperror.ErrBadRequest("X-Device-Id wajib untuk pembeli tamu").WithCode(CodeDeviceIDRequired)
		}
		if subtle.ConstantTimeCompare([]byte(g.DeviceHash), []byte(DeviceHash(cfg.DevicePepper, device))) != 1 {
			return Grant{}, apperror.ErrUnauthorized("Grant terdaftar di perangkat lain").WithCode(CodeDeviceMismatch)
		}
		return g, nil
	}
	return g, checkSession(r, g, cfg.Tokens)
}

// checkSession is the second condition of an account grant: the access token must belong to the account the grant was
// issued to. The token only checks; the booking's account stays the grant's (api-contracts §4).
func checkSession(r *http.Request, g Grant, tokens TokenVerifier) error {
	raw, ok := BearerToken(r.Header.Get("Authorization"))
	if !ok {
		return apperror.ErrUnauthorized("Sesi tidak valid").WithCode(CodeTokenInvalid)
	}
	if raw == "" {
		return apperror.ErrUnauthorized("Login diperlukan untuk grant ini").WithCode(CodeUnauthorized)
	}
	claims, err := tokens.Verify(raw)
	switch {
	case errors.Is(err, token.ErrExpired):
		return apperror.ErrUnauthorized("Sesi sudah kedaluwarsa").WithCode(CodeTokenExpired)
	case err != nil:
		return apperror.ErrUnauthorized("Sesi tidak valid").WithCode(CodeTokenInvalid)
	}
	if subtle.ConstantTimeCompare([]byte(claims.Subject), []byte(g.AccountID)) != 1 {
		slog.Warn("grantauth: session of another account", slog.String("metric", "GrantAccountMismatch"))
		return apperror.ErrUnauthorized("Grant milik akun lain").WithCode(CodeUnauthorized)
	}
	return nil
}

// BearerToken reads an Authorization header. No header is ("", true); "Bearer <token>" (scheme case-insensitive,
// token trimmed) is (token, true); anything else, including "Bearer" with no token, is ("", false). Queue's join and
// /queue/me read the header the same way.
func BearerToken(h string) (string, bool) {
	if h == "" {
		return "", true
	}
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	return tok, tok != ""
}

// WithGrant returns ctx carrying g, as the middleware does. For handler tests.
func WithGrant(ctx context.Context, g Grant) context.Context {
	return context.WithValue(ctx, ctxKey{}, g)
}

// FromContext returns the Grant the middleware stored.
func FromContext(ctx context.Context) (Grant, bool) {
	g, ok := ctx.Value(ctxKey{}).(Grant)
	return g, ok
}

// DeviceHash is HMAC-SHA256(pepper, deviceID) in hex: the same value queue's join stores as device_hash.
// An empty device id hashes to empty, which matches no stored hash.
func DeviceHash(pepper []byte, deviceID string) string {
	if deviceID == "" {
		return ""
	}
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(deviceID))
	return hex.EncodeToString(m.Sum(nil))
}
