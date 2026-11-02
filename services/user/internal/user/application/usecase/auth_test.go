package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	mock_outbound "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/_mock/user/port/outbound"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// ---- RequestMagicLink ----

func TestRequestMagicLink_success_newEmail(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().
		FindAccountByEmail(gomock.Any(), "new@example.com").
		Return(entity.Account{}, apperror.ErrNotFound("not found"))
	repo.EXPECT().
		SaveMagicToken(gomock.Any(), gomock.Any()).
		Return(nil)

	err := uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "new@example.com"})
	require.NoError(t, err)
}

func TestRequestMagicLink_success_existingEmail(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().
		FindAccountByEmail(gomock.Any(), "existing@example.com").
		Return(entity.Account{ID: "USR-001"}, nil)
	repo.EXPECT().
		SaveMagicToken(gomock.Any(), gomock.Any()).
		Return(nil)

	err := uc.RequestMagicLink(context.Background(), dto.RequestMagicLinkRequest{Email: "existing@example.com"})
	require.NoError(t, err)
}

// ---- VerifyMagicLink ----

func TestVerifyMagicLink_success(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	now := time.Now().UTC()
	mt := entity.MagicToken{
		ID:        "tok-1",
		Email:     "user@example.com",
		Token:     "valid-token",
		CreatedAt: now.Add(-1 * time.Minute),
		ExpiresAt: now.Add(14 * time.Minute),
	}
	repo.EXPECT().FindMagicTokenByValue(gomock.Any(), "valid-token").Return(mt, nil)
	repo.EXPECT().MarkMagicTokenUsed(gomock.Any(), mt.ID).Return(nil)
	repo.EXPECT().FindAccountByEmail(gomock.Any(), mt.Email).Return(entity.Account{ID: "USR-001", Email: mt.Email}, nil)
	repo.EXPECT().SaveAccessToken(gomock.Any(), gomock.Any()).Return(nil)

	resp, err := uc.VerifyMagicLink(context.Background(), "valid-token")
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccessToken)
	assert.Equal(t, "USR-001", resp.User.ID)
}

func TestVerifyMagicLink_tokenNotFound(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().FindMagicTokenByValue(gomock.Any(), "bad-token").Return(entity.MagicToken{}, errors.New("not found"))

	_, err := uc.VerifyMagicLink(context.Background(), "bad-token")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

func TestVerifyMagicLink_expired(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	mt := entity.MagicToken{
		ID:        "tok-old",
		Email:     "user@example.com",
		Token:     "old-token",
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute), // already expired
	}
	repo.EXPECT().FindMagicTokenByValue(gomock.Any(), "old-token").Return(mt, nil)

	_, err := uc.VerifyMagicLink(context.Background(), "old-token")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

func TestVerifyMagicLink_alreadyUsed(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	usedAt := time.Now().UTC().Add(-5 * time.Minute)
	mt := entity.MagicToken{
		ID:        "tok-used",
		Email:     "user@example.com",
		Token:     "used-token",
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		UsedAt:    &usedAt,
	}
	repo.EXPECT().FindMagicTokenByValue(gomock.Any(), "used-token").Return(mt, nil)

	_, err := uc.VerifyMagicLink(context.Background(), "used-token")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

// ---- Register ----

func TestRegister_success(t *testing.T) {
	uc, repo, dukcapil := newTestUsecase(t)

	repo.EXPECT().IsNIKRegistered(gomock.Any(), gomock.Any()).Return(false, nil)
	dukcapil.EXPECT().VerifyNIK(gomock.Any(), "3171000000000001").Return(portout.DukcapilResult{
		Valid: true, Name: "TEST USER", Status: "ACTIVE", Ref: "dukcapil-ref-1",
	}, nil)
	repo.EXPECT().SaveAccount(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a entity.Account) (entity.Account, error) { return a, nil },
	)
	repo.EXPECT().SaveConsentRecord(gomock.Any(), gomock.Any()).Return(nil)

	resp, err := uc.Register(context.Background(), dto.RegisterRequest{
		Email: "alex@example.com",
		NIK:   "3171000000000001",
		Consent: dto.ConsentRequest{
			Accepted:      true,
			PolicyVersion: "2026-10-01",
		},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.UserID)
}

func TestRegister_consentNotAccepted(t *testing.T) {
	uc, _, _ := newTestUsecase(t)

	_, err := uc.Register(context.Background(), dto.RegisterRequest{
		Email: "alex@example.com",
		NIK:   "3171000000000001",
		Consent: dto.ConsentRequest{
			Accepted:      false, // not accepted
			PolicyVersion: "2026-10-01",
		},
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 422, appErr.HTTPStatus)
}

func TestRegister_nikAlreadyRegistered(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().IsNIKRegistered(gomock.Any(), gomock.Any()).Return(true, nil)

	_, err := uc.Register(context.Background(), dto.RegisterRequest{
		Email: "other@example.com",
		NIK:   "3171000000000001",
		Consent: dto.ConsentRequest{Accepted: true, PolicyVersion: "2026-10-01"},
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 409, appErr.HTTPStatus)
}

func TestRegister_dukcapilUnavailable(t *testing.T) {
	uc, repo, dukcapil := newTestUsecase(t)
	repo.EXPECT().IsNIKRegistered(gomock.Any(), gomock.Any()).Return(false, nil)
	dukcapil.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).Return(portout.DukcapilResult{}, errors.New("timeout"))

	_, err := uc.Register(context.Background(), dto.RegisterRequest{
		Email: "alex@example.com",
		NIK:   "3171000000000001",
		Consent: dto.ConsentRequest{Accepted: true, PolicyVersion: "2026-10-01"},
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 503, appErr.HTTPStatus)
}

// ---- ValidateAccessToken ----

func TestValidateAccessToken_success(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	at := entity.AccessToken{
		Token:     "bearer-token",
		AccountID: "USR-001",
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}
	repo.EXPECT().FindAccessTokenByValue(gomock.Any(), "bearer-token").Return(at, nil)

	accountID, err := uc.ValidateAccessToken(context.Background(), "bearer-token")
	require.NoError(t, err)
	assert.Equal(t, "USR-001", accountID)
}

func TestValidateAccessToken_expired(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	at := entity.AccessToken{
		Token:     "stale-token",
		AccountID: "USR-001",
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
	}
	repo.EXPECT().FindAccessTokenByValue(gomock.Any(), "stale-token").Return(at, nil)

	_, err := uc.ValidateAccessToken(context.Background(), "stale-token")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

func TestValidateAccessToken_missing(t *testing.T) {
	uc, _, _ := newTestUsecase(t)
	_, err := uc.ValidateAccessToken(context.Background(), "")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
}

// ---- hashNIK consistency ----

func TestHashNIK_deterministicAndKeyed(t *testing.T) {
	uc, _, _ := newTestUsecase(t)

	h1 := uc.hashNIK("3171000000000001")
	h2 := uc.hashNIK("3171000000000001")
	assert.Equal(t, h1, h2, "same input must produce same HMAC")

	h3 := uc.hashNIK("3171000000000002")
	assert.NotEqual(t, h1, h3, "different NIK must produce different HMAC")
}

// ---- helpers for mock setup ----

// mockRepo returns a configured MockIUserRepository.
func mockRepo(t *testing.T) *mock_outbound.MockIUserRepository {
	t.Helper()
	return mock_outbound.NewMockIUserRepository(gomock.NewController(t))
}
