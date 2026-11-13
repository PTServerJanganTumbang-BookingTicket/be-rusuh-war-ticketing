package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// Retry implements POST /payments/{booking_id}/retry (§5.3) — cancel and
// switch method.
//
// Narrower than its name suggests. It is for ONE situation: a payment that is
// still pending, which the customer wants to abandon in favour of another
// method. It is NOT the path after a decline (that already returned the
// booking to HELD and raised the attempt, so §5.2 is the call), and NOT for
// changing the UI selection before pressing Pay (nothing exists at the gateway
// yet).
//
// The TTL is not reset. payment_expires_at stays anchored to held_at, so
// switching at minute 8 yields a new instrument with 2 minutes on it (BR-05).
func (u *PaymentUsecase) Retry(ctx context.Context, bookingID, grantToken string) (dto.RetryResponse, error) {
	cancel := func(ctx context.Context, orderID string) error {
		err := u.gateway.Cancel(ctx, orderID)
		// "Already paid" is Midtrans answering, not Midtrans failing: it must not push the error alarm.
		if err != nil && !errors.Is(err, portout.ErrAlreadySettled) {
			u.metrics.Incr(MetricMidtransErrors)
		}
		return err
	}

	out, err := u.bookings.Retry(ctx, portout.RetryInput{
		BookingID:    bookingID,
		GrantToken:   grantToken,
		Now:          u.now(),
		MinRemaining: u.cfg.MinRetryRemaining,
		MaxAttempts:  u.cfg.MaxPaymentAttempts,
	}, cancel)

	switch {
	case errors.Is(err, portout.ErrAlreadySettled):
		// The old payment turned out to be paid as the cancel was attempted.
		// Nothing changes; the webhook confirms the booking normally.
		return dto.RetryResponse{}, apperror.
			ErrConflict("previous payment has already been settled").
			WithCode(apperror.CodePaymentAlreadySettled)
	case errors.Is(err, portout.ErrInvalidStatus):
		return dto.RetryResponse{}, apperror.
			ErrConflict("no pending payment to cancel").
			WithCode(apperror.CodeBookingStatusInvalid)
	case errors.Is(err, portout.ErrWindowClosed):
		return dto.RetryResponse{}, apperror.
			ErrConflict("payment window has closed").
			WithCode(apperror.CodePaymentWindowClosed)
	case errors.Is(err, portout.ErrMaxAttempts):
		return dto.RetryResponse{}, apperror.
			ErrTooManyRequests("too many payment attempts").
			WithCode(apperror.CodeMaxAttemptsExceeded)
	case errors.Is(err, portout.ErrGatewayUnavailable):
		// payment_attempts is deliberately NOT raised: the old VA is still
		// valid and the customer may pay it or try again.
		return dto.RetryResponse{}, apperror.
			ErrServiceUnavailable("payment gateway is unavailable").
			WithCode(apperror.CodePaymentGatewayUnavailable)
	case err != nil:
		return dto.RetryResponse{}, mapBookingErr(err)
	}

	resp := dto.RetryResponse{
		BookingID:        out.Booking.ID,
		Status:           string(out.Booking.Status),
		Attempt:          out.Attempt,
		CancelledOrderID: out.CancelledOrderID,
		ServerTime:       u.now().Format(time.RFC3339),
	}
	if out.Booking.PaymentExpiresAt != nil {
		resp.PaymentExpiresAt = out.Booking.PaymentExpiresAt.UTC().Format(time.RFC3339)
	}
	return resp, nil
}
