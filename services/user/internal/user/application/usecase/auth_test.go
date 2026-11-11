package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	mock_outbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/_mock/user/port/outbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

const testNIK = "3171000000000022"

func verifiedAccount() entity.Account {
	now := time.Now().UTC()
	return entity.Account{
		ID:            testAccountID,
		Email:         "user@example.com",
		NIKHmac:       "hmac-abc",
		NIKMasked:     "3171**********22",
		NIKVerifiedAt: &now,
	}
}

func goodVerification() portout.NIKVerification {
	return portout.NIKVerification{
		NIKHmac: "hmac-abc", NIKMasked: "3171**********22",
		Valid: true, Status: "ACTIVE", Source: "verified", Ref: "ref-1",
	}
}

// ---- RequestMagicLink ----

func TestRequestMagicLink_StoresHashNotToken(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.allowRateLimit()
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), "user@example.com").Return(verifiedAccount(), nil)

	var stored entity.MagicToken
	var queued entity.OutboxEvent
	d.repo.EXPECT().SaveMagicTokenWithEvent(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, mt entity.MagicToken, e entity.OutboxEvent) error {
			stored, queued = mt, e
			return nil
		})

	require.NoError(t, uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "user@example.com"}))

	assert.Empty(t, stored.Token, "the plaintext token must never be persisted")
	assert.Equal(t, entity.PurposeLogin, stored.Purpose)
	assert.Equal(t, events.TypeAuthMagicLink, queued.Type)

	// The email carries the plaintext; the row carries only its hash.
	var payload events.AuthMagicLinkPayload
	require.NoError(t, json.Unmarshal(queued.Payload, &payload))
	assert.NotEmpty(t, payload.Token)
	assert.Equal(t, hashSecret(payload.Token), stored.TokenHash)
	assert.Equal(t, "user@example.com", payload.Email)
}

func TestRequestMagicLink_NormalisesEmail(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.allowRateLimit()
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), "user@example.com").Return(verifiedAccount(), nil)
	d.repo.EXPECT().SaveMagicTokenWithEvent(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "  User@Example.COM "}))
}

func TestRequestMagicLink_UnknownAddressStillSucceeds(t *testing.T) {
	// Behaving differently for unknown addresses enumerates who is registered.
	uc, d := newTestUsecase(t)
	d.allowRateLimit()
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(entity.Account{}, apperror.ErrNotFound("nope"))
	d.repo.EXPECT().SaveMagicTokenWithEvent(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	assert.NoError(t, uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "ghost@example.com"}))
}

func TestRequestMagicLink_RateLimited(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), MagicLinkLimit, MagicLinkWindow).
		Return(false, 90*time.Second, nil)

	err := uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "user@example.com"})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 429, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeTooManyRequests, appErr.ErrorCode)
	assert.Equal(t, 90*time.Second, appErr.RetryAfter)
}

func TestRequestMagicLink_LimiterFailureFailsOpen(t *testing.T) {
	// The limiter guards against abuse; letting it take down login would turn
	// a Redis blip into an outage.
	uc, d := newTestUsecase(t)
	d.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(false, time.Duration(0), errors.New("redis down"))
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(verifiedAccount(), nil)
	d.repo.EXPECT().SaveMagicTokenWithEvent(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	assert.NoError(t, uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "user@example.com"}))
}

func TestRequestMagicLink_DoesNotTalkToAnEmailProvider(t *testing.T) {
	// NFR-10: an email provider outage must not fail the request. The use case
	// has no mailer at all — it writes an outbox row and returns, so there is
	// nothing here that SES being down could break.
	uc, d := newTestUsecase(t)
	d.allowRateLimit()
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(verifiedAccount(), nil)
	d.repo.EXPECT().SaveMagicTokenWithEvent(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	assert.NoError(t, uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "user@example.com"}))
}

func TestRequestMagicLink_OutboxWriteFailureFailsTheRequest(t *testing.T) {
	// The opposite of the above: if the row cannot be written, no email will
	// ever be sent, so reporting success would be a lie.
	uc, d := newTestUsecase(t)
	d.allowRateLimit()
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(verifiedAccount(), nil)
	d.repo.EXPECT().SaveMagicTokenWithEvent(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("db down"))

	err := uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "user@example.com"})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 500, appErr.HTTPStatus)
}

