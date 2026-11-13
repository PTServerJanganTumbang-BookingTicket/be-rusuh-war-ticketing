package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/valueobject"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// SweepConfig holds the sweep tuning parameters.
type SweepConfig struct {
	// GraceSeconds is the delay after payment_expires_at before a booking
	// becomes a sweep candidate. Non-zero to let in-flight webhooks land.
	GraceSeconds int
	// BatchSize caps how many candidates are processed per invocation.
	BatchSize int
	// StuckMinutes is the threshold for logging bookings stuck due to gateway
	// unavailability.
	StuckMinutes int
	// MaxPaymentAttempts for ApplyNotification.
	MaxPaymentAttempts int
	// SettledTombstoneTTL is how long settled:{queue_token} answers §3.3 after
	// the queue credential is deleted. Must match the webhook's value.
	SettledTombstoneTTL time.Duration
}

// sweepOutcome is the explicit result of processing one candidate.
// F4: stop inferring the outcome from a zero Action value.
type sweepOutcome int

const (
	outcomeSkipped   sweepOutcome = iota // gateway unreachable, no payment_ref, or unfunded
	outcomeExpired                       // booking expired
	outcomeSettled                       // booking confirmed by sweep (alarm: webhook broken)
	outcomeDuplicate                     // already processed by webhook or previous sweep
)

// SweepResult is the outcome of one SweepAwaitingPayment invocation.
type SweepResult struct {
	Expired   int `json:"expired"`
	Settled   int `json:"settled"`
	Skipped   int `json:"skipped"`
	Duplicate int `json:"duplicate"`
}

// SweepUsecase handles the backstop sweep for AWAITING_PAYMENT bookings.
//
// When the webhook never arrives, this polls Midtrans and applies the same
// decision the webhook would have. It runs on a tick from admission-ctl.
type SweepUsecase struct {
	bookings    portout.ISweepRepository
	gateway     portout.IMidtransClient
	breaker     portout.ICircuitBreaker
	credentials portout.ICredentialStore
	holds       portout.IHolds
	cfg         SweepConfig

	now func() time.Time
}

