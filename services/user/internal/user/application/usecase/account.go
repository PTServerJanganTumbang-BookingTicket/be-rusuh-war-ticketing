package usecase

import (
	"context"
	"errors"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/mapper"
)

// GetMyAccount returns the session owner's profile: email and masked NIK.
//
// The account is resolved from the token's subject, never from anything the
// caller sends. There is no path parameter and no body, so "whose profile" is
// not a question this endpoint can be asked — which is what makes it safe for
// it to have no ownership check of its own.
//
// Implements GET /accounts/me (contract §1.8, FR-32c).
func (u *UserUsecase) GetMyAccount(ctx context.Context, accountID string) (dto.AccountProfileResponse, error) {
	account, err := u.repo.FindAccountByID(ctx, accountID)
	if err != nil {
		var appErr *apperror.AppError
		if errors.As(err, &appErr) && appErr.HTTPStatus == http.StatusNotFound {
			// A signed token for an account that no longer exists. 404 would be
			// the wrong answer twice over: the contract defines only 200 and 401
			// here (§1.8), and the resource is "me" — it exists exactly as long
			// as the session's subject does. So the session is what is wrong,
			// and TOKEN_INVALID rather than TOKEN_EXPIRED, because refreshing
			// would hand back another token for the same missing account and
			// the client would loop.
			return dto.AccountProfileResponse{}, apperror.
				ErrUnauthorized("session does not belong to an existing account").
				WithCode(apperror.CodeTokenInvalid)
		}
		return dto.AccountProfileResponse{}, apperror.ErrInternal("failed to read account")
	}

	return mapper.ToAccountProfile(account), nil
}
