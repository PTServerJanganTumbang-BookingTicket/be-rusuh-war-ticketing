package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

func statusOf(t *testing.T, h *harness, b entity.Booking) dto.StatusResponse {
	t.Helper()
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) { return b, nil }
	out, err := h.uc.GetStatus(context.Background(), b.ID, "grant-abc")
	require.NoError(t, err)
	return out
}

// paid_at is the GATEWAY's settlement time, stored on the booking by the
// webhook (migration 0013) — not the payment deadline, which is what this
// endpoint reported before the column existed. For a customer who paid at
// minute 2 of a ten-minute window, the deadline was a timestamp eight minutes
// in the future of the response carrying it.
func TestGetStatus_Confirmed_IsPaidWithTheSettlementTime(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusConfirmed)
	// Paid at minute 2 of a window that opened five minutes before fixedNow.
	paid := b.HeldAt.Add(2 * time.Minute)
	b.PaidAt = &paid

	out := statusOf(t, h, b)

	assert.Equal(t, dto.StatusPaid, out.Status)
	assert.Equal(t, "/bookings/RB-00912/success", out.RedirectURL)
	assert.Equal(t, paid.UTC().Format(time.RFC3339), out.PaidAt)
	assert.NotEqual(t, b.PaymentExpiresAt.UTC().Format(time.RFC3339), out.PaidAt,
		"the deadline is not the payment time")
	// And it is in the past, which the old behaviour could not promise.
	assert.True(t, paid.Before(fixedNow))
	// A settled booking has no deadline left to show.
	assert.Empty(t, out.ExpiresAt)
}

// Bookings confirmed before migration 0013 have no paid_at. The field is
// omitted rather than substituted: a wrong timestamp on a receipt is worse
// than an absent one, and the settlement time is still in payment_events.raw
// for anyone who needs it.
func TestGetStatus_ConfirmedBeforeThePaidAtColumn_OmitsIt(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusConfirmed)
	b.PaidAt = nil

	out := statusOf(t, h, b)
	assert.Equal(t, dto.StatusPaid, out.Status)
	assert.Empty(t, out.PaidAt)
	assert.Equal(t, "/bookings/RB-00912/success", out.RedirectURL)
}

func TestGetStatus_ExpiredAndLapsed_AreExpired(t *testing.T) {
	for _, st := range []entity.BookingStatus{entity.StatusExpired, entity.StatusLapsed} {
		t.Run(string(st), func(t *testing.T) {
			h := newHarness(t)
			out := statusOf(t, h, heldBooking(st))
			assert.Equal(t, dto.StatusExpired, out.Status)
			assert.NotEmpty(t, out.ExpiredAt)
			assert.Nil(t, out.CanRetry)
		})
	}
}

// The distinction this endpoint exists for. HELD with attempts is the ONLY
// signal the customer gets that a charge was refused; without it they watch a
// countdown run out knowing nothing.
func TestGetStatus_HeldAfterDecline_IsFailedAndRetryable(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	b.PaymentAttempts = 2

	out := statusOf(t, h, b)

	assert.Equal(t, dto.StatusFailed, out.Status)
	assert.NotEmpty(t, out.Reason)
	require.NotNil(t, out.CanRetry)
	assert.True(t, *out.CanRetry)
	require.NotNil(t, out.Attempt)
	assert.Equal(t, 2, *out.Attempt)
	// The deadline is reported unchanged, so the client cannot mistake a
	// failure for a fresh window.
	assert.Equal(t, b.PaymentExpiresAt.UTC().Format(time.RFC3339), out.PaymentExpiresAt)
}

// HELD with no attempts is "not started", not a failure.
func TestGetStatus_HeldBeforeAnyCharge_IsPending(t *testing.T) {
	h := newHarness(t)
	out := statusOf(t, h, heldBooking(entity.StatusHeld))

	assert.Equal(t, dto.StatusPending, out.Status)
	assert.Nil(t, out.CanRetry)
	// An absolute deadline, not a countdown: contract v2.15 #107 replaced
	// ttl_seconds because a number computed at the server and rendered after
	// the round trip is already wrong, and wrong in the customer's favour
	// right up to the moment the seats go.
	assert.Equal(t, "2026-03-01T10:10:00Z", out.ExpiresAt)
	assert.Equal(t, fixedNow.Format(time.RFC3339), out.ServerTime)
}