// ---- VerifyMagicLink ----

func TestVerifyMagicLink_Success(t *testing.T) {
	uc, d := newTestUsecase(t)
	account := verifiedAccount()
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), hashSecret("raw"), entity.PurposeLogin).
		Return(entity.MagicToken{ID: "mt-1", Email: account.Email, Purpose: entity.PurposeLogin}, nil)
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), account.Email).Return(account, nil)
	d.repo.EXPECT().MarkEmailVerified(gomock.Any(), account.ID).Return(nil)
	d.issuesSession()
	d.noEngagement()

	resp, err := uc.VerifyMagicLink(context.Background(), "raw")
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken, "a session without refresh dies in five minutes")
	assert.Equal(t, 22740, resp.ExpiresIn, "the session must outlive the 15.5-minute journey")
	assert.Equal(t, account.ID, resp.User.ID)
	assert.Equal(t, "3171**********22", resp.User.NIK, "the summary carries the MASKED NIK")

	claims, err := d.tokens.Verify(resp.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, account.ID, claims.Subject)
	assert.True(t, claims.NIKVerified)
}

func TestVerifyMagicLink_DoesNotCreateAnAccount(t *testing.T) {
	// The blocking gap this closes: creating an account here produced one with
	// no NIK and no consent, which could then store other people's NIKs
	// (FR-31a, FR-31c, BR-11). No CreateAccount expectation is registered, so
	// gomock fails the test if one is attempted.
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeLogin).
		Return(entity.MagicToken{ID: "mt-1", Email: "ghost@example.com"}, nil)
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), "ghost@example.com").
		Return(entity.Account{}, apperror.ErrNotFound("account not found"))

	_, err := uc.VerifyMagicLink(context.Background(), "raw")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

func TestVerifyMagicLink_UnusableToken(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeLogin).
		Return(entity.MagicToken{}, apperror.ErrNotFound("token is not usable"))

	_, err := uc.VerifyMagicLink(context.Background(), "spent")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeTokenInvalid, appErr.ErrorCode)
}

func TestVerifyMagicLink_StoresTheRefreshTokenHashedInANewFamily(t *testing.T) {
	uc, d := newTestUsecase(t)
	account := verifiedAccount()
	verifiedAt := time.Now().UTC()
	account.EmailVerifiedAt = &verifiedAt
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeLogin).
		Return(entity.MagicToken{Email: account.Email}, nil)
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(account, nil)

	var stored entity.RefreshToken
	d.repo.EXPECT().CreateRefreshToken(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, rt entity.RefreshToken) error { stored = rt; return nil })
	d.noEngagement()

	resp, err := uc.VerifyMagicLink(context.Background(), "raw")
	require.NoError(t, err)

	assert.Empty(t, stored.Token, "the plaintext must never be persisted")
	assert.Equal(t, hashSecret(resp.RefreshToken), stored.TokenHash)
	assert.Equal(t, account.ID, stored.AccountID)
	assert.NotEmpty(t, stored.FamilyID, "each login opens its own chain, so revoking one does not sign out other devices")
	assert.True(t, stored.ExpiresAt.After(time.Now().UTC().Add(29*24*time.Hour)))
}

func TestVerifyMagicLink_RedirectContextFromActiveBooking(t *testing.T) {
	// BR-09: a re-entrant customer returns to the page they were on.
	uc, d := newTestUsecase(t)
	account := verifiedAccount()
	verifiedAt := time.Now().UTC()
	account.EmailVerifiedAt = &verifiedAt
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeLogin).
		Return(entity.MagicToken{Email: account.Email}, nil)
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(account, nil)
	d.issuesSession()
	d.engagement.EXPECT().FindActiveEngagement(gomock.Any(), account.ID).Return(entity.Engagement{
		Kind: entity.EngagementBooking, EventID: "evt_konser_a", BookingID: "RB-00912",
	}, nil)

	resp, err := uc.VerifyMagicLink(context.Background(), "raw")
	require.NoError(t, err)
	require.NotNil(t, resp.RedirectContext)
	assert.Equal(t, "booking", resp.RedirectContext.Type)
	assert.Equal(t, "evt_konser_a", resp.RedirectContext.EventID)
	assert.Equal(t, "RB-00912", resp.RedirectContext.BookingID)
}

