package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

func addReq() dto.AddSavedNIKRequest {
	return dto.AddSavedNIKRequest{NIK: "3171000000000033", Label: "Budi"}
}

// ---- GetSavedNIKs ----

func TestGetSavedNIKs_ReturnsStoredMask(t *testing.T) {
	// The bug this closes: the mask used to be derivable only at create time,
	// so listing returned placeholders while creating returned a real mask.
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindSavedNIKsByAccountID(gomock.Any(), testAccountID).Return([]entity.SavedNIK{
		{ID: "SNK-1", AccountID: testAccountID, NIKHmac: "h1", NIKMasked: "3171**********33",
			Label: "Budi", VerifiedAt: time.Now().UTC()},
	}, nil)

	resp, err := uc.GetSavedNIKs(context.Background(), testAccountID)
	require.NoError(t, err)
	assert.Equal(t, entity.MaxSavedNIKs, resp.MaxEntries)
	require.Len(t, resp.SavedNIKs, 1)
	assert.Equal(t, "3171**********33", resp.SavedNIKs[0].NIKMasked)
	assert.Equal(t, "VERIFIED", resp.SavedNIKs[0].Status)
	assert.NotContains(t, resp.SavedNIKs[0].NIKMasked, "0000000000")
}

func TestGetSavedNIKs_Empty(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindSavedNIKsByAccountID(gomock.Any(), testAccountID).Return([]entity.SavedNIK{}, nil)

	resp, err := uc.GetSavedNIKs(context.Background(), testAccountID)
	require.NoError(t, err)
	assert.Empty(t, resp.SavedNIKs)
	assert.NotNil(t, resp.SavedNIKs, "an empty list must marshal as [] not null")
}

func TestGetSavedNIKs_RepoError(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindSavedNIKsByAccountID(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

	_, err := uc.GetSavedNIKs(context.Background(), testAccountID)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 500, appErr.HTTPStatus)
}

// ---- AddSavedNIK ----

func TestAddSavedNIK_Success(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().HasConsent(gomock.Any(), testAccountID).Return(true, nil)
	d.verifier.EXPECT().VerifyNIK(gomock.Any(), "3171000000000033").Return(portout.NIKVerification{
		NIKHmac: "hmac-33", NIKMasked: "3171**********33", Valid: true, Status: "ACTIVE", Ref: "ref-33",
	}, nil)

	var inserted entity.SavedNIK
	var capSeen int
	d.repo.EXPECT().CreateSavedNIKWithinCap(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, n entity.SavedNIK, c int) (entity.SavedNIK, error) {
			inserted, capSeen = n, c
			return n, nil
		})

	item, err := uc.AddSavedNIK(context.Background(), testAccountID, addReq())
	require.NoError(t, err)
	assert.Equal(t, "Budi", item.Label)
	assert.Equal(t, "3171**********33", item.NIKMasked)
	assert.Equal(t, "hmac-33", inserted.NIKHmac)
	assert.Equal(t, entity.MaxSavedNIKs, capSeen)
	assert.False(t, inserted.VerifiedAt.IsZero(), "a row cannot exist without proof of verification")
}

func TestAddSavedNIK_RequiresConsent(t *testing.T) {
	// This list holds OTHER PEOPLE's NIKs. Storing one with no recorded consent
	// is a UU PDP transfer with no legal basis (FR-31c, NFR-11). No verifier
	// expectation: consent is checked before anything is spent.
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().HasConsent(gomock.Any(), testAccountID).Return(false, nil)

	_, err := uc.AddSavedNIK(context.Background(), testAccountID, addReq())
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 422, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeConsentRequired, appErr.ErrorCode)
}

func TestAddSavedNIK_VerifiesBeforeInserting(t *testing.T) {
	// Checkout treats a stored NIK as proof and SKIPS Dukcapil, so a row
	// written before the answer arrives moves the fail-closed hole one step
	// back. No CreateSavedNIKWithinCap expectation here.
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().HasConsent(gomock.Any(), gomock.Any()).Return(true, nil)
	d.verifier.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).
		Return(portout.NIKVerification{Valid: false, Status: "NOT_FOUND"}, nil)

	_, err := uc.AddSavedNIK(context.Background(), testAccountID, addReq())
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 422, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeNIKValidationFailed, appErr.ErrorCode)
}

func TestAddSavedNIK_VerifierUnreachable_StoresNothing(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().HasConsent(gomock.Any(), gomock.Any()).Return(true, nil)
	d.verifier.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).
		Return(portout.NIKVerification{}, errors.New("connection timeout"))

	_, err := uc.AddSavedNIK(context.Background(), testAccountID, addReq())
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 503, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeDukcapilUnavailable, appErr.ErrorCode)
}

func TestAddSavedNIK_CapAndDuplicateSurfaceTheirCodes(t *testing.T) {
	cases := []struct {
		name string
		err  *apperror.AppError
		code string
	}{
		{"cap reached", apperror.ErrConflict("full").WithCode(apperror.CodeMaxSavedNIKsReached), apperror.CodeMaxSavedNIKsReached},
		{"already saved", apperror.ErrConflict("dupe").WithCode(apperror.CodeNIKAlreadySaved), apperror.CodeNIKAlreadySaved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc, d := newTestUsecase(t)
			d.repo.EXPECT().HasConsent(gomock.Any(), gomock.Any()).Return(true, nil)
			d.verifier.EXPECT().VerifyNIK(gomock.Any(), gomock.Any()).Return(portout.NIKVerification{
				NIKHmac: "h", NIKMasked: "3171**********33", Valid: true, Status: "ACTIVE",
			}, nil)
			d.repo.EXPECT().CreateSavedNIKWithinCap(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(entity.SavedNIK{}, tc.err)

			_, err := uc.AddSavedNIK(context.Background(), testAccountID, addReq())
			var appErr *apperror.AppError
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, 409, appErr.HTTPStatus)
			assert.Equal(t, tc.code, appErr.ErrorCode)
		})
	}
}

func TestAddSavedNIK_ConsentCheckFailure(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().HasConsent(gomock.Any(), gomock.Any()).Return(false, errors.New("db down"))

	_, err := uc.AddSavedNIK(context.Background(), testAccountID, addReq())
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 500, appErr.HTTPStatus)
}

// ---- DeleteSavedNIK ----

func TestDeleteSavedNIK_Success(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().DeleteSavedNIK(gomock.Any(), testAccountID, "SNK-1").Return(nil)
	assert.NoError(t, uc.DeleteSavedNIK(context.Background(), testAccountID, "SNK-1"))
}

func TestDeleteSavedNIK_NotFoundAndNotYoursAreIndistinguishable(t *testing.T) {
	// A 403 would confirm the ID exists (§9 object ownership).
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().DeleteSavedNIK(gomock.Any(), testAccountID, "SNK-other").
		Return(apperror.ErrNotFound("saved NIK not found"))

	err := uc.DeleteSavedNIK(context.Background(), testAccountID, "SNK-other")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 404, appErr.HTTPStatus)
}

func TestDeleteSavedNIK_UnexpectedError(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().DeleteSavedNIK(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	err := uc.DeleteSavedNIK(context.Background(), testAccountID, "SNK-1")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 500, appErr.HTTPStatus)
}
