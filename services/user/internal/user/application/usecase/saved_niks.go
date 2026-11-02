package usecase

import (
	"context"
	"time"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/idgen"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/mapper"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/valueobject"
)

// GetSavedNIKs returns the account's saved friend-NIK list.
// Implements GET /accounts/me/saved-niks (FR-15a).
func (u *UserUsecase) GetSavedNIKs(ctx context.Context, accountID string) (dto.SavedNIKsResponse, error) {
	niks, err := u.repo.FindSavedNIKsByAccountID(ctx, accountID)
	if err != nil {
		return dto.SavedNIKsResponse{}, apperror.ErrInternal("failed to retrieve saved NIKs")
	}

	items := make([]dto.SavedNIKItem, 0, len(niks))
	for _, n := range niks {
		// Raw NIK is not stored; display the masked placeholder.
		// In production the masked form would be stored in a separate column.
		items = append(items, mapper.ToSavedNIKItem(n, valueobject.MaskNIK("????????????????")))
	}

	return dto.SavedNIKsResponse{
		MaxEntries: entity.MaxSavedNIKs,
		SavedNIKs:  items,
	}, nil
}

// AddSavedNIK verifies a NIK with Dukcapil and, if valid, stores it on the account.
// The row is only created when verification succeeds — no status PENDING (FR-15a, ADR-005 §8).
// Implements POST /accounts/me/saved-niks.
func (u *UserUsecase) AddSavedNIK(ctx context.Context, accountID string, req dto.AddSavedNIKRequest) (dto.SavedNIKItem, error) {
	count, err := u.repo.CountSavedNIKsByAccountID(ctx, accountID)
	if err != nil {
		return dto.SavedNIKItem{}, apperror.ErrInternal("failed to check saved NIK count")
	}
	if count >= entity.MaxSavedNIKs {
		return dto.SavedNIKItem{}, apperror.ErrConflict("MAX_SAVED_NIKS_REACHED")
	}

	nikHmac := u.hashNIK(req.NIK)

	alreadySaved, err := u.repo.IsNIKAlreadySaved(ctx, accountID, nikHmac)
	if err != nil {
		return dto.SavedNIKItem{}, apperror.ErrInternal("failed to check NIK uniqueness")
	}
	if alreadySaved {
		return dto.SavedNIKItem{}, apperror.ErrConflict("NIK_ALREADY_SAVED")
	}

	// Dukcapil verification — fail closed (ADR-005 §8).
	result, err := u.dukcapil.VerifyNIK(ctx, req.NIK)
	if err != nil {
		return dto.SavedNIKItem{}, apperror.ErrServiceUnavailable("DUKCAPIL_UNAVAILABLE")
	}
	if !result.Valid {
		return dto.SavedNIKItem{}, apperror.ErrUnprocessable("NIK validation failed: " + result.Status)
	}

	now := time.Now().UTC()
	nik := entity.SavedNIK{
		ID:          idgen.NewPrefixed("SNK-"),
		AccountID:   accountID,
		NIKHmac:     nikHmac,
		Label:       req.Label,
		VerifiedAt:  now,
		DukcapilRef: result.Ref,
	}

	saved, err := u.repo.SaveSavedNIK(ctx, nik)
	if err != nil {
		return dto.SavedNIKItem{}, apperror.ErrInternal("failed to save NIK")
	}

	return mapper.ToSavedNIKItem(saved, req.NIK), nil
}

// DeleteSavedNIK removes a saved NIK from the account.
// Deletion does not affect tickets already purchased with that NIK (FR-15a).
// Implements DELETE /accounts/me/saved-niks/{id}.
func (u *UserUsecase) DeleteSavedNIK(ctx context.Context, accountID, nikID string) error {
	_, err := u.repo.FindSavedNIKByID(ctx, accountID, nikID)
	if err != nil {
		// Not found or belongs to a different account — return 404 in both cases
		// to avoid confirming that the ID exists.
		return apperror.ErrNotFound("saved NIK not found")
	}
	if err := u.repo.DeleteSavedNIK(ctx, accountID, nikID); err != nil {
		return apperror.ErrInternal("failed to delete saved NIK")
	}
	return nil
}
