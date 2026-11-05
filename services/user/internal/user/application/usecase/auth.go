package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/idgen"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/mapper"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// newSecret returns a 256-bit random credential and its hash for storage.
//
// Not a ULID: a ULID is time-ordered and largely predictable, which is fine
// for an identifier and disqualifying for a credential. Only the hash is
// persisted, so a database read is not enough to use what it refers to.
func newSecret() (plain, hashed string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, hashSecret(plain), nil
}

func hashSecret(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// RequestMagicLink issues a one-time login link.
//
// The response never reveals whether the address has an account: the same 200
// comes back either way, and a token is stored regardless. An attacker
// learning which addresses are registered is a privacy leak on its own, and
// here it would also enumerate who holds tickets.
//
// The token row and its email are written in one transaction. A token with no
// queued email is a link nobody receives; a queued email with no token is a
// link that cannot be redeemed.
//
// Implements POST /auth/magic-link and /auth/magic-link/resend (FR-31).
func (u *UserUsecase) RequestMagicLink(ctx context.Context, req dto.RequestMagicLinkRequest) error {
	email := normalizeEmail(req.Email)

	allowed, retryAfter, err := u.limiter.Allow(ctx, "auth:magic-link:"+email, MagicLinkLimit, MagicLinkWindow)
	if err != nil {
		// Fail open: the limiter protects against abuse, and letting it take
		// down login would convert a Redis blip into an outage.
		slog.WarnContext(ctx, "rate limiter unavailable, allowing request", slog.Any("err", err))
	} else if !allowed {
		return apperror.
			ErrTooManyRequests("Please wait before requesting another link").
			WithCode(apperror.CodeTooManyRequests).
			WithRetryAfter(retryAfter)
	}

	plain, hashed, err := newSecret()
	if err != nil {
		return apperror.ErrInternal("failed to generate login link")
	}

	now := time.Now().UTC()
	mt := entity.MagicToken{
		ID:        idgen.New(),
		Email:     email,
		TokenHash: hashed,
		Purpose:   entity.PurposeLogin,
		CreatedAt: now,
		ExpiresAt: now.Add(entity.MagicTokenTTL),
	}
	if account, err := u.repo.FindAccountByEmail(ctx, email); err == nil {
		mt.AccountID = account.ID
	}

	event, err := entity.NewOutboxEvent(idgen.New(), email, events.TypeAuthMagicLink,
		events.AuthMagicLinkPayload{AccountID: mt.AccountID, Email: email, Token: plain})
	if err != nil {
		return apperror.ErrInternal("failed to queue login link")
	}

	if err := u.repo.SaveMagicTokenWithEvent(ctx, mt, event); err != nil {
		return apperror.ErrInternal("failed to issue login link")
	}
	return nil
}

// VerifyMagicLink consumes a login link and issues a session.
//
// It does NOT create an account for an unknown address. Account creation runs
// only through Register, which requires a Dukcapil-verified NIK and a recorded
// consent (FR-31a, FR-31c, BR-11); creating one here produced accounts with
// neither, which could then store other people's NIKs.
//
// Implements GET /auth/verify (FR-31, BR-09).
func (u *UserUsecase) VerifyMagicLink(ctx context.Context, rawToken string) (dto.VerifyMagicLinkResponse, error) {
	mt, err := u.repo.ConsumeMagicToken(ctx, hashSecret(rawToken), entity.PurposeLogin)
	if err != nil {
		// One message for unknown, expired and already-used alike: telling
		// them apart confirms which addresses have had links issued.
		return dto.VerifyMagicLinkResponse{}, apperror.
			ErrUnauthorized("invalid or expired login link").
			WithCode(apperror.CodeTokenInvalid)
	}

	account, err := u.repo.FindAccountByEmail(ctx, mt.Email)
	if err != nil {
		return dto.VerifyMagicLinkResponse{}, apperror.
			ErrUnauthorized("no account exists for this address").
			WithCode(apperror.CodeTokenInvalid)
	}

	// Redeeming a login link proves control of the address.
	if !account.IsEmailVerified() {
		if err := u.repo.MarkEmailVerified(ctx, account.ID); err != nil {
			slog.ErrorContext(ctx, "failed to mark email verified", slog.Any("err", err))
		}
	}

	session, err := u.issueSession(ctx, account)
	if err != nil {
		return dto.VerifyMagicLinkResponse{}, err
	}

	return dto.VerifyMagicLinkResponse{
		AccessToken:     session.AccessToken,
		RefreshToken:    session.RefreshToken,
		ExpiresIn:       session.ExpiresIn,
		User:            mapper.ToUserSummary(account),
		RedirectContext: u.redirectContext(ctx, account.ID),
	}, nil
}

// redirectContext resolves where a returning customer left off (BR-09).
//
// Best-effort: a login must not fail because the engagement lookup did. The
// customer lands on the home page instead of their booking, which is a
// degraded experience rather than a broken one.
func (u *UserUsecase) redirectContext(ctx context.Context, accountID string) *dto.RedirectContext {
	engagement, err := u.engagement.FindActiveEngagement(ctx, accountID)
	if err != nil {
		slog.WarnContext(ctx, "engagement lookup failed, omitting redirect context", slog.Any("err", err))
		return nil
	}
	if !engagement.IsActive() {
		return nil
	}
	return mapper.ToRedirectContext(engagement)
}

// session is an issued access/refresh pair.
type session struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// issueSession mints an access token and opens a new refresh rotation chain.
//
// A fresh family per login means revoking one compromised chain does not log
// the customer out of their other devices.
func (u *UserUsecase) issueSession(ctx context.Context, account entity.Account) (session, error) {
	signed, claims, err := u.tokens.Issue(account.ID, account.Email, account.IsNIKVerified())
	if err != nil {
		return session{}, apperror.ErrInternal("failed to issue access token")
	}

	plain, hashed, err := newSecret()
	if err != nil {
		return session{}, apperror.ErrInternal("failed to issue refresh token")
	}

	now := time.Now().UTC()
	refresh := entity.RefreshToken{
		ID:        idgen.NewPrefixed(entity.RefreshTokenIDPrefix),
		AccountID: account.ID,
		FamilyID:  idgen.New(),
		TokenHash: hashed,
		IssuedAt:  now,
		ExpiresAt: now.Add(entity.RefreshTokenTTL),
	}
	if err := u.repo.CreateRefreshToken(ctx, refresh); err != nil {
		return session{}, apperror.ErrInternal("failed to issue refresh token")
	}

	return session{
		AccessToken:  signed,
		RefreshToken: plain,
		ExpiresIn:    int(time.Unix(claims.ExpiresAt, 0).Sub(time.Unix(claims.IssuedAt, 0)).Seconds()),
	}, nil
}

// RefreshSession redeems a refresh token and issues a new pair.
//
// Rotation: the presented token is spent and a replacement comes back, both in
// one transaction. Replaying a spent token is the signature of theft, so the
// adapter revokes the whole family and this returns 401 — the customer logs in
// again, which is the correct outcome when a credential has leaked
// (OAuth 2.0 BCP §4.13).
//
// Implements POST /auth/refresh.
func (u *UserUsecase) RefreshSession(ctx context.Context, rawToken string) (dto.RefreshSessionResponse, error) {
	if rawToken == "" {
		return dto.RefreshSessionResponse{}, apperror.
			ErrUnauthorized("missing refresh token").
			WithCode(apperror.CodeTokenInvalid)
	}

	plain, hashed, err := newSecret()
	if err != nil {
		return dto.RefreshSessionResponse{}, apperror.ErrInternal("failed to issue refresh token")
	}

	now := time.Now().UTC()
	replacement := entity.RefreshToken{
		ID:        idgen.NewPrefixed(entity.RefreshTokenIDPrefix),
		TokenHash: hashed,
		IssuedAt:  now,
		ExpiresAt: now.Add(entity.RefreshTokenTTL),
	}

	// The adapter fills AccountID and FamilyID from the redeemed row: the
	// chain's owner is whatever the stored token says, never what the caller
	// claims.
	rotated, err := u.repo.RotateRefreshToken(ctx, hashSecret(rawToken), replacement)
	switch {
	case errors.Is(err, portout.ErrRefreshTokenReplayed):
		slog.WarnContext(ctx, "refresh token replayed, family revoked",
			slog.String("metric", "RefreshTokenReplayed"))
		return dto.RefreshSessionResponse{}, apperror.
			ErrUnauthorized("session ended; please sign in again").
			WithCode(apperror.CodeTokenInvalid)
	case err != nil:
		var appErr *apperror.AppError
		if errors.As(err, &appErr) && appErr.HTTPStatus == 404 {
			return dto.RefreshSessionResponse{}, apperror.
				ErrUnauthorized("invalid or expired refresh token").
				WithCode(apperror.CodeTokenExpired)
		}
		return dto.RefreshSessionResponse{}, apperror.ErrInternal("failed to refresh session")
	}

	account, err := u.repo.FindAccountByID(ctx, rotated.AccountID)
	if err != nil {
		return dto.RefreshSessionResponse{}, apperror.
			ErrUnauthorized("account is no longer active").
			WithCode(apperror.CodeTokenInvalid)
	}

	// Re-read from the account rather than copying the old token's claims:
	// a NIK verified since the last refresh must take effect, and one revoked
	// by operations must stop being asserted.
	signed, claims, err := u.tokens.Issue(account.ID, account.Email, account.IsNIKVerified())
	if err != nil {
		return dto.RefreshSessionResponse{}, apperror.ErrInternal("failed to issue access token")
	}

	return dto.RefreshSessionResponse{
		AccessToken:  signed,
		RefreshToken: plain,
		ExpiresIn:    int(time.Unix(claims.ExpiresAt, 0).Sub(time.Unix(claims.IssuedAt, 0)).Seconds()),
	}, nil
}

// Register creates an account with a Dukcapil-verified NIK and records consent.
//
// Order is fixed by ADR-005 §8: consent, then verification, then one
// transactional write. The account, its consent record and the account.verify
// event all commit together — an account carrying a verified NIK with no
// consent record is a UU PDP violation, and that is precisely what a crash
// between separate writes leaves behind.
//
// Implements POST /auth/register (FR-29, FR-30, FR-31a..d).
func (u *UserUsecase) Register(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error) {
	if req.Consent == nil || !req.Consent.Accepted {
		return dto.RegisterResponse{}, apperror.
			ErrUnprocessable("consent to third-party NIK processing is required").
			WithCode(apperror.CodeConsentRequired)
	}

	email := normalizeEmail(req.Email)

	// nik-verify owns the rate limiter, the breaker and the cache; this call
	// fails closed when no answer is obtainable (BR-13).
	verification, err := u.verifier.VerifyNIK(ctx, req.NIK)
	if err != nil {
		return dto.RegisterResponse{}, apperror.
			ErrServiceUnavailable("NIK verification is temporarily unavailable").
			WithCode(apperror.CodeDukcapilUnavailable)
	}
	if !verification.Valid {
		return dto.RegisterResponse{}, apperror.
			ErrUnprocessable("NIK could not be verified: " + verification.Status).
			WithCode(apperror.CodeNIKValidationFailed)
	}

	now := time.Now().UTC()
	accountID := idgen.NewPrefixed(entity.AccountIDPrefix)

	verifyToken, verifyHash, err := newSecret()
	if err != nil {
		return dto.RegisterResponse{}, apperror.ErrInternal("failed to create account")
	}

	account := entity.Account{
		ID:            accountID,
		Email:         email,
		NIKHmac:       verification.NIKHmac,
		NIKMasked:     verification.NIKMasked,
		NIKVerifiedAt: &now,
		DukcapilRef:   verification.Ref,
		CreatedAt:     now,
	}
	consent := entity.ConsentRecord{
		ID:            idgen.New(),
		AccountID:     accountID,
		PolicyVersion: req.Consent.PolicyVersion,
		ConsentedAt:   now,
	}
	event, err := entity.NewOutboxEvent(idgen.New(), accountID, events.TypeAccountVerify,
		events.AccountVerifyPayload{AccountID: accountID, Email: email, Token: verifyToken})
	if err != nil {
		return dto.RegisterResponse{}, apperror.ErrInternal("failed to queue verification email")
	}

	// The verification link is a magic token of its own purpose, so the same
	// single-use, hashed, expiring machinery covers it.
	verifyMagicToken := entity.MagicToken{
		ID:        idgen.New(),
		AccountID: accountID,
		Email:     email,
		TokenHash: verifyHash,
		Purpose:   entity.PurposeVerifyEmail,
		CreatedAt: now,
		ExpiresAt: now.Add(entity.MagicTokenTTL),
	}

	saved, err := u.repo.RegisterAccount(ctx, portout.Registration{
		Account:     account,
		Consent:     consent,
		VerifyToken: verifyMagicToken,
		Event:       event,
	})
	if err != nil {
		// Uniqueness is decided by the indexes, not by a prior read: two
		// registrations for one address or one NIK can interleave between a
		// check and an insert, and only the database sees both.
		var appErr *apperror.AppError
		if errors.As(err, &appErr) {
			return dto.RegisterResponse{}, appErr
		}
		return dto.RegisterResponse{}, apperror.ErrInternal("failed to create account")
	}

	return dto.RegisterResponse{UserID: saved.ID}, nil
}

// VerifyEmail consumes an address-confirmation link (FR-31).
//
// Separate from VerifyMagicLink and matched on purpose: a verification link
// proves control of an address, a login link grants a session. Letting one be
// redeemed as the other turns "confirm your email" into an auth bypass.
//
// Per ADR-005 §12 an unverified account can still buy; verification only
// unlocks order history. So this is not a gate on anything load-bearing.
func (u *UserUsecase) VerifyEmail(ctx context.Context, rawToken string) error {
	mt, err := u.repo.ConsumeMagicToken(ctx, hashSecret(rawToken), entity.PurposeVerifyEmail)
	if err != nil {
		return apperror.
			ErrUnauthorized("invalid or expired verification link").
			WithCode(apperror.CodeTokenInvalid)
	}
	if err := u.repo.MarkEmailVerified(ctx, mt.AccountID); err != nil {
		return apperror.ErrInternal("failed to verify email")
	}
	return nil
}

// ValidateAccessToken resolves a bearer token to its account ID.
//
// Stateless: the token is a signed JWT, so no store is consulted. That is what
// lets booking-service and queue-service verify the same token, and what makes
// it survive the per-container lifetime of a Lambda.
func (u *UserUsecase) ValidateAccessToken(_ context.Context, raw string) (string, error) {
	if raw == "" {
		return "", apperror.ErrUnauthorized("missing access token").WithCode(apperror.CodeTokenInvalid)
	}
	claims, err := u.tokens.Verify(raw)
	switch {
	case errors.Is(err, token.ErrExpired):
		return "", apperror.ErrUnauthorized("access token has expired").WithCode(apperror.CodeTokenExpired)
	case err != nil:
		return "", apperror.ErrUnauthorized("invalid access token").WithCode(apperror.CodeTokenInvalid)
	}
	return claims.Subject, nil
}