func TestGetStatus_AtMaxAttempts_CannotRetry(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	b.PaymentAttempts = h.cfg.MaxPaymentAttempts

	out := statusOf(t, h, b)

	assert.Equal(t, dto.StatusFailed, out.Status)
	require.NotNil(t, out.CanRetry)
	assert.False(t, *out.CanRetry)
}

// The deadline has passed and the grace period is running. Reporting EXPIRED
// here would be the "jangan PHP" failure: a callback may still arrive and
// confirm, and the customer would have been told their seats were gone.
func TestGetStatus_PendingCancellation_IsStillPending(t *testing.T) {
	h := newHarness(t)
	out := statusOf(t, h, heldBooking(entity.StatusPendingCancellation))

	assert.Equal(t, dto.StatusPending, out.Status)
	assert.NotEmpty(t, out.ExpiresAt)
	assert.Empty(t, out.ExpiredAt)
}

// expires_at and server_time travel together: the pair is what lets a client
// with a skewed clock draw a correct countdown, which is the whole reason
// #107 moved the arithmetic to the client.
func TestGetStatus_AwaitingPayment_ReportsTheDeadlineAndTheServerClock(t *testing.T) {
	h := newHarness(t)
	out := statusOf(t, h, heldBooking(entity.StatusAwaitingPayment))

	assert.Equal(t, dto.StatusPending, out.Status)
	assert.Equal(t, "2026-03-01T10:10:00Z", out.ExpiresAt)
	assert.Equal(t, fixedNow.Format(time.RFC3339), out.ServerTime)
}

// Statuses that a payment route cannot reach still have to answer something
// coherent: an empty status field would leave the client with nothing to
// render.
func TestGetStatus_NonPaymentStatuses_StillAnswer(t *testing.T) {
	for _, st := range []entity.BookingStatus{
		entity.StatusGranted, entity.StatusSelecting, entity.StatusAbandoned,
	} {
		t.Run(string(st), func(t *testing.T) {
			h := newHarness(t)
			out := statusOf(t, h, heldBooking(st))
			assert.Equal(t, dto.StatusPending, out.Status)
			assert.NotEmpty(t, out.Status)
		})
	}
}

// A deadline in the past is reported AS IT IS, not clamped to now. Clamping
// was what ttl_seconds had to do, and it hid the one case the client needs to
// tell apart: a window that has genuinely run out while a callback is still
// in flight. The client clamps its own countdown at zero.
func TestGetStatus_PastDeadline_IsReportedUnchanged(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusAwaitingPayment)
	past := fixedNow.Add(-30 * time.Second)
	b.PaymentExpiresAt = &past

	out := statusOf(t, h, b)
	assert.Equal(t, past.UTC().Format(time.RFC3339), out.ExpiresAt)
	assert.Equal(t, fixedNow.Format(time.RFC3339), out.ServerTime)
}

func TestGetStatus_UnknownBooking_Is404(t *testing.T) {
	h := newHarness(t)
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return entity.Booking{}, portout.ErrNotFound
	}

	_, err := h.uc.GetStatus(context.Background(), "RB-00001", "grant-abc")
	assertAppError(t, err, 404, "NOT_FOUND")
}

// ServerTime is present on every polled answer so the client can correct its
// own clock drift instead of trusting the browser's.
func TestGetStatus_ServerTimeOnPollingAnswers(t *testing.T) {
	h := newHarness(t)
	for _, st := range []entity.BookingStatus{
		entity.StatusAwaitingPayment, entity.StatusPendingCancellation, entity.StatusHeld,
	} {
		out := statusOf(t, h, heldBooking(st))
		assert.Equal(t, fixedNow.Format(time.RFC3339), out.ServerTime, string(st))
	}
}

// ---- payment detail (migration 0017) ----

