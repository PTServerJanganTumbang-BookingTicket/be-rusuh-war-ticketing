package usecase

import (
	"context"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
)

// GetStatus implements GET /payments/{booking_id}/status (§5.4).
//
// This is the ONLY signal that a payment failed. Without it the customer
// watches a countdown run down with no idea the charge was refused, so the
// FAILED branch carries everything the frontend needs to send them back to the
// method list: can_retry, the attempt number, and the UNCHANGED deadline.
func (u *PaymentUsecase) GetStatus(ctx context.Context, bookingID, grantToken string) (dto.StatusResponse, error) {
	b, err := u.bookings.GetOwned(ctx, bookingID, grantToken)
	if err != nil {
		return dto.StatusResponse{}, mapBookingErr(err)
	}

	now := u.now()
	iso := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}

	switch b.Status {
	case entity.StatusConfirmed:
		return dto.StatusResponse{
			Status: dto.StatusPaid,
			// paid_at is not a column; the settlement time lives in
			// payment_events. The contract renders a timestamp here and the
			// booking's deadline is the closest stored instant, so the
			// frontend gets a value without a second query on a polling path.
			PaidAt:      iso(b.PaymentExpiresAt),
			RedirectURL: "/bookings/" + b.ID + "/success",
		}, nil

	case entity.StatusExpired, entity.StatusLapsed:
		return dto.StatusResponse{
			Status:    dto.StatusExpired,
			ExpiredAt: iso(b.PaymentExpiresAt),
		}, nil

	case entity.StatusHeld:
		// The distinction the whole endpoint exists for: HELD after a decline
		// is a FAILURE the customer must be told about, while HELD before any
		// charge is simply "not started". payment_attempts is what separates
		// them.
		if b.PaymentAttempts > 0 {
			canRetry := b.PaymentAttempts < u.cfg.MaxPaymentAttempts
			attempt := b.PaymentAttempts
			return dto.StatusResponse{
				Status:   dto.StatusFailed,
				Reason:   "Pembayaran ditolak penyedia",
				CanRetry: &canRetry,
				Attempt:  &attempt,
				// Recomputed from held_at, never reset. A customer who failed
				// at minute 8 has 2 minutes left, not a fresh 10 (BR-05).
				PaymentExpiresAt: iso(b.PaymentExpiresAt),
				ServerTime:       now.Format(time.RFC3339),
			}, nil
		}
		return dto.StatusResponse{
			Status: dto.StatusPending, ServerTime: now.Format(time.RFC3339),
			ExpiresAt: iso(b.PaymentExpiresAt),
		}, nil

	case entity.StatusPendingCancellation:
		// The TTL elapsed and the 30-second grace is running. Deliberately
		// PENDING, not EXPIRED: a callback may still arrive and confirm, and
		// showing an expiry screen here would be the "jangan PHP" failure
		// NFR-02 exists to prevent. The frontend shows "verifying payment…".
		// The deadline is reported as it stands - already in the past - and the
		// client's countdown lands on zero by itself. Nothing says "expired":
		// a callback may still arrive and confirm.
		return dto.StatusResponse{
			Status: dto.StatusPending, ServerTime: now.Format(time.RFC3339),
			ExpiresAt: iso(b.PaymentExpiresAt),
		}, nil

	default:
		// AWAITING_PAYMENT, and GRANTED/SELECTING/ABANDONED, which cannot be
		// reached through a payment route but must still answer something
		// coherent rather than an empty status.
		return dto.StatusResponse{
			Status: dto.StatusPending, ServerTime: now.Format(time.RFC3339),
			ExpiresAt: iso(b.PaymentExpiresAt),
		}, nil
	}
}
