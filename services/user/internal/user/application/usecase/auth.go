package usecase

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/idgen"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/valueobject"
)

// hashNIK computes a keyed HMAC-SHA256 of the raw NIK, producing a fixed-length
// hex string stored in place of the raw number. The same input + key always
// produces the same output, enabling equality checks without storing the NIK.
func (u *UserUsecase) hashNIK(rawNIK string) string {
	mac := hmac.New(sha256.New, []byte(u.nikHMACKey))
	mac.Write([]byte(rawNIK))
	return hex.EncodeToString(mac.Sum(nil))
}

// RequestMagicLink generates a one-time login link token and "sends" it via email.
// In the POC the token is stored in memory; production sends via SES outbox.
// Implements POST /auth/magic-link and POST /auth/magic-link/resend (FR-31).
func (u *UserUsecase) RequestMagicLink(ctx context.Context, req dto.RequestMagicLinkRequest) error {
	now := time.Now().UTC()
	token := entity.MagicToken{
		ID:        idgen.New(),
		Email:     req.Email,
		Token:     idgen.New(), // opaque random value — not a ULID prefix, just a random string
		CreatedAt: now,
		ExpiresAt: now.Add(entity.MagicTokenTTL),
	}

	// Try to link the token to an existing account, but do not reveal whether
	// the address is registered (prevents account-enumeration via timing).
	account, err := u.repo.FindAccountByEmail(ctx, req.Email)
	if err == nil {
		token.AccountID = account.ID
	}

	return u.repo.SaveMagicToken(ctx, token)
}

// VerifyMagicLink validates the magic token, issues a new access token, and
// returns the user's session. Implements GET /auth/verify (FR-31, BR-09).
func (u *UserUsecase) VerifyMagicLink(ctx context.Context, token string) (dto.VerifyMagicLinkResponse, error) {
	mt, err := u.repo.FindMagicTokenByValue(ctx, token)
	if err != nil {
		return dto.VerifyMagicLinkResponse{}, apperror.ErrUnauthorized("invalid or expired login link")
	}
	if mt.IsExpired() {
		return dto.VerifyMagicLinkResponse{}, apperror.ErrUnauthorized("login link has expired")
	}
	if mt.IsUsed() {
		return dto.VerifyMagicLinkResponse{}, apperror.ErrUnauthorized("login link has already been used")
	}

	if err := u.repo.MarkMagicTokenUsed(ctx, mt.ID); err != nil {
		return dto.VerifyMagicLinkResponse{}, apperror.ErrInternal("failed to consume login token")
	}

	// Find or create the account.
	account, err := u.repo.FindAccountByEmail(ctx, mt.Email)
	if err != nil {
		// Account does not yet exist — create a minimal one.
		now := time.Now().UTC()
		account = entity.Account{
			ID:        idgen.NewPrefixed(entity.AccountIDPrefix),
			Email:     mt.Email,
			CreatedAt: now,
		}
		account, err = u.repo.SaveAccount(ctx, account)
		if err != nil {
			return dto.VerifyMagicLinkResponse{}, apperror.ErrInternal("failed to create account")
		}
	}

	// Issue access + refresh tokens.
	now := time.Now().UTC()
	accessToken := entity.AccessToken{
		Token:     idgen.New(),
		AccountID: account.ID,
		ExpiresAt: now.Add(entity.AccessTokenTTLSeconds * time.Second),
	}
	if err := u.repo.SaveAccessToken(ctx, accessToken); err != nil {
		return dto.VerifyMagicLinkResponse{}, apperror.ErrInternal("failed to issue access token")
	}

	// Build user summary — include masked NIK only when the account has one.
	summary := dto.UserSummary{
		ID:    account.ID,
		Email: account.Email,
	}
	// We don't store the raw NIK, so the masked form cannot be derived here.
	// Production would return the masked NIK stored separately; for the POC
	// we leave the field empty when NIK is set (HMAC only).

	return dto.VerifyMagicLinkResponse{
		AccessToken:  accessToken.Token,
		RefreshToken: idgen.New(), // refresh token not fully implemented in POC
		ExpiresIn:    entity.AccessTokenTTLSeconds,
		User:         summary,
	}, nil
}

// Register creates a new account with a verified NIK and records consent.
// Implements POST /auth/register (FR-29, FR-30, FR-31).
func (u *UserUsecase) Register(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error) {
	if !req.Consent.Accepted {
		return dto.RegisterResponse{}, apperror.ErrUnprocessable("CONSENT_REQUIRED")
	}

	nikHmac := u.hashNIK(req.NIK)

	// One NIK = one account globally (UNIQUE nik_hmac on accounts table).
	registered, err := u.repo.IsNIKRegistered(ctx, nikHmac)
	if err != nil {
		return dto.RegisterResponse{}, apperror.ErrInternal("failed to check NIK registration")
	}
	if registered {
		return dto.RegisterResponse{}, apperror.ErrConflict("NIK_ALREADY_REGISTERED")
	}

	// Verify NIK against Dukcapil (fail closed — ADR-005 §8).
	result, err := u.dukcapil.VerifyNIK(ctx, req.NIK)
	if err != nil {
		return dto.RegisterResponse{}, apperror.ErrServiceUnavailable("DUKCAPIL_UNAVAILABLE")
	}
	if !result.Valid {
		return dto.RegisterResponse{}, apperror.ErrBadRequest("NIK validation failed: " + result.Status)
	}

	now := time.Now().UTC()
	account := entity.Account{
		ID:            idgen.NewPrefixed(entity.AccountIDPrefix),
		Email:         req.Email,
		NIKHmac:       nikHmac,
		NIKVerifiedAt: &now,
		DukcapilRef:   result.Ref,
		CreatedAt:     now,
	}

	saved, err := u.repo.SaveAccount(ctx, account)
	if err != nil {
		return dto.RegisterResponse{}, apperror.ErrInternal("failed to create account")
	}

	consent := entity.ConsentRecord{
		ID:            idgen.New(),
		AccountID:     saved.ID,
		PolicyVersion: req.Consent.PolicyVersion,
		ConsentedAt:   now,
	}
	if err := u.repo.SaveConsentRecord(ctx, consent); err != nil {
		// Consent record failure is non-fatal for the caller, but should be alarmed.
		// For the POC we swallow it silently.
		_ = err
	}

	return dto.RegisterResponse{UserID: saved.ID}, nil
}

// ValidateAccessToken resolves a bearer token string to its account ID.
// Returns ErrUnauthorized when the token is absent, unknown, or expired.
func (u *UserUsecase) ValidateAccessToken(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", apperror.ErrUnauthorized("missing access token")
	}
	at, err := u.repo.FindAccessTokenByValue(ctx, token)
	if err != nil {
		return "", apperror.ErrUnauthorized("invalid or expired access token")
	}
	if at.IsExpired() {
		return "", apperror.ErrUnauthorized("access token has expired")
	}
	return at.AccountID, nil
}

// maskStoredNIK returns the display-safe masked form of a NIK.
// Since we only store the HMAC, we cannot unmask here; callers that have the
// raw NIK in scope should use valueobject.MaskNIK directly.
func maskStoredNIK(_ string) string {
	// The raw NIK is never stored; this is a placeholder indicating that
	// production would look up the masked form from a separate column or
	// decode it from a reversible encryption scheme.
	return valueobject.MaskNIK("????????????????")
}
