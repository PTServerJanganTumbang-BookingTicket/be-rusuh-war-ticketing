package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/idgen"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/mapper"
)

// GetSavedNIKs returns the account's saved friend-NIK list.
//
// Masked forms only; the full NIK is never returned (FR-32c). The mask comes
// from the stored column, so this agrees with what AddSavedNIK returned.
//
// Implements GET /accounts/me/saved-niks.
func (u *UserUsecase) GetSavedNIKs(ctx context.Context, accountID string) (dto.SavedNIKsResponse, error) {
	niks, err := u.repo.FindSavedNIKsByAccountID(ctx, accountID)
	if err != nil {
		return dto.SavedNIKsResponse{}, apperror.ErrInternal("failed to retrieve saved NIKs")
	}

	items := make([]dto.SavedNIKItem, 0, len(niks))
	for _, n := range niks {
		items = append(items, mapper.ToSavedNIKItem(n))
	}

	return dto.SavedNIKsResponse{MaxEntries: u.maxSavedNIKs, SavedNIKs: items}, nil
}

// AddSavedNIK verifies a NIK and, only then, stores it.
//
// The order is consent → verify → insert, per ADR-005 §8:
//
//   - Consent first because this list holds OTHER PEOPLE's NIKs. Storing one
//     without a recorded consent is a UU PDP transfer with no legal basis
//     (FR-31c, NFR-11), and accounts predating the consent requirement exist.
//   - Verification before the insert because checkout treats a stored NIK as
//     proof and SKIPS Dukcapil. A row written before the answer arrives moves
//     the fail-closed hole one step back instead of closing it.
//
// Rejecting here costs the customer nothing: no stock is held and no countdown
// is running, so retrying in a few minutes loses nothing. That is what
// separates this from checkout, where a refusal stops a sale in progress.
//
// Implements POST /accounts/me/saved-niks.
func (u *UserUsecase) AddSavedNIK(ctx context.Context, accountID string, req dto.AddSavedNIKRequest) (dto.SavedNIKItem, error) {
	consented, err := u.repo.HasConsent(ctx, accountID)
	if err != nil {
		return dto.SavedNIKItem{}, apperror.ErrInternal("failed to check consent")
	}
	if !consented {
		return dto.SavedNIKItem{}, apperror.
			ErrUnprocessable("consent to third-party NIK processing is required").
			WithCode(apperror.CodeConsentRequired)
	}

	verification, err := u.verifier.VerifyNIK(ctx, req.NIK)
	if err != nil {
		return dto.SavedNIKItem{}, apperror.
			ErrServiceUnavailable("NIK verification is temporarily unavailable").
			WithCode(apperror.CodeDukcapilUnavailable)
	}
	if !verification.Valid {
		return dto.SavedNIKItem{}, apperror.
			ErrUnprocessable("NIK could not be verified: " + verification.Status).
			WithCode(apperror.CodeNIKValidationFailed)
	}

	nik := entity.SavedNIK{
		ID:          idgen.NewPrefixed(entity.SavedNIKIDPrefix),
		AccountID:   accountID,
		NIKHmac:     verification.NIKHmac,
		NIKMasked:   verification.NIKMasked,
		Label:       req.Label,
		VerifiedAt:  time.Now().UTC(),
		DukcapilRef: verification.Ref,
	}

	// The cap and the insert happen under one row lock on the account, so two
	// concurrent adds cannot both see four entries and both insert (BR-12).
	saved, err := u.repo.CreateSavedNIKWithinCap(ctx, nik, u.maxSavedNIKs)
	if err != nil {
		var appErr *apperror.AppError
		if errors.As(err, &appErr) {
			return dto.SavedNIKItem{}, appErr
		}
		return dto.SavedNIKItem{}, apperror.ErrInternal("failed to save NIK")
	}

	return mapper.ToSavedNIKItem(saved), nil
}

// DeleteSavedNIK removes an entry.
//
// Deletion does not touch tickets already bought with that NIK (FR-32d) — the
// tickets table carries its own hash and is not joined to this one.
//
// A missing entry and one belonging to another account both return 404: a 403
// would confirm that the ID exists, which §9 rules out.
//
// Implements DELETE /accounts/me/saved-niks/{id}.
func (u *UserUsecase) DeleteSavedNIK(ctx context.Context, accountID, nikID string) error {
	if err := u.repo.DeleteSavedNIK(ctx, accountID, nikID); err != nil {
		var appErr *apperror.AppError
		if errors.As(err, &appErr) {
			return appErr
		}
		return apperror.ErrInternal("failed to delete saved NIK")
	}
	return nil
}
