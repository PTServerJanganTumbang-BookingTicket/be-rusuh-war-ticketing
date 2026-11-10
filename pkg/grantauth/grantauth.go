// Package grantauth checks the grant on every booking and payment endpoint (api-contracts §4, §5).
//
// X-Grant-Token proves the caller holds a slot, and for a guest X-Device-Id proves it is the device that
// received it (ADR-006 §8.9, no rebind). The middleware reads grant:<token> (written by admission-ctl,
// ADR-006 §8.13) and puts the Grant in the request context.
//
// A grant belonging to a logged-in account additionally requires Authorization: Bearer for the SAME
// account (ADR-006 §8.17, contract v2.20). A leaked or copied grant must not be enough to buy in
// someone's name. The session only CHECKS: bookings.account_id still comes from the grant, so a dead
// session is a visible error rather than the wrong attribution.
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
	// CodeUnauthorized is Appendix A's UNAUTHORIZED for an account grant whose
	// Authorization header is missing or belongs to another account. TOKEN_EXPIRED
	// and TOKEN_INVALID are apperror's: they are the same two codes §1.4 uses, and
	// one definition per wire string is the point.
	CodeUnauthorized = "UNAUTHORIZED"
)

// Request headers.
const (
	HeaderGrantToken    = "X-Grant-Token"
	HeaderDeviceID      = "X-Device-Id"
	HeaderAuthorization = "Authorization"
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

// Config holds the device pepper, the same DEVICE_PEPPER queue's join uses, and the session verifier
// for ADR-006 §8.17.
//
// Sessions is required: without it an account grant cannot be checked, and the middleware answers 503
// rather than letting the request through. Failing closed is the whole point of §8.17 — a guard that
// skips the check when misconfigured is the hole it was written to close. Guest grants never touch it.
type Config struct {
	DevicePepper []byte
	Sessions     *token.Issuer
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
// 401 DEVICE_MISMATCH; account grant without a Bearer for the same account → 401 UNAUTHORIZED /
// TOKEN_EXPIRED / TOKEN_INVALID (§8.17). Redis unreachable → 503: no credential check, no access.
//
// The session is checked LAST, after grant and device, as §8.17 specifies: the weaker credential
// cannot be used to probe the stronger one, and a guest never needs a session at all.
func Middleware(rdb goredis.UniversalClient, cfg Config) func(http.Handler) http.Handler {
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
		// A guest grant ignores Authorization entirely (§8.17): a token sent
		// alongside one is not an error, it is simply not what binds the grant.
		return g, nil
	}
	if err := checkSession(r, g, cfg); err != nil {
		return Grant{}, err
	}
	return g, nil
}

// checkSession enforces ADR-006 §8.17 for an account grant.
//
// It verifies the session and nothing else: the account id used for attribution is already on the
// grant, written by admission-ctl from the queue token. v2.3 took it from the header instead, and a
// stale header meant a booking attributed to the wrong account; here a stale session is a 401 the
// client can act on by refreshing (§1.4).
func checkSession(r *http.Request, g Grant, cfg Config) error {
	if cfg.Sessions == nil {
		// Fail closed. Letting the request through would silently restore
		// exactly the gap §8.17 closes, and would do it on the path where a
		// leaked grant is worth the most.
		slog.Error("grantauth: no session verifier configured; cannot check an account grant",
			slog.String("account_id", g.AccountID))
		return apperror.ErrServiceUnavailable("Layanan sedang sibuk, coba lagi").WithCode(apinizer.ErrorCodeInternal)
	}
	raw, ok := bearer(r.Header.Get(HeaderAuthorization))
	if !ok {
		return apperror.ErrUnauthorized("Authorization wajib untuk grant milik akun").WithCode(CodeUnauthorized)
	}
	claims, err := cfg.Sessions.Verify(raw)
	switch {
	case errors.Is(err, token.ErrExpired):
		return apperror.ErrUnauthorized("Sesi kedaluwarsa, muat ulang lalu coba lagi").WithCode(apperror.CodeTokenExpired)
	case err != nil:
		return apperror.ErrUnauthorized("Sesi tidak sah").WithCode(apperror.CodeTokenInvalid)
	}
	// Another account's valid session answers UNAUTHORIZED, not TOKEN_INVALID:
	// the token is fine, it is the pairing that is wrong, and the client's fix
	// is to log in as the right account rather than to refresh.
	if subtle.ConstantTimeCompare([]byte(claims.Subject), []byte(g.AccountID)) != 1 {
		slog.Warn("grantauth: session does not match the grant's account")
		return apperror.ErrUnauthorized("Sesi bukan milik akun pemegang grant").WithCode(CodeUnauthorized)
	}
	return nil
}

// bearer extracts the token from an Authorization header, case-insensitively on the scheme as RFC 7235
// requires. An empty header and a header with another scheme both read as absent.
func bearer(header string) (string, bool) {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	raw := strings.TrimSpace(header[len(prefix):])
	return raw, raw != ""
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
