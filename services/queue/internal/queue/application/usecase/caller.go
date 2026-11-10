package usecase

import (
	"crypto/subtle"
	"errors"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

// caller answers "who is asking?" the same way for join and /queue/me: an access token gives an account, a device id
// gives a guest, and a queue token is valid only for its owner (ADR-006 §8.9).
type caller struct {
	tokens  outbound.TokenVerifier
	metrics outbound.Metrics
	pepper  []byte
}

// identity verifies the Bearer token (if any) and requires a device id for a guest.
func (c caller) identity(bearer, deviceID string) (accountID string, err error) {
	if bearer != "" {
		claims, verr := c.tokens.Verify(bearer)
		switch {
		case errors.Is(verr, token.ErrExpired):
			return "", apperror.ErrUnauthorized("Sesi sudah kedaluwarsa").WithCode(domain.CodeTokenExpired)
		case verr != nil:
			return "", apperror.ErrUnauthorized("Sesi tidak valid").WithCode(domain.CodeTokenInvalid)
		}
		accountID = claims.Subject
	}
	if deviceID == "" && accountID == "" {
		return "", apperror.ErrBadRequest("X-Device-Id wajib untuk pembeli tamu").WithCode(domain.CodeDeviceIDRequired)
	}
	return accountID, nil
}

// deviceHash is HMAC(DEVICE_PEPPER, device id); empty for no device id. One definition, shared with booking's
// grantauth, which recomputes it from X-Device-Id on every booking request.
func (c caller) deviceHash(deviceID string) string { return grantauth.DeviceHash(c.pepper, deviceID) }

// owns rejects a presented token of this event that is not the caller's: a logged-in token needs the same account;
// a guest token, presented by a guest, needs the same device. A guest token with a logged-in caller is not an error.
func (c caller) owns(e domain.Engagement, accountID, deviceHash string) error {
	if e.AccountID != "" {
		if e.AccountID != accountID {
			c.metrics.Incr(domain.MetricTokenAccountMismatch)
			return errForeignToken(domain.CodeUnauthorized, "Token antrian milik akun lain")
		}
		return nil
	}
	if accountID == "" && !sameDevice(e.DeviceHash, deviceHash) {
		c.metrics.Incr(domain.MetricDeviceMismatch)
		return errForeignToken(domain.CodeDeviceMismatch, "Token antrian terdaftar di perangkat lain")
	}
	return nil
}

// sameDevice compares device hashes in constant time. A token without a device_hash matches no device (fail closed).
func sameDevice(stored, presented string) bool {
	return stored != "" && subtle.ConstantTimeCompare([]byte(stored), []byte(presented)) == 1
}