func TestVerifyMagicLink_RedirectContextFromQueuePosition(t *testing.T) {
	uc, d := newTestUsecase(t)
	account := verifiedAccount()
	verifiedAt := time.Now().UTC()
	account.EmailVerifiedAt = &verifiedAt
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeLogin).
		Return(entity.MagicToken{Email: account.Email}, nil)
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(account, nil)
	d.issuesSession()
	d.engagement.EXPECT().FindActiveEngagement(gomock.Any(), gomock.Any()).Return(entity.Engagement{
		Kind: entity.EngagementQueue, EventID: "evt_konser_a",
	}, nil)

	resp, err := uc.VerifyMagicLink(context.Background(), "raw")
	require.NoError(t, err)
	require.NotNil(t, resp.RedirectContext)
	assert.Equal(t, "queue", resp.RedirectContext.Type)
	assert.Empty(t, resp.RedirectContext.BookingID, "a queue position has no booking yet")
}

func TestVerifyMagicLink_RedirectContextOmittedWhenNothingInFlight(t *testing.T) {
	uc, d := newTestUsecase(t)
	account := verifiedAccount()
	verifiedAt := time.Now().UTC()
	account.EmailVerifiedAt = &verifiedAt
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeLogin).
		Return(entity.MagicToken{Email: account.Email}, nil)
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(account, nil)
	d.issuesSession()
	d.noEngagement()

	resp, err := uc.VerifyMagicLink(context.Background(), "raw")
	require.NoError(t, err)
	assert.Nil(t, resp.RedirectContext, "omitted, not sent empty")
}

func TestVerifyMagicLink_EngagementFailureDoesNotFailTheLogin(t *testing.T) {
	// Landing on the home page instead of a booking is degraded. Failing the
	// login outright is broken.
	uc, d := newTestUsecase(t)
	account := verifiedAccount()
	verifiedAt := time.Now().UTC()
	account.EmailVerifiedAt = &verifiedAt
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeLogin).
		Return(entity.MagicToken{Email: account.Email}, nil)
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(account, nil)
	d.issuesSession()
	d.engagement.EXPECT().FindActiveEngagement(gomock.Any(), gomock.Any()).
		Return(entity.Engagement{}, errors.New("db down"))

	resp, err := uc.VerifyMagicLink(context.Background(), "raw")
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccessToken)
	assert.Nil(t, resp.RedirectContext)
}

func TestVerifyMagicLink_RefreshStoreFailureFailsTheLogin(t *testing.T) {
	// Returning an access token with no refresh token would hand the customer
	// a session that dies in five minutes with no way to extend it.
	uc, d := newTestUsecase(t)
	account := verifiedAccount()
	verifiedAt := time.Now().UTC()
	account.EmailVerifiedAt = &verifiedAt
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeLogin).
		Return(entity.MagicToken{Email: account.Email}, nil)
	d.repo.EXPECT().FindAccountByEmail(gomock.Any(), gomock.Any()).Return(account, nil)
	d.repo.EXPECT().CreateRefreshToken(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	_, err := uc.VerifyMagicLink(context.Background(), "raw")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 500, appErr.HTTPStatus)
}

// ---- RefreshSession ----

func TestRefreshSession_RotatesAndReturnsANewPair(t *testing.T) {
	uc, d := newTestUsecase(t)
	account := verifiedAccount()

	var presented string
	var replacement entity.RefreshToken
	d.repo.EXPECT().RotateRefreshToken(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, hash string, rt entity.RefreshToken) (entity.RefreshToken, error) {
			presented, replacement = hash, rt
			rt.AccountID = account.ID
			rt.FamilyID = "fam-1"
			return rt, nil
		})
	d.repo.EXPECT().FindAccountByID(gomock.Any(), account.ID).Return(account, nil)

	resp, err := uc.RefreshSession(context.Background(), "old-refresh")
	require.NoError(t, err)
	assert.Equal(t, hashSecret("old-refresh"), presented, "the adapter is given the hash, never the plaintext")
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEqual(t, "old-refresh", resp.RefreshToken, "tokens rotate on every use")
	assert.Equal(t, hashSecret(resp.RefreshToken), replacement.TokenHash)
	assert.Equal(t, 22740, resp.ExpiresIn, "the session must outlive the 15.5-minute journey")
}