// NewSweep creates the sweep use case. All dependencies are required.
func NewSweep(
	bookings portout.ISweepRepository,
	gateway portout.IMidtransClient,
	breaker portout.ICircuitBreaker,
	credentials portout.ICredentialStore,
	holds portout.IHolds,
	cfg SweepConfig,
) *SweepUsecase {
	return &SweepUsecase{
		bookings:    bookings,
		gateway:     gateway,
		breaker:     breaker,
		credentials: credentials,
		holds:       holds,
		cfg:         cfg,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// SweepAwaitingPayment processes bookings whose payment window has elapsed.
//
// Four rungs:
//  1. GRANTED → LAPSED (admission-ctl): never opened the ticket page.
//  2. SELECTING → EXPIRED (admission-ctl): opened but didn't press Pay.
//  3. HELD → EXPIRED (admission-ctl): pressed Pay but no charge within 10 min.
//  4. AWAITING_PAYMENT → EXPIRED or CONFIRMED (this sweep): charged but webhook
//     never arrived.
//
// For each candidate:
//  1. Skip if payment_ref is empty: no order exists at the gateway to poll.
//  2. Poll the gateway for the transaction status.
//  3. If gateway unreachable: skip, count — fail closed.
//  4. Use valueobject.Decide to determine the action.
//  5. If ActionExpire: call gateway.Expire first so Midtrans stops accepting.
//  6. Apply through the same ApplyNotification path the webhook uses.
//  7. Release Redis if the action is expire and it was applied.
func (u *SweepUsecase) SweepAwaitingPayment(ctx context.Context) (SweepResult, error) {
	now := u.now()
	candidates, err := u.bookings.DueAwaitingPayment(ctx, now, u.cfg.GraceSeconds, u.cfg.BatchSize)
	if err != nil {
		return SweepResult{}, fmt.Errorf("sweep: query due bookings: %w", err)
	}

	var result SweepResult
	stuckThreshold := now.Add(-time.Duration(u.cfg.StuckMinutes) * time.Minute)

	for _, c := range candidates {
		// Skip bookings without a payment_ref — they have no order at the
		// gateway to poll. These are charges that timed out before Midtrans
		// returned. Nothing resolves them automatically: the HELD sweep expires
		// the booking, and if the charge did exist and is paid later, the
		// late-settlement path (ActionLateSettlement, FR-36) raises it for an
		// operator. The stuck warning below is the only signal before that.
		if c.PaymentRef == "" {
			result.Skipped++
			if c.PaymentExpiresAt.Before(stuckThreshold) {
				slog.WarnContext(ctx, "sweep: booking stuck without payment_ref; needs manual reconciliation",
					slog.String("booking_id", c.ID),
					slog.Time("deadline", c.PaymentExpiresAt))
			}
			continue
		}

		outcome, err := u.processCandidate(ctx, c, now)
		if err != nil {
			// Gateway unreachable or error. Skip, don't expire — we cannot
			// prove the customer didn't pay.
			result.Skipped++
			if c.PaymentExpiresAt.Before(stuckThreshold) {
				slog.WarnContext(ctx, "sweep: booking stuck due to gateway unavailability",
					slog.String("booking_id", c.ID),
					slog.String("payment_ref", c.PaymentRef),
					slog.Time("deadline", c.PaymentExpiresAt),
					slog.Any("err", err))
			}
			continue
		}

		switch outcome {
		case outcomeSettled:
			result.Settled++
		case outcomeExpired:
			result.Expired++
		case outcomeDuplicate:
			result.Duplicate++
		default:
			// outcomeSkipped: unfunded, decline, or record-only.
			result.Skipped++
		}
	}

	return result, nil
}

// processCandidate polls one booking and applies the result.
func (u *SweepUsecase) processCandidate(ctx context.Context, c portout.AwaitingCandidate, now time.Time) (sweepOutcome, error) {
	// 1. Check breaker first.
	allowed, err := u.breaker.Allow(ctx)
	if err != nil {
		return outcomeSkipped, fmt.Errorf("breaker check: %w", err)
	}
	if !allowed {
		return outcomeSkipped, errors.New("breaker open")
	}

	// 2. Poll the gateway.
	status, err := u.gateway.Status(ctx, c.PaymentRef)
	if err != nil {
		_ = u.breaker.RecordFailure(ctx)
		return outcomeSkipped, fmt.Errorf("gateway status: %w", err)
	}
	_ = u.breaker.RecordSuccess(ctx)

	// 3. Decide what to do, using the same logic as the webhook.
	// The booking is known to be AWAITING_PAYMENT since that's what we queried.
	// NOTE: The local Decide call is informational only; ApplyNotification
	// re-decides under the row lock. We use the action here to know whether
	// to call gateway.Expire beforehand.
	action := valueobject.Decide(status.TransactionStatus, status.FraudStatus, entity.StatusAwaitingPayment)

	// 4. If expiring, tell Midtrans to stop accepting payment first.
	// This is a best-effort call: custom_expiry has already passed, so the
	// gateway should already be refusing. A failure here is logged, not fatal.
	if action == valueobject.ActionExpire {
		if err := u.gateway.Expire(ctx, c.PaymentRef); err != nil {
			slog.WarnContext(ctx, "sweep: could not expire order at gateway; proceeding anyway",
				slog.String("booking_id", c.ID),
				slog.String("payment_ref", c.PaymentRef),
				slog.Any("err", err))
		}
	}

	// 5. Build a synthetic notification input from the poll result.
	// The gateway_event_id format ensures:
	//   - It cannot collide with a real webhook notification.
	//   - A repeat poll of the same gateway state is absorbed as a duplicate.
	gatewayEventID := fmt.Sprintf("%s:poll:%s:%s", c.PaymentRef, status.TransactionStatus, status.StatusCode)

	out, err := u.bookings.ApplyNotification(ctx, portout.NotificationInput{
		BookingID:         c.ID,
		GatewayEventID:    gatewayEventID,
		OrderID:           c.PaymentRef,
		TransactionStatus: status.TransactionStatus,
		FraudStatus:       status.FraudStatus,
		PaymentType:       "", // Not available from Status response.
		GrossAmount:       parseGrossAmountFromStatus(status.GrossAmount),
		Raw:               status.Raw,
		Now:               now,
		PaidAt:            valueobject.GatewayTime(status.SettlementTime, status.TransactionTime),
		MaxAttempts:       u.cfg.MaxPaymentAttempts,
	})

	if errors.Is(err, portout.ErrDuplicateEvent) {
		// Already processed, either by a webhook or a previous sweep. This is
		// normal and means the system worked.
		slog.DebugContext(ctx, "sweep: duplicate event absorbed",
			slog.String("booking_id", c.ID),
			slog.String("gateway_event_id", gatewayEventID))
		return outcomeDuplicate, nil
	}
	if err != nil {
		return outcomeSkipped, fmt.Errorf("apply notification: %w", err)
	}

	// 6. Post-commit: credential cleanup on confirm, Redis release on expire.
	if out.Applied && out.Action == valueobject.ActionConfirm {
		// Settled by the sweep — the webhook never arrived. This is the alarm
		// condition: non-zero settled means the webhook path is broken.
		slog.WarnContext(ctx, "sweep: settled booking that webhook missed",
			slog.String("booking_id", c.ID),
			slog.String("payment_ref", c.PaymentRef),
			slog.String("alarm", "AwaitingSweepSettled"))

		if err := u.credentials.SettleEngagement(ctx, portout.SettleInput{
			EventID:      out.EventID,
			BookingID:    out.Booking.ID,
			QueueToken:   out.QueueToken,
			AccountID:    out.AccountID,
			TombstoneTTL: u.cfg.SettledTombstoneTTL,
		}); err != nil {
			slog.WarnContext(ctx, "sweep: could not settle engagement credentials",
				slog.String("booking_id", c.ID),
				slog.Any("err", err))
		}
		return outcomeSettled, nil
	}

	if out.Applied && out.Action == valueobject.ActionExpire && u.holds != nil {
		missing, err := u.holds.Release(ctx, out.EventID, out.Seats, out.NIKs)
		if err != nil {
			slog.ErrorContext(ctx, "sweep: could not release hold; stock reconciliation will repair",
				slog.String("booking_id", c.ID),
				slog.String("event_id", out.EventID),
				slog.Any("err", err))
		}
		if missing > 0 {
			slog.WarnContext(ctx, "sweep: release found missing stock keys",
				slog.String("booking_id", c.ID),
				slog.Int("missing", missing))
		}
		return outcomeExpired, nil
	}

	// out.Action is ActionDecline, ActionRecordOnly, ActionIgnoreUnfunded, etc.
	// or out.Applied is false (no state change).
	return outcomeSkipped, nil
}

// parseGrossAmountFromStatus converts the status response's gross_amount string
// to minor units. The Status response returns it as a string like "3000000.00".
func parseGrossAmountFromStatus(s string) int64 {
	// Reuse the webhook's parser; the format is identical.
	return parseGrossAmount(s)
}

// MarshalJSON makes SweepResult serializable for the Lambda response.
func (r SweepResult) MarshalJSON() ([]byte, error) {
	type alias SweepResult
	return json.Marshal(alias(r))
}
