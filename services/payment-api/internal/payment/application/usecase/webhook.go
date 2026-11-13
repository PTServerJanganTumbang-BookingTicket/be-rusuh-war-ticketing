package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/valueobject"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// HandleNotification implements POST /payments/webhook (§5.5).
//
// The endpoint answers 200 to everything — a duplicate, a bad signature, an
// unknown order. That is not laziness: Midtrans retries any non-2xx, so a 4xx
// for a notification we have already applied turns one event into a retry
// storm, and a 4xx for a bad signature tells a prober that their guess was
// wrong. The error returned here is for OUR logs; the HTTP layer discards it.
func (u *PaymentUsecase) HandleNotification(ctx context.Context, n dto.Notification) error {
	// 1. Signature first. Nothing is read, written or even parsed on an
	//    unverified body.
	//
	//    gross_amount goes into the hash EXACTLY as received, decimals and
	//    all: "3000000.00" and "3000000" hash differently and only the former
	//    matches. That is why the field is a string all the way to here.
	//    The last argument is the SIGNATURE SECRET KEY, never the server key:
	//    Midtrans split the two on 28 October 2026 and the values only
	//    coincide until one of them is regenerated.
	if !valueobject.VerifySignature(n.SignatureKey, n.OrderID, n.StatusCode, n.GrossAmount, u.cfg.SignatureSecretKey) {
		// Ignore it completely. Do not process, do not change any status, and
		// do not tell the sender why.
		slog.WarnContext(ctx, "payment notification signature mismatch, ignoring",
			slog.String("order_id", n.OrderID),
			slog.String("transaction_status", n.TransactionStatus))
		return errors.New("payment: signature mismatch")
	}

	bookingID, _, err := valueobject.ParseOrderID(n.OrderID)
	if err != nil {
		slog.ErrorContext(ctx, "payment notification has malformed order_id",
			slog.String("order_id", n.OrderID))
		return err
	}

	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}

	out, err := u.bookings.ApplyNotification(ctx, portout.NotificationInput{
		BookingID:         bookingID,
		GatewayEventID:    n.GatewayEventID(),
		OrderID:           n.OrderID,
		TransactionStatus: n.TransactionStatus,
		FraudStatus:       n.FraudStatus,
		PaymentType:       n.PaymentType,
		GrossAmount:       parseGrossAmount(n.GrossAmount),
		Raw:               raw,
		Now:               u.now(),
		PaidAt:            n.PaidAt(),
		MaxAttempts:       u.cfg.MaxPaymentAttempts,
	})
	if errors.Is(err, portout.ErrDuplicateEvent) {
		// A redelivery. The UNIQUE on gateway_event_id tripped, the whole
		// transaction rolled back, and there is nothing left to do.
		slog.InfoContext(ctx, "duplicate payment notification ignored",
			slog.String("gateway_event_id", n.GatewayEventID()))
		return nil
	}
	if errors.Is(err, portout.ErrNotFound) {
		// An order_id we never issued, or one whose booking is gone. Logged,
		// not retried: answering non-2xx would make Midtrans redeliver
		// something that can never succeed.
		slog.WarnContext(ctx, "payment notification for unknown booking",
			slog.String("order_id", n.OrderID))
		return err
	}
	if err != nil {
		// A real failure. Returning the error lets the HTTP layer log it; the
		// response is still 200, and the AWAITING_PAYMENT sweep is the backstop:
		// the booking has a payment_ref, so the sweep polls the gateway and
		// applies whatever truth it finds.
		return err
	}

	u.countNotification(out)

	if out.Action == valueobject.ActionLateSettlement {
		// Money arrived after the seats were resold. The booking is already
		// marked disputed and a payment.failed_notice is queued inside the
		// transaction; this is the operator-facing half. Automatic refund is
		// deliberately NOT implemented in the POC — a refund moves money out,
		// and an automated path triggered by a callback is the most expensive
		// thing to get wrong.
		slog.ErrorContext(ctx, "LatePaymentRefund: settlement after booking expiry",
			slog.String("event_id", out.EventID),
			slog.String("booking_id", bookingID),
			slog.String("order_id", n.OrderID),
			slog.String("gross_amount", n.GrossAmount),
			slog.String("alarm", "LatePaymentRefund"))
	}

	// 2. Post-commit, best effort. Credential lifecycle only.
	//
	//    ADR-006 §3.2 deleted slots:open and §4.1 removed this block's queue
	//    half — the DECR, the advance_now_serving EVALSHA and the sse-grant
	//    PUBLISH are gone "tanpa pengganti", because admission-ctl recounts
	//    from Aurora every tick and picks the freed seat up within 500 ms.
	//    advance_now_serving.lua now has exactly one caller.
	//
	//    What is left is idempotent, which is also why there is no lease
	//    guard: the lease existed to protect the one non-idempotent operation
	//    (the DECR) and that operation no longer exists.
	if out.Applied && out.Action == valueobject.ActionConfirm {
		if err := u.credentials.SettleEngagement(ctx, portout.SettleInput{
			EventID:      out.EventID,
			BookingID:    out.Booking.ID,
			QueueToken:   out.QueueToken,
			AccountID:    out.AccountID,
			TombstoneTTL: u.cfg.SettledTombstoneTTL,
		}); err != nil {
			// Never fails the request. The worst case is a queue credential
			// that outlives its engagement until its own TTL, which the
			// contract already treats as a bounded window (§3.3).
			slog.WarnContext(ctx, "could not settle engagement credentials", slog.Any("err", err))
		}
	}

	// Release the stock and NIK locks on expire. The database has already
	// committed — the booking is EXPIRED and nik_claims are gone — so a failure
	// here is logged but never fails the webhook: a non-2xx makes Midtrans
	// retry a notification that was already applied, and retrying the release
	// would double-credit inventory.
	//
	// Stock reconciliation (B5) repairs the keys if this fails; nik:used
	// lapses on its TTL. The worst case is frozen inventory until the next
	// reconciliation tick, which is minutes rather than hours.
	if out.Applied && out.Action == valueobject.ActionExpire && u.holds != nil {
		missing, err := u.holds.Release(ctx, out.EventID, out.Seats, out.NIKs)
		if err != nil {
			slog.ErrorContext(ctx, "could not release hold on expire; stock reconciliation will repair",
				slog.String("booking_id", bookingID),
				slog.String("event_id", out.EventID),
				slog.Any("err", err))
		}
		if missing > 0 {
			slog.WarnContext(ctx, "expire release found missing stock keys",
				slog.String("booking_id", bookingID),
				slog.Int("missing", missing))
		}
	}
	return nil
}

// parseGrossAmount converts Midtrans's decimal string to minor units for
// storage only.
//
// The signature already used the original string, so precision loss here
// cannot break verification. IDR has no circulating sub-unit and Midtrans
// always sends ".00", so the fractional part is dropped rather than rounded —
// rounding would invent a rupiah that was never charged.
func parseGrossAmount(s string) int64 {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// countNotification counts what a notification changed, once: only an applied transition counts, so a Midtrans
// redelivery (rejected as a duplicate before this) or a notification that changed nothing is never counted.
func (u *PaymentUsecase) countNotification(out portout.NotificationOutcome) {
	if !out.Applied {
		return
	}
	switch out.Action {
	case valueobject.ActionConfirm:
		u.metrics.IncrEvent(MetricBookingsConfirmed, out.EventID)
	case valueobject.ActionExpire:
		u.metrics.IncrEvent(MetricBookingsPaymentExpired, out.EventID)
	case valueobject.ActionLateSettlement:
		u.metrics.IncrEvent(MetricLateSettlement, out.EventID)
	}
}
