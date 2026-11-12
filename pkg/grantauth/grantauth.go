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
	// CodePaymentSessionInvalid is the 401 for an X-Payment-Session that is unknown, expired, or whose grant is gone.
	CodePaymentSessionInvalid = "PAYMENT_SESSION_INVALID"
)

// Request headers.
const (
	HeaderGrantToken = "X-Grant-Token"
	HeaderDeviceID   = "X-Device-Id"
	// HeaderPaymentSession carries the WebView payment page's session (ADR-007), sent instead of the grant headers.
	HeaderPaymentSession = "X-Payment-Session"
)

// Grant is the content of grant:<token>. Exactly one of AccountID (logged-in) and DeviceHash (guest) is set.
type Grant struct {
	Token      string
	EventID    string
	BookingID  string
	QueueToken string
	AccountID  string
	DeviceHash string
	// ViaSession is true when the request came with X-Payment-Session instead of X-Grant-Token. For logs and metrics
	// only: nothing may branch on it, the grant is the same either way.
	ViaSession bool
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
//
// X-Payment-Session is refused here with 400 VALIDATION_ERROR, before Redis is read: the routes behind this middleware
// (create a booking, the ticket page, mint a handoff code) are the ones a WebView session must never reach. The
// routes that accept it use MiddlewareWithSession.
func Middleware(rdb goredis.UniversalClient, cfg Config) func(http.Handler) http.Handler {
	return build(cfg, func(r *http.Request) (Grant, error) {
		if r.Header.Get(HeaderPaymentSession) != "" {
			return Grant{}, apperror.ErrBadRequest("X-Payment-Session tidak diterima di endpoint ini").WithCode(apperror.CodeValidationError)
		}
		return verify(r, rdb, cfg)
	})
}

// MiddlewareWithSession is Middleware that also accepts X-Payment-Session instead of the grant headers (ADR-007,
// docs/07-payment/06-grant-guard.md §12). It is mounted on GET /bookings/{booking_id} and §5.1–§5.4 only.
//
// The session resolves to its grant, which is read on every request: a deleted grant ends the session at once. The
// device binding and the account session are not checked on this path; they were checked when the app asked for the
// handoff code, and a stray X-Device-Id or Authorization next to a session is ignored. A session together with
// X-Grant-Token is 400 VALIDATION_ERROR (one credential per request). An unknown session and a session whose grant is
// gone or malformed are the same 401 PAYMENT_SESSION_INVALID. Redis unreachable → 503.
func MiddlewareWithSession(rdb goredis.UniversalClient, cfg Config) func(http.Handler) http.Handler {
	return build(cfg, func(r *http.Request) (Grant, error) {
		session := r.Header.Get(HeaderPaymentSession)
		if session == "" {
			return verify(r, rdb, cfg)
		}
		if r.Header.Get(HeaderGrantToken) != "" {
			return Grant{}, apperror.ErrBadRequest("Kirim X-Payment-Session atau X-Grant-Token, tidak keduanya").WithCode(apperror.CodeValidationError)
		}
		return verifySession(r, rdb, session)
	})
}

func build(cfg Config, check func(*http.Request) (Grant, error)) func(http.Handler) http.Handler {
	if cfg.Tokens == nil {
		panic("grantauth: Config.Tokens is required (account grants need their access token checked)")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			g, err := check(r)
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
	g, ok, err := readGrant(r.Context(), rdb, token)
	if err != nil {
		return Grant{}, err
	}
	if !ok {
		return Grant{}, ErrInvalid()
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

// verifySession resolves a payment session to its grant. The grant must still exist and be well formed.
func verifySession(r *http.Request, rdb goredis.UniversalClient, session string) (Grant, error) {
	invalid := apperror.ErrUnauthorized("Sesi pembayaran tidak berlaku").WithCode(CodePaymentSessionInvalid)
	token, err := rdb.Get(r.Context(), rediskeys.PaymentSession(session)).Result()
	if errors.Is(err, goredis.Nil) || (err == nil && token == "") {
		return Grant{}, invalid
	}
	if err != nil {
		return Grant{}, unavailable(err)
	}
	g, ok, err := readGrant(r.Context(), rdb, token)
	if err != nil {
		return Grant{}, err
	}
	if !ok {
		return Grant{}, invalid
	}
	g.ViaSession = true
	return g, nil
}

// readGrant reads grant:<token>. ok is false for a missing hash and for a malformed one (admission-ctl writes all three
// ids and exactly one identity; anything else is a bug or tampering, logged).
func readGrant(ctx context.Context, rdb goredis.UniversalClient, token string) (Grant, bool, error) {
	h, err := rdb.HGetAll(ctx, rediskeys.Grant(token)).Result()
	if err != nil {
		return Grant{}, false, unavailable(err)
	}
	if len(h) == 0 {
		return Grant{}, false, nil
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
		slog.Error("grantauth: malformed grant hash", slog.Bool("account", g.AccountID != ""), slog.Bool("device", g.DeviceHash != ""))
		return Grant{}, false, nil
	}
	return g, true, nil
}

func unavailable(err error) error {
	slog.Warn("grantauth: cannot read Redis", slog.Any("err", err))
	return apperror.ErrServiceUnavailable("Layanan sedang sibuk, coba lagi").WithCode(apinizer.ErrorCodeInternal)
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