// The detail is attached at a single seam in GetStatus rather than in each of
// the seven branches, so this walks every status: a branch added later that
// forgot it would fail here rather than silently answer without it.
func TestGetStatus_PaymentDetailIsPresentOnEveryStatus(t *testing.T) {
	amount := int64(3000000)
	for _, st := range []entity.BookingStatus{
		entity.StatusConfirmed, entity.StatusExpired, entity.StatusLapsed,
		entity.StatusHeld, entity.StatusAwaitingPayment, entity.StatusPendingCancellation,
	} {
		t.Run(string(st), func(t *testing.T) {
			h := newHarness(t)
			b := heldBooking(st)
			b.PaymentRef = "RB-00912:1"
			b.PaymentMethod = "bank_transfer"
			b.PaymentStatus = "settlement"
			b.PaymentAmount = &amount
			b.PaymentInstructions = map[string]string{"bank": "bca", "va_number": "8801234567890"}
			if st == entity.StatusConfirmed {
				paid := b.HeldAt.Add(2 * time.Minute)
				b.PaidAt = &paid
			}

			out := statusOf(t, h, b)

			assert.Equal(t, "RB-00912:1", out.PaymentID)
			assert.Equal(t, "bank_transfer", out.Method)
			assert.Equal(t, "settlement", out.PaymentStatus)
			require.NotNil(t, out.TotalAmount)
			assert.Equal(t, amount, *out.TotalAmount)
			assert.Equal(t, "8801234567890", out.Instructions["va_number"])
			assert.Equal(t, "bca", out.Instructions["bank"])
		})
	}
}

// A booking that never reached a charge must not report zeroes: "charged 0"
// and "not charged" are different claims, and omitempty is what separates them.
func TestGetStatus_NoChargeYet_OmitsThePaymentDetail(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	b.PaymentAttempts = 0
	b.PaymentRef, b.PaymentMethod, b.PaymentStatus = "", "", ""
	b.PaymentAmount = nil

	out := statusOf(t, h, b)

	assert.Empty(t, out.PaymentID)
	assert.Empty(t, out.Method)
	assert.Empty(t, out.PaymentStatus)
	assert.Nil(t, out.TotalAmount, "a nil amount must stay nil, not become 0")
	assert.Nil(t, out.Instructions, "no charge means nothing to do, not an empty object")
}

// payment_status is the GATEWAY's view and bookings.status is ours. They are
// reported side by side precisely because they can disagree: a booking still
// AWAITING_PAYMENT whose last notification said deny is the case support
// needs to see without opening payment_events.
func TestGetStatus_GatewayStatusIsReportedSeparatelyFromOurs(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusAwaitingPayment)
	b.PaymentRef = "RB-00912:1"
	b.PaymentStatus = "deny"

	out := statusOf(t, h, b)

	assert.Equal(t, dto.StatusPending, out.Status, "our lifecycle status")
	assert.Equal(t, "deny", out.PaymentStatus, "the gateway's, unchanged")
}

// The VA number is the whole point of returning instructions on §5.4: a
// customer who closed the tab mid-payment reopens the status page and must
// still be able to pay. §5.2 returned it once and is not idempotent to
// re-trigger from the UI.
func TestGetStatus_PendingTransfer_CarriesTheVirtualAccount(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusAwaitingPayment)
	b.PaymentRef = "RB-00912:1"
	b.PaymentMethod = "bank_transfer"
	b.PaymentInstructions = map[string]string{"bank": "bca", "va_number": "8801234567890"}

	out := statusOf(t, h, b)

	assert.Equal(t, dto.StatusPending, out.Status)
	assert.Equal(t, "bank_transfer", out.Method)
	assert.Equal(t, "bca", out.Instructions["bank"])
	assert.Equal(t, "8801234567890", out.Instructions["va_number"])
}

// The shape is method-dependent, which is why it is a map: an e-wallet has no
// VA number and a transfer has no QR url. A struct would carry one NULL field
// for every payment ever made.
func TestGetStatus_PendingEWallet_CarriesTheQRAndDeeplink(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusAwaitingPayment)
	b.PaymentMethod = "gopay"
	b.PaymentInstructions = map[string]string{
		"qr_url":       "https://api.sandbox.veritrans.co.id/v2/gopay/tx-1/qr-code",
		"deeplink_url": "https://simulator.sandbox.midtrans.com/gopay/ui/checkout",
	}

	out := statusOf(t, h, b)

	assert.Equal(t, "gopay", out.Method)
	assert.Contains(t, out.Instructions["qr_url"], "qr-code")
	assert.Contains(t, out.Instructions["deeplink_url"], "checkout")
	assert.NotContains(t, out.Instructions, "va_number")
}