func TestRefreshSession_ReplayEndsTheSession(t *testing.T) {
	// A replayed token means the credential leaked. The adapter revokes the
	// family; this must not hand back a working session.
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().RotateRefreshToken(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(entity.RefreshToken{}, portout.ErrRefreshTokenReplayed)

	_, err := uc.RefreshSession(context.Background(), "stolen")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeTokenInvalid, appErr.ErrorCode)
}

func TestRefreshSession_UnknownOrExpiredTokenIsTokenExpired(t *testing.T) {
	// Distinct from a replay: no second holder is implied, so the client is
	// told to sign in rather than treated as compromised.
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().RotateRefreshToken(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(entity.RefreshToken{}, apperror.ErrNotFound("refresh token not found"))

	_, err := uc.RefreshSession(context.Background(), "stale")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeTokenExpired, appErr.ErrorCode)
}

func TestRefreshSession_MissingToken(t *testing.T) {
	uc, _ := newTestUsecase(t)
	_, err := uc.RefreshSession(context.Background(), "")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

func TestRefreshSession_RereadsNIKVerificationFromTheAccount(t *testing.T) {
	// A NIK verified since the last refresh must take effect, and one revoked
	// by operations must stop being asserted — so the claim is re-read from
	// the account rather than copied from the old token.
	uc, d := newTestUsecase(t)
	unverified := entity.Account{ID: testAccountID, Email: "user@example.com"}
	d.repo.EXPECT().RotateRefreshToken(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, rt entity.RefreshToken) (entity.RefreshToken, error) {
			rt.AccountID = testAccountID
			return rt, nil
		})
	d.repo.EXPECT().FindAccountByID(gomock.Any(), testAccountID).Return(unverified, nil)

	resp, err := uc.RefreshSession(context.Background(), "old")
	require.NoError(t, err)

	claims, err := d.tokens.Verify(resp.AccessToken)
	require.NoError(t, err)
	assert.False(t, claims.NIKVerified)
}

func TestRefreshSession_DeletedAccountCannotRefresh(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().RotateRefreshToken(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, rt entity.RefreshToken) (entity.RefreshToken, error) {
			rt.AccountID = "USR-gone"
			return rt, nil
		})
	d.repo.EXPECT().FindAccountByID(gomock.Any(), "USR-gone").
		Return(entity.Account{}, apperror.ErrNotFound("account not found"))

	_, err := uc.RefreshSession(context.Background(), "old")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

// ---- VerifyEmail ----

func TestVerifyEmail_RequiresTheVerifyPurpose(t *testing.T) {
	// A verification link must not be redeemable as a login link, or
	// "confirm your email" becomes an authentication bypass.
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), hashSecret("raw"), entity.PurposeVerifyEmail).
		Return(entity.MagicToken{AccountID: testAccountID}, nil)
	d.repo.EXPECT().MarkEmailVerified(gomock.Any(), testAccountID).Return(nil)

	assert.NoError(t, uc.VerifyEmail(context.Background(), "raw"))
}

