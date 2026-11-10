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

func TestGetStatus_Confirmed_IsPaid(t *testing.T) {
	h := newHarness(t)
	out := statusOf(t, h, heldBooking(entity.StatusConfirmed))

	assert.Equal(t, dto.StatusPaid, out.Status)
	assert.Equal(t, "/bookings/RB-00912/success", out.RedirectURL)
	assert.NotEmpty(t, out.PaidAt)
	// A settled booking has no countdown left to show.
	assert.Nil(t, out.TTLSeconds)
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
	require.NotNil(t, out.TTLSeconds)
	assert.Equal(t, int((5 * time.Minute).Seconds()), *out.TTLSeconds)
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

// The TTL elapsed and the grace period is running. Reporting EXPIRED here
// would be the "jangan PHP" failure: a callback may still arrive and confirm,
// and the customer would have been told their seats were gone.
func TestGetStatus_PendingCancellation_IsPendingWithZeroTTL(t *testing.T) {
	h := newHarness(t)
	out := statusOf(t, h, heldBooking(entity.StatusPendingCancellation))

	assert.Equal(t, dto.StatusPending, out.Status)
	require.NotNil(t, out.TTLSeconds)
	assert.Equal(t, 0, *out.TTLSeconds)
	assert.Empty(t, out.ExpiredAt)
}

func TestGetStatus_AwaitingPayment_IsPendingWithRemainingTTL(t *testing.T) {
	h := newHarness(t)
	out := statusOf(t, h, heldBooking(entity.StatusAwaitingPayment))

	assert.Equal(t, dto.StatusPending, out.Status)
	require.NotNil(t, out.TTLSeconds)
	assert.Equal(t, 300, *out.TTLSeconds)
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

// A countdown is never negative: the client renders it directly, and "-12s"
// would reach a customer's screen.
func TestGetStatus_PastDeadline_TTLIsZeroNotNegative(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusAwaitingPayment)
	past := fixedNow.Add(-30 * time.Second)
	b.PaymentExpiresAt = &past

	out := statusOf(t, h, b)
	require.NotNil(t, out.TTLSeconds)
	assert.Equal(t, 0, *out.TTLSeconds)
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
