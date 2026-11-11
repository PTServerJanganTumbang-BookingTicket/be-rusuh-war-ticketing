package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// GetMethods implements GET /payments/{booking_id}/methods (§5.1).
//
// The catalogue is static, but the lookup is not pointless: the booking is
// resolved first so that an unknown or unowned booking_id answers 404 before
// anything is disclosed. Returning the method list to anyone who asks would
// make it an oracle for which booking ids exist.
func (u *PaymentUsecase) GetMethods(ctx context.Context, bookingID, grantToken string) (dto.MethodsResponse, error) {
	if _, err := u.bookings.GetOwned(ctx, bookingID, grantToken); err != nil {
		return dto.MethodsResponse{}, mapBookingErr(err)
	}

	// A breaker read that fails is not a reason to fail the request: the worst
	// outcome of guessing "enabled" here is a 503 at charge time, which §5.2
	// handles. Guessing "disabled" would hide every method over a Redis blip.
	allowed, err := u.breaker.Allow(ctx)
	if err != nil {
		slog.WarnContext(ctx, "breaker unavailable, offering methods as enabled", slog.Any("err", err))
		allowed = true
	}

	// The catalogue is what the code can charge; the switches are what an
	// operator has turned off since. Masking here rather than inside Catalogue
	// keeps the code the only place a method can be ADDED.
	methods := entity.ApplyDisabled(entity.Catalogue(u.cfg.IconBase, allowed), u.disabledMethods(ctx))
	out := dto.MethodsResponse{Methods: make([]dto.Method, 0, len(methods))}
	for _, m := range methods {
		row := dto.Method{
			ID: m.ID, Name: m.Name, Description: m.Description,
			IconURL: m.IconURL, Enabled: m.Enabled,
		}
		for _, b := range m.Banks {
			row.Banks = append(row.Banks, dto.Bank{Code: b.Code, Name: b.Name, IconURL: b.IconURL})
		}
		out.Methods = append(out.Methods, row)
	}
	return out, nil
}

// mapBookingErr turns repository sentinels into contract responses.
func mapBookingErr(err error) error {
	if errors.Is(err, portout.ErrNotFound) {
		// 404 for both "no such booking" and "not yours" — see ErrNotFound.
		return apperror.ErrNotFound("booking not found").WithCode(apperror.CodeNotFound)
	}
	return err
}