func TestVerifyEmail_RejectsUnusableToken(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().ConsumeMagicToken(gomock.Any(), gomock.Any(), entity.PurposeVerifyEmail).
		Return(entity.MagicToken{}, apperror.ErrNotFound("nope"))

	err := uc.VerifyEmail(context.Background(), "raw")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

// ---- Register ----

func TestRegister_Success(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.verifier.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(goodVerification(), nil)

	var reg portout.Registration
	d.repo.EXPECT().RegisterAccount(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, r portout.Registration) (entity.Account, error) {
			reg = r
			return r.Account, nil
		})

	resp, err := uc.Register(context.Background(), dto.RegisterRequest{
		Email: "alex@example.com", NIK: testNIK,
		Consent: &dto.ConsentRequest{Accepted: true},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.UserID)
	assert.Equal(t, "hmac-abc", reg.Account.NIKHmac)
	assert.Equal(t, "3171**********22", reg.Account.NIKMasked, "the mask is stored, because a hash cannot be unmasked")
	assert.NotNil(t, reg.Account.NIKVerifiedAt)
	// The consent record says WHO agreed and WHEN, and nothing else. It used to
	// carry the policy version the client posted, which made the client the
	// authority on what the customer had just agreed to — for a record whose
	// only purpose is to be trustworthy evidence (UU PDP No. 27/2022).
	assert.Equal(t, reg.Account.ID, reg.Consent.AccountID)
	assert.NotEmpty(t, reg.Consent.ID)
	assert.False(t, reg.Consent.ConsentedAt.IsZero(), "the timestamp is the server's")
	assert.Equal(t, entity.ConsentRecord{
		ID:          reg.Consent.ID,
		AccountID:   reg.Account.ID,
		ConsentedAt: reg.Consent.ConsentedAt,
	}, reg.Consent, "three fields, and a fourth would have to be justified")

	// All four writes go to one call, so they commit together: an account with
	// a verified NIK and no consent record would be a UU PDP violation, and an
	// account with no queued email never gets verified.
	assert.Equal(t, events.TypeAccountVerify, reg.Event.Type)
	assert.Equal(t, reg.Account.ID, reg.Event.AggregateID)
	assert.Equal(t, entity.PurposeVerifyEmail, reg.VerifyToken.Purpose)
	assert.Equal(t, reg.Account.ID, reg.VerifyToken.AccountID)

	// The emailed link's plaintext is in the event; only its hash is stored.
	var payload events.AccountVerifyPayload
	require.NoError(t, json.Unmarshal(reg.Event.Payload, &payload))
	assert.NotEmpty(t, payload.Token)
	assert.Equal(t, hashSecret(payload.Token), reg.VerifyToken.TokenHash)
	assert.Empty(t, reg.VerifyToken.Token, "the plaintext must never be persisted")
}

// An unchanged client still posts {"accepted":true,"policy_version":"..."}.
// The extra member is ignored by the decoder rather than rejected, so removing
// the field from the contract did not break anyone mid-flight — which is the
// whole reason it is ignored instead of refused.
func TestRegister_APolicyVersionStillSentByAnOldClientIsIgnored(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.verifier.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(goodVerification(), nil)

	var reg portout.Registration
	d.repo.EXPECT().RegisterAccount(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, r portout.Registration) (entity.Account, error) {
			reg = r
			return r.Account, nil
		})

	// Decoded the way the handler decodes it, so the unknown member travels the
	// same path a real request would.
	var req dto.RegisterRequest
	require.NoError(t, json.Unmarshal([]byte(
		`{"email":"alex@example.com","nik":"`+testNIK+`","consent":{"accepted":true,"policy_version":"2026-10-01"}}`), &req))
	assert.True(t, req.Consent.Accepted)

	_, err := uc.Register(context.Background(), req)
	require.NoError(t, err)
	// And it reaches nothing: the record carries no version, so there is no
	// field for a stale client value to land in.
	assert.Equal(t, reg.Account.ID, reg.Consent.AccountID)
}

func TestRegister_ConsentMissingOrDeclined(t *testing.T) {
	// No verifier expectation: consent is checked before anything is spent.
	for _, consent := range []*dto.ConsentRequest{nil, {Accepted: false}} {
		uc, _ := newTestUsecase(t)
		_, err := uc.Register(context.Background(), dto.RegisterRequest{
			Email: "alex@example.com", NIK: testNIK, Consent: consent,
		})
		var appErr *apperror.AppError
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, 422, appErr.HTTPStatus)
		assert.Equal(t, apperror.CodeConsentRequired, appErr.ErrorCode)
	}
}

func TestRegister_NikVerifyUnreachable_FailsClosed(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.verifier.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).Return(portout.NIKVerification{}, errors.New("timeout"))

	_, err := uc.Register(context.Background(), dto.RegisterRequest{
		Email: "alex@example.com", NIK: testNIK,
		Consent: &dto.ConsentRequest{Accepted: true},
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 503, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeDukcapilUnavailable, appErr.ErrorCode)
}

