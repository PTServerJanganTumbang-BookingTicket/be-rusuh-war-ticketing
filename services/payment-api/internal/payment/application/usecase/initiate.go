package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// Initiate implements POST /payments/{booking_id} (§5.2).
//
// The ordering is ADR-005 §9 step 1 and it is load bearing throughout:
//
//  1. lock the owned row — ownership before any status talk, so a guessed
//     booking_id cannot learn a status
//  2. replay, confirm or reject on status — BEFORE the breaker, because a
//     replay calls no gateway and must keep working while it is open
//  3. derive order_id from stored state — never from the client
//  4. charge under the held lock, so two Pay clicks serialise on the row
//  5. write status, then COMMIT
func (u *PaymentUsecase) Initiate(ctx context.Context, bookingID, grantToken string, req dto.InitiateRequest) (dto.InitiateResponse, error) {
	if req.Method != entity.MethodBankTransfer && req.Method != entity.MethodGoPay {
		return dto.InitiateResponse{}, apperror.
			ErrBadRequest("unsupported payment method").
			WithCode(apperror.CodeValidationError)
	}
	if req.Method == entity.MethodBankTransfer && !entity.SupportedBank(req.Bank) {
		// A distinct code, because "bca typo'd as bcaa" and "method missing"
		// are different fixes for the client.
		return dto.InitiateResponse{}, apperror.
			ErrBadRequest("bank is not supported").
			WithCode(apperror.CodeBankNotSupported)
	}

	// The kill switch is checked HERE and not only on §5.1, because a customer
	// who loaded the payment page before an operator switched BCA off still
	// has a BCA button on screen. A switch enforced only on the listing does
	// nothing for exactly the people already mid-checkout, who are the ones
	// about to lose seats to an instrument they cannot pay.
	//
	// Before the booking is locked and before any gateway call: nothing has
	// changed yet, so refusing costs the customer only a choice of method.
	if off := u.disabledMethods(ctx); off != nil {
		switch {
		case off.MethodOff(req.Method):
			slog.InfoContext(ctx, "charge refused, method switched off by an operator",
				slog.String("method", req.Method), slog.String("booking_id", bookingID))
			return dto.InitiateResponse{}, apperror.
				ErrServiceUnavailable("payment method is temporarily unavailable").
				WithCode(apperror.CodePaymentGatewayUnavailable)
		case req.Bank != "" && off.BankOff(req.Method, req.Bank):
			slog.InfoContext(ctx, "charge refused, bank switched off by an operator",
				slog.String("method", req.Method), slog.String("bank", req.Bank),
				slog.String("booking_id", bookingID))
			return dto.InitiateResponse{}, apperror.
				ErrServiceUnavailable("selected bank is temporarily unavailable").
				WithCode(apperror.CodePaymentGatewayUnavailable)
		}
	}

	var gatewayErr error
	charge := func(ctx context.Context, b entity.Booking, orderID string) (portout.ChargeResult, error) {
		allowed, err := u.breaker.Allow(ctx)
		if err != nil {
			// Fail OPEN on a breaker read error. The breaker exists to spare
			// Midtrans, not to become a second thing that can refuse payments.
			slog.WarnContext(ctx, "breaker unavailable, attempting charge", slog.Any("err", err))
			allowed = true
		}
		if !allowed {
			return portout.ChargeResult{}, errBreakerOpen
		}

		heldAt := u.now()
		if b.HeldAt != nil {
			heldAt = *b.HeldAt
		}
		res, err := u.gateway.Charge(ctx, portout.ChargeRequest{
			OrderID:     orderID,
			GrossAmount: b.Total, // summed from booking_items under the lock
			PaymentType: req.Method,
			Bank:        req.Bank,
			// order_time = held_at. Anchoring on the request time would hand
			// every method switch a fresh 10 minutes and BR-05 would never be
			// enforced.
			HeldAt:        heldAt,
			ExpirySeconds: int(u.cfg.PaymentTTL / time.Second),
		})
		if err != nil {
			// A timeout is NOT a failed charge: the instrument may exist. Do
			// not re-send. Record the failure, let the transaction roll back
			// with payment_ref still NULL, and leave the truth to the charge
			// reconciler (ADR-005 §8, §9).
			if recErr := u.breaker.RecordFailure(ctx); recErr != nil {
				slog.WarnContext(ctx, "breaker record failure failed", slog.Any("err", recErr))
			}
			gatewayErr = err
			return portout.ChargeResult{}, err
		}
		if recErr := u.breaker.RecordSuccess(ctx); recErr != nil {
			slog.WarnContext(ctx, "breaker record success failed", slog.Any("err", recErr))
		}
		return res, nil
	}

	out, err := u.bookings.Initiate(ctx, portout.InitiateInput{
		BookingID:  bookingID,
		GrantToken: grantToken,
		Method:     req.Method,
		Bank:       req.Bank,
		Now:        u.now(),
	}, charge)

	switch {
	case errors.Is(err, errBreakerOpen):
		return dto.InitiateResponse{}, apperror.
			ErrServiceUnavailable("payment gateway is unavailable").
			WithCode(apperror.CodePaymentGatewayUnavailable)
	case gatewayErr != nil && err != nil:
		return dto.InitiateResponse{}, apperror.
			ErrServiceUnavailable("payment gateway is unavailable").
			WithCode(apperror.CodePaymentGatewayUnavailable)
	case errors.Is(err, portout.ErrInvalidStatus):
		return dto.InitiateResponse{}, apperror.
			ErrConflict("booking is not awaiting a charge").
			WithCode(apperror.CodeBookingStatusInvalid)
	case err != nil:
		return dto.InitiateResponse{}, mapBookingErr(err)
	}

	now := u.now()
	resp := dto.InitiateResponse{
		PaymentID:    out.OrderID,
		Method:       req.Method,
		Status:       dto.StatusPending,
		Instructions: out.Instructions,
		TTL: dto.TTL{
			ServerTime: now.Format(time.RFC3339),
		},
	}
	if out.Booking.PaymentExpiresAt != nil {
		resp.TTL.ExpiresAt = out.Booking.PaymentExpiresAt.UTC().Format(time.RFC3339)
	}
	if out.Declined {
		// A synchronous refusal is still a 200: the request succeeded, the
		// payment did not. payment_attempts has already been raised, so the
		// customer picks another method and calls THIS endpoint again — not
		// §5.3, which exists only to void a still-pending instrument.
		resp.Status = dto.StatusFailed
	}
	return resp, nil
}
