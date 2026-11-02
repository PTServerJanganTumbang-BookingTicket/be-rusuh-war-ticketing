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
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

const testAccountID = "USR-001"

// ---- GetSavedNIKs ----

func TestGetSavedNIKs_success(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().FindSavedNIKsByAccountID(gomock.Any(), testAccountID).Return([]entity.SavedNIK{
		{ID: "SNK-1", AccountID: testAccountID, NIKHmac: "hmac1", Label: "Budi", VerifiedAt: time.Now()},
	}, nil)

	resp, err := uc.GetSavedNIKs(context.Background(), testAccountID)
	require.NoError(t, err)
	assert.Equal(t, 5, resp.MaxEntries)
	assert.Len(t, resp.SavedNIKs, 1)
	assert.Equal(t, "SNK-1", resp.SavedNIKs[0].ID)
	assert.Equal(t, "VERIFIED", resp.SavedNIKs[0].Status)
}

func TestGetSavedNIKs_empty(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().FindSavedNIKsByAccountID(gomock.Any(), testAccountID).Return([]entity.SavedNIK{}, nil)

	resp, err := uc.GetSavedNIKs(context.Background(), testAccountID)
	require.NoError(t, err)
	assert.Empty(t, resp.SavedNIKs)
}

// ---- AddSavedNIK ----

func TestAddSavedNIK_success(t *testing.T) {
	uc, repo, dukcapil := newTestUsecase(t)
	repo.EXPECT().CountSavedNIKsByAccountID(gomock.Any(), testAccountID).Return(2, nil)
	repo.EXPECT().IsNIKAlreadySaved(gomock.Any(), testAccountID, gomock.Any()).Return(false, nil)
	dukcapil.EXPECT().VerifyNIK(gomock.Any(), "3171000000000033").Return(portout.DukcapilResult{
		Valid: true, Name: "BUDI", Status: "ACTIVE", Ref: "ref-33",
	}, nil)
	repo.EXPECT().SaveSavedNIK(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, n entity.SavedNIK) (entity.SavedNIK, error) { return n, nil },
	)

	item, err := uc.AddSavedNIK(context.Background(), testAccountID, dto.AddSavedNIKRequest{
		NIK: "3171000000000033", Label: "Budi",
	})
	require.NoError(t, err)
	assert.Equal(t, "Budi", item.Label)
	assert.Equal(t, "VERIFIED", item.Status)
}

func TestAddSavedNIK_maxReached(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().CountSavedNIKsByAccountID(gomock.Any(), testAccountID).Return(5, nil)

	_, err := uc.AddSavedNIK(context.Background(), testAccountID, dto.AddSavedNIKRequest{
		NIK: "3171000000000099", Label: "Extra",
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 409, appErr.HTTPStatus)
	assert.Contains(t, appErr.Message, "MAX_SAVED_NIKS_REACHED")
}

func TestAddSavedNIK_alreadySaved(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().CountSavedNIKsByAccountID(gomock.Any(), testAccountID).Return(1, nil)
	repo.EXPECT().IsNIKAlreadySaved(gomock.Any(), testAccountID, gomock.Any()).Return(true, nil)

	_, err := uc.AddSavedNIK(context.Background(), testAccountID, dto.AddSavedNIKRequest{
		NIK: "3171000000000033", Label: "Budi",
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 409, appErr.HTTPStatus)
	assert.Contains(t, appErr.Message, "NIK_ALREADY_SAVED")
}

func TestAddSavedNIK_dukcapilDown(t *testing.T) {
	uc, repo, dukcapil := newTestUsecase(t)
	repo.EXPECT().CountSavedNIKsByAccountID(gomock.Any(), testAccountID).Return(0, nil)
	repo.EXPECT().IsNIKAlreadySaved(gomock.Any(), testAccountID, gomock.Any()).Return(false, nil)
	dukcapil.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).Return(portout.DukcapilResult{}, errors.New("connection timeout"))

	_, err := uc.AddSavedNIK(context.Background(), testAccountID, dto.AddSavedNIKRequest{
		NIK: "3171000000000099", Label: "Charlie",
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 503, appErr.HTTPStatus)
}

func TestAddSavedNIK_nikInvalid(t *testing.T) {
	uc, repo, dukcapil := newTestUsecase(t)
	repo.EXPECT().CountSavedNIKsByAccountID(gomock.Any(), testAccountID).Return(0, nil)
	repo.EXPECT().IsNIKAlreadySaved(gomock.Any(), testAccountID, gomock.Any()).Return(false, nil)
	dukcapil.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).Return(portout.DukcapilResult{
		Valid: false, Status: "NIK_NOT_FOUND",
	}, nil)

	_, err := uc.AddSavedNIK(context.Background(), testAccountID, dto.AddSavedNIKRequest{
		NIK: "3171000000000099", Label: "Dave",
	})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 422, appErr.HTTPStatus)
}

// ---- DeleteSavedNIK ----

func TestDeleteSavedNIK_success(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().FindSavedNIKByID(gomock.Any(), testAccountID, "SNK-1").Return(entity.SavedNIK{ID: "SNK-1"}, nil)
	repo.EXPECT().DeleteSavedNIK(gomock.Any(), testAccountID, "SNK-1").Return(nil)

	err := uc.DeleteSavedNIK(context.Background(), testAccountID, "SNK-1")
	require.NoError(t, err)
}

func TestDeleteSavedNIK_notFound(t *testing.T) {
	uc, repo, _ := newTestUsecase(t)
	repo.EXPECT().FindSavedNIKByID(gomock.Any(), testAccountID, "SNK-missing").Return(entity.SavedNIK{}, apperror.ErrNotFound("not found"))

	err := uc.DeleteSavedNIK(context.Background(), testAccountID, "SNK-missing")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 404, appErr.HTTPStatus)
}