func TestRegister_NikRejected(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.verifier.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).
		Return(portout.NIKVerification{Valid: false, Status: "NOT_FOUND"}, nil)

	_, err := uc.Register(context.Background(), dto.RegisterRequest{
		Email: "alex@example.com", NIK: testNIK,
		Consent: &dto.ConsentRequest{Accepted: true},
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 422, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeNIKValidationFailed, appErr.ErrorCode)
}

func TestRegister_DuplicateNIKAndEmailSurfaceTheirCodes(t *testing.T) {
	// Uniqueness is decided by the indexes, not by a prior read: two
	// registrations can interleave between a check and an insert.
	cases := []struct {
		name string
		err  *apperror.AppError
		code string
	}{
		{"nik", apperror.ErrConflict("x").WithCode(apperror.CodeNIKAlreadyRegistered), apperror.CodeNIKAlreadyRegistered},
		{"email", apperror.ErrConflict("x").WithCode(apperror.CodeEmailAlreadyUsed), apperror.CodeEmailAlreadyUsed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc, d := newTestUsecase(t)
			d.verifier.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).Return(goodVerification(), nil)
			d.repo.EXPECT().RegisterAccount(gomock.Any(), gomock.Any()).
				Return(entity.Account{}, tc.err)

			_, err := uc.Register(context.Background(), dto.RegisterRequest{
				Email: "alex@example.com", NIK: testNIK,
				Consent: &dto.ConsentRequest{Accepted: true},
			})
			var appErr *apperror.AppError
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, 409, appErr.HTTPStatus)
			assert.Equal(t, tc.code, appErr.ErrorCode)
		})
	}
}

// ---- ValidateAccessToken ----

func TestValidateAccessToken_Success(t *testing.T) {
	uc, d := newTestUsecase(t)
	signed, _, err := d.tokens.Issue(testAccountID, "user@example.com", true)
	require.NoError(t, err)

	accountID, err := uc.ValidateAccessToken(context.Background(), signed)
	require.NoError(t, err)
	assert.Equal(t, testAccountID, accountID)
}

func TestValidateAccessToken_Missing(t *testing.T) {
	uc, _ := newTestUsecase(t)
	_, err := uc.ValidateAccessToken(context.Background(), "")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeTokenInvalid, appErr.ErrorCode)
}

func TestValidateAccessToken_Garbage(t *testing.T) {
	uc, _ := newTestUsecase(t)
	_, err := uc.ValidateAccessToken(context.Background(), "not.a.jwt")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, apperror.CodeTokenInvalid, appErr.ErrorCode)
}

func TestValidateAccessToken_ExpiredIsDistinguishable(t *testing.T) {
	// Expired means "refresh"; invalid means "log in again". Clients act on the
	// difference, so the two must not collapse into one code.
	ctrl := gomock.NewController(t)
	// A one-nanosecond TTL puts exp in the same second as iat, so the token is
	// already expired when Verify compares them — no sleeping in tests.
	issuer, err := token.NewIssuer("test-signing-secret", "rusuh-user", time.Nanosecond)
	require.NoError(t, err)
	uc := NewUser(
		mock_outbound.NewMockIUserRepository(ctrl),
		mock_outbound.NewMockIEngagementRepository(ctrl),
		mock_outbound.NewMockINikVerifier(ctrl),
		mock_outbound.NewMockIRateLimiter(ctrl),
		issuer,
	)

	signed, _, err := issuer.Issue(testAccountID, "user@example.com", true)
	require.NoError(t, err)

	_, err = uc.ValidateAccessToken(context.Background(), signed)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeTokenExpired, appErr.ErrorCode)
}

func TestHashSecret_IsDeterministicAndNotReversible(t *testing.T) {
	a := hashSecret("abc")
	assert.Equal(t, a, hashSecret("abc"))
	assert.NotEqual(t, a, hashSecret("abd"))
	assert.NotContains(t, a, "abc")
}

func TestNewSecret_IsRandom(t *testing.T) {
	// Not a ULID: a time-ordered identifier is fine for an ID and
	// disqualifying for a credential.
	a, ha, err := newSecret()
	require.NoError(t, err)
	b, hb, err := newSecret()
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
	assert.NotEqual(t, ha, hb)
	assert.Equal(t, hashSecret(a), ha)
}
