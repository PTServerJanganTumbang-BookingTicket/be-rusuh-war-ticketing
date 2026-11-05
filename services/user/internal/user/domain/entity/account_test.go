package entity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAccount_IsNIKVerified(t *testing.T) {
	now := time.Now().UTC()
	assert.True(t, Account{NIKHmac: "h", NIKVerifiedAt: &now}.IsNIKVerified())
	// BR-11: an account without a verified NIK cannot purchase. Each half
	// alone must not read as verified.
	assert.False(t, Account{NIKHmac: "h"}.IsNIKVerified())
	assert.False(t, Account{NIKVerifiedAt: &now}.IsNIKVerified())
	assert.False(t, Account{}.IsNIKVerified())
}

func TestAccount_IsEmailVerified(t *testing.T) {
	now := time.Now().UTC()
	assert.True(t, Account{EmailVerifiedAt: &now}.IsEmailVerified())
	assert.False(t, Account{}.IsEmailVerified())
}

func TestMagicToken_IsExpired(t *testing.T) {
	assert.True(t, MagicToken{ExpiresAt: time.Now().UTC().Add(-time.Minute)}.IsExpired())
	assert.False(t, MagicToken{ExpiresAt: time.Now().UTC().Add(time.Minute)}.IsExpired())
}

func TestMagicToken_IsUsed(t *testing.T) {
	now := time.Now().UTC()
	assert.True(t, MagicToken{UsedAt: &now}.IsUsed())
	assert.False(t, MagicToken{}.IsUsed())
}

func TestRefreshToken_Lifecycle(t *testing.T) {
	now := time.Now().UTC()
	live := RefreshToken{ExpiresAt: now.Add(RefreshTokenTTL)}
	assert.False(t, live.IsExpired())
	assert.False(t, live.IsUsed())
	assert.False(t, live.IsRevoked())

	assert.True(t, RefreshToken{ExpiresAt: now.Add(-time.Minute)}.IsExpired())
	assert.True(t, RefreshToken{UsedAt: &now}.IsUsed())
	assert.True(t, RefreshToken{RevokedAt: &now}.IsRevoked())
}

func TestRefreshTokenTTL_OutlivesTheAccessToken(t *testing.T) {
	// The access token lives five minutes (contract §1.3). If refresh were not
	// substantially longer it would not keep anyone logged in through a sale.
	assert.Greater(t, RefreshTokenTTL, time.Hour)
}

func TestEngagement_IsActive(t *testing.T) {
	assert.False(t, Engagement{}.IsActive())
	assert.False(t, Engagement{Kind: EngagementNone}.IsActive())
	assert.True(t, Engagement{Kind: EngagementQueue, EventID: "evt"}.IsActive())
	assert.True(t, Engagement{Kind: EngagementBooking, BookingID: "RB-1"}.IsActive())
}

func TestPurposesAreDistinct(t *testing.T) {
	// A verification link redeemable as a login link is an auth bypass.
	assert.NotEqual(t, PurposeLogin, PurposeVerifyEmail)
}
