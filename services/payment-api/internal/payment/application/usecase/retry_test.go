package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// cancellingRepo runs the cancel closure, as the real repository does while
// holding the row - the cancel has to happen BEFORE the commit, or a failed
// cancel leaves a raised attempt counter next to a still-payable VA.
func cancellingRepo(b entity.Booking, cancelled string, attempt int) func(context.Context, portout.RetryInput, portout.CancelFunc) (portout.RetryOutcome, error) {
	return func(ctx context.Context, in portout.RetryInput, cancel portout.CancelFunc) (portout.RetryOutcome, error) {
		if err := cancel(ctx, cancelled); err != nil {
			return portout.RetryOutcome{}, err
		}
		return portout.RetryOutcome{Booking: b, CancelledOrderID: cancelled, Attempt: attempt}, nil
	}
}

func TestRetry_OK(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	h.repo.retryFn = cancellingRepo(b, "RB-00912_1", 2)

	out, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)

	assert.Equal(t, "RB-00912", out.BookingID)
	assert.Equal(t, string(entity.StatusHeld), out.Status)
	assert.Equal(t, 2, out.Attempt)
	assert.Equal(t, "RB-00912_1", out.CancelledOrderID)
	assert.Equal(t, fixedNow.Format(time.RFC3339), out.ServerTime)
	assert.Equal(t, 1, h.gw.cancelCalls)
}

// The deadline is untouched. A customer who switches at minute 8 of a ten
// minute window gets an instrument with two minutes on it, not a fresh ten
// (BR-05).
func TestRetry_DoesNotResetTheWindow(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	h.repo.retryFn = cancellingRepo(b, "RB-00912_1", 2)

	out, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	assert.Equal(t, b.PaymentExpiresAt.UTC().Format(time.RFC3339), out.PaymentExpiresAt)
}

func TestRetry_PassesPolicyToRepository(t *testing.T) {
	h := newHarness(t)
	h.repo.retryFn = cancellingRepo(heldBooking(entity.StatusHeld), "RB-00912_1", 2)

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)

	assert.Equal(t, "RB-00912", h.repo.lastRetry.BookingID)
	assert.Equal(t, "grant-abc", h.repo.lastRetry.GrantToken)
	assert.Equal(t, 60*time.Second, h.repo.lastRetry.MinRemaining)
	assert.Equal(t, 5, h.repo.lastRetry.MaxAttempts)
	assert.Equal(t, fixedNow, h.repo.lastRetry.Now)
}

// The payment turned out to be paid as the cancel was attempted. Nothing
// changes here; the webhook confirms the booking in the ordinary way.
func TestRetry_AlreadySettled_Is409(t *testing.T) {
	h := newHarness(t)
	h.repo.retryFn = func(context.Context, portout.RetryInput, portout.CancelFunc) (portout.RetryOutcome, error) {
		return portout.RetryOutcome{}, portout.ErrAlreadySettled
	}

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assertAppError(t, err, 409, apperror.CodePaymentAlreadySettled)
}

// Not the path after a decline: a decline already returned the booking to HELD
// and raised the attempt, so §5.2 is the call. With nothing pending at the
// gateway there is nothing to void.
func TestRetry_NothingPending_Is409(t *testing.T) {
	h := newHarness(t)
	h.repo.retryFn = func(context.Context, portout.RetryInput, portout.CancelFunc) (portout.RetryOutcome, error) {
		return portout.RetryOutcome{}, portout.ErrInvalidStatus
	}

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assertAppError(t, err, 409, apperror.CodeBookingStatusInvalid)
}

// Below the floor the switch is refused rather than served: handing out a new
// virtual account with fifteen seconds of life only moves the failure
// somewhere more painful for the customer.
func TestRetry_WindowClosed_Is409(t *testing.T) {
	h := newHarness(t)
	h.repo.retryFn = func(context.Context, portout.RetryInput, portout.CancelFunc) (portout.RetryOutcome, error) {
		return portout.RetryOutcome{}, portout.ErrWindowClosed
	}

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assertAppError(t, err, 409, apperror.CodePaymentWindowClosed)
}

func TestRetry_MaxAttempts_Is429(t *testing.T) {
	h := newHarness(t)
	h.repo.retryFn = func(context.Context, portout.RetryInput, portout.CancelFunc) (portout.RetryOutcome, error) {
		return portout.RetryOutcome{}, portout.ErrMaxAttempts
	}

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assertAppError(t, err, 429, apperror.CodeMaxAttemptsExceeded)
}

// The cancel failed, so the old instrument is still valid and the attempt
// counter was deliberately NOT raised: the customer may still pay the VA they
// have, or try the switch again.
func TestRetry_CancelFails_Is503(t *testing.T) {
	h := newHarness(t)
	h.gw.cancelFn = func(context.Context, string) error { return errors.New("context deadline exceeded") }
	h.repo.retryFn = func(ctx context.Context, in portout.RetryInput, cancel portout.CancelFunc) (portout.RetryOutcome, error) {
		if err := cancel(ctx, "RB-00912_1"); err != nil {
			// The real repository rolls back and reports this sentinel.
			return portout.RetryOutcome{}, portout.ErrGatewayUnavailable
		}
		return portout.RetryOutcome{}, nil
	}

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assertAppError(t, err, 503, apperror.CodePaymentGatewayUnavailable)
	assert.Equal(t, 1, h.gw.cancelCalls)
}

func TestRetry_UnknownBooking_Is404(t *testing.T) {
	h := newHarness(t)
	h.repo.retryFn = func(context.Context, portout.RetryInput, portout.CancelFunc) (portout.RetryOutcome, error) {
		return portout.RetryOutcome{}, portout.ErrNotFound
	}

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assertAppError(t, err, 404, apperror.CodeNotFound)
}

// An unexpected repository failure stays a 500 rather than being flattened
// into one of the policy responses above.
func TestRetry_UnexpectedError_IsPassedThrough(t *testing.T) {
	h := newHarness(t)
	boom := errors.New("postgres: deadlock detected")
	h.repo.retryFn = func(context.Context, portout.RetryInput, portout.CancelFunc) (portout.RetryOutcome, error) {
		return portout.RetryOutcome{}, boom
	}

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assert.ErrorIs(t, err, boom)
}

// Cancel, not Expire. Nothing expired when the customer chose another method,
// and Midtrans reports the two separately in its dashboard.
func TestRetry_UsesCancelNotExpire(t *testing.T) {
	h := newHarness(t)
	var gotOrderID string
	h.gw.cancelFn = func(_ context.Context, orderID string) error {
		gotOrderID = orderID
		return nil
	}
	h.repo.retryFn = cancellingRepo(heldBooking(entity.StatusHeld), "RB-00912_1", 2)

	_, err := h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	assert.Equal(t, "RB-00912_1", gotOrderID)
}
