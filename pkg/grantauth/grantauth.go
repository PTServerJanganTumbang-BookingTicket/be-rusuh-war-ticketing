// Package grantauth checks the grant on every booking and payment endpoint (api-contracts §4, §5).
//
// The grant is the only credential there: X-Grant-Token proves the caller holds a slot, and for a guest
// X-Device-Id proves it is the device that received it (ADR-006 §8.9, no rebind). The middleware reads
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
	"log/slog"
	"net/http"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

// Contract codes (api-contracts Appendix A).
const (
	CodeGrantTokenMissing = "GRANT_TOKEN_MISSING"
	CodeGrantTokenInvalid = "GRANT_TOKEN_INVALID"
	CodeDeviceIDRequired  = "DEVICE_ID_REQUIRED"
	CodeDeviceMismatch    = "DEVICE_MISMATCH"
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

// Config holds the device pepper, the same DEVICE_PEPPER queue's join uses.
type Config struct {
	DevicePepper []byte
}

type ctxKey struct{}

// Middleware verifies the grant and calls next with the Grant in the context. Checks, in order:
// missing header → 401 GRANT_TOKEN_MISSING; no hash, or a hash with both or neither identity → 401
// GRANT_TOKEN_INVALID; guest without X-Device-Id → 400 DEVICE_ID_REQUIRED; guest from another device →
// 401 DEVICE_MISMATCH. Redis unreachable → 503: no credential check, no access.
func Middleware(rdb goredis.UniversalClient, cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			g, err := verify(r, rdb, cfg)
			if err != nil {
				apinizer.WriteError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, g)))
		})
	}
}

func verify(r *http.Request, rdb goredis.UniversalClient, cfg Config) (Grant, error) {
	token := r.Header.Get(HeaderGrantToken)
	if token == "" {
		return Grant{}, apperror.ErrUnauthorized("X-Grant-Token wajib").WithCode(CodeGrantTokenMissing)
	}
	h, err := rdb.HGetAll(r.Context(), rediskeys.Grant(token)).Result()
	if err != nil {
		slog.Warn("grantauth: cannot read the grant", slog.Any("err", err))
		return Grant{}, apperror.ErrServiceUnavailable("Layanan sedang sibuk, coba lagi").WithCode(apinizer.ErrorCodeInternal)
	}
	invalid := apperror.ErrUnauthorized("Grant tidak dikenal atau kedaluwarsa").WithCode(CodeGrantTokenInvalid)
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
	}
	return g, nil
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
