package inbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/dto"
)

// INikUsecase is the inbound port for NIK verification.
//
// One method: everything that needs a NIK verified — registration, adding a
// saved NIK, checkout — asks the same question and gets the same guarantees
// (rate limit, breaker, cache, fail-closed). Adding a second entry point is how
// one caller ends up bypassing the guards.
type INikUsecase interface {
	Validate(ctx context.Context, req dto.ValidateRequest) (dto.ValidateResponse, error)
}
