package usecase

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// chargingRepo is a repository that behaves like the real one: it runs the
// charge closure it is given. Everything about breaker placement, order_time
// anchoring and timeout handling lives inside that closure, so a double that
// skipped it would test nothing.
func chargingRepo(b entity.Booking, orderID string) func(context.Context, portout.InitiateInput, portout.ChargeFunc) (portout.InitiateOutcome, error) {
	return func(ctx context.Context, in portout.InitiateInput, charge portout.ChargeFunc) (portout.InitiateOutcome, error) {
		res, err := charge(ctx, b, orderID)
		if err != nil {
			return portout.InitiateOutcome{}, err
		}
		return portout.InitiateOutcome{
			Booking:      b,
			OrderID:      orderID,
			Instructions: res.Instructions,
			Declined:     res.Declined(),
		}, nil
	}
}

func TestInitiate_BankTransfer_OK(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	h.repo.initiateFn = chargingRepo(b, "RB-00912:1")
	h.gw.chargeFn = func(context.Context, portout.ChargeRequest) (portout.ChargeResult, error) {
		return portout.ChargeResult{
			TransactionStatus: "pending",
			Instructions:      map[string]string{"bank": "bca", "va_number": "8277081234567890"},
		}, nil
	}

	out, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodBankTransfer, Bank: entity.BankBCA})
	require.NoError(t, err)

	assert.Equal(t, "RB-00912:1", out.PaymentID)
	assert.Equal(t, entity.MethodBankTransfer, out.Method)
	assert.Equal(t, dto.StatusPending, out.Status)
	assert.Equal(t, "8277081234567890", out.Instructions["va_number"])
	assert.Equal(t, b.PaymentExpiresAt.UTC().Format(time.RFC3339), out.TTL.ExpiresAt)
	assert.Equal(t, 1, h.gw.chargeCalls)
}

func TestInitiate_GoPay_NeedsNoBank(t *testing.T) {
	h := newHarness(t)
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	out, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Equal(t, entity.MethodGoPay, out.Method)
	assert.Equal(t, entity.MethodGoPay, h.gw.lastCharge.PaymentType)
	assert.Empty(t, h.gw.lastCharge.Bank)
}

// ── the operator kill switch (migration 0014) ───────────────────────────────
//
// Checked on §5.2 and not only on §5.1, because a customer who loaded the
// payment page before an operator switched BCA off still has a BCA button on
// screen. A switch enforced only on the listing does nothing for the people
// already mid-checkout, who are precisely the ones about to be handed an
// instrument they cannot pay.

func TestInitiate_ASwitchedOffMethodIsRefusedBeforeAnythingHappens(t *testing.T) {
	h := newHarness(t)
	h.sw.off = offMethod(entity.MethodGoPay)

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodePaymentGatewayUnavailable, appErr.ErrorCode)
	// Nothing was locked, nothing was charged: the customer loses only a
	// choice of method.
	assert.Zero(t, h.gw.chargeCalls)
	assert.Empty(t, h.repo.lastInitiate.BookingID, "the repository was never called")
}

func TestInitiate_ASwitchedOffBankIsRefused(t *testing.T) {
	h := newHarness(t)
	h.sw.off = offBank(entity.MethodBankTransfer, entity.BankBCA)

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodBankTransfer, Bank: entity.BankBCA})

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPStatus)
	assert.Zero(t, h.gw.chargeCalls)
	assert.Empty(t, h.repo.lastInitiate.BookingID)
}

// Switching off one bank must not close the method. BNI still charges.
func TestInitiate_AnotherBankUnderTheSameMethodStillCharges(t *testing.T) {
	h := newHarness(t)
	h.sw.off = offBank(entity.MethodBankTransfer, entity.BankBCA)
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodBankTransfer, Bank: entity.BankBNI})
	require.NoError(t, err)
	assert.Equal(t, 1, h.gw.chargeCalls)
}

// Switching off the METHOD closes every bank under it, so a client that kept a
// bank code from an older page cannot slip past the method-level switch.
func TestInitiate_AMethodOffAlsoRefusesEachOfItsBanks(t *testing.T) {
	for _, bank := range []string{entity.BankBCA, entity.BankBNI, entity.BankBRI, entity.BankCIMB} {
		t.Run(bank, func(t *testing.T) {
			h := newHarness(t)
			h.sw.off = offMethod(entity.MethodBankTransfer)

			_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
				dto.InitiateRequest{Method: entity.MethodBankTransfer, Bank: bank})
			var appErr *apperror.AppError
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPStatus)
			assert.Zero(t, h.gw.chargeCalls)
		})
	}
}

// A switch for something else does not interfere.
func TestInitiate_AnUnrelatedSwitchDoesNotBlockTheCharge(t *testing.T) {
	h := newHarness(t)
	h.sw.off = offMethod(entity.MethodBankTransfer)
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Equal(t, 1, h.gw.chargeCalls)
}

// The switch store being down must not stop payments.
func TestInitiate_SwitchStoreUnreadable_ChargesAnyway(t *testing.T) {
	h := newHarness(t)
	h.sw.err = errors.New("pq: connection refused")
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Equal(t, 1, h.gw.chargeCalls)
}

func TestInitiate_UnsupportedMethod_IsRejectedBeforeAnyLookup(t *testing.T) {
	for _, method := range []string{"", "credit_card", "cstore", "echannel", "BANK_TRANSFER"} {
		t.Run(method, func(t *testing.T) {
			h := newHarness(t)
			_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
				dto.InitiateRequest{Method: method})
			assertAppError(t, err, 400, apperror.CodeValidationError)
			assert.Equal(t, 0, h.gw.chargeCalls)
		})
	}
}

// A distinct code from a missing method: "bca typed as bcaa" and "no method at
// all" are different fixes for the client.
func TestInitiate_UnsupportedBank_IsBankNotSupported(t *testing.T) {
	for _, bank := range []string{"", "permata", "mandiri", "indomaret", "BCA"} {
		t.Run(bank, func(t *testing.T) {
			h := newHarness(t)
			_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
				dto.InitiateRequest{Method: entity.MethodBankTransfer, Bank: bank})
			assertAppError(t, err, 400, apperror.CodeBankNotSupported)
			assert.Equal(t, 0, h.gw.chargeCalls)
		})
	}
}

// custom_expiry.order_time is held_at, never the request time. Anchoring on
// "now" would hand every method switch a fresh ten minutes and BR-05 would
// never bind.
func TestInitiate_ChargeAnchorsExpiryOnHeldAt(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	h.repo.initiateFn = chargingRepo(b, "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)

	assert.Equal(t, *b.HeldAt, h.gw.lastCharge.HeldAt)
	assert.NotEqual(t, fixedNow, h.gw.lastCharge.HeldAt)
	// Seconds: the window is sent to the gateway at full resolution.
	assert.Equal(t, 600, h.gw.lastCharge.ExpirySeconds)
}

// A booking with no held_at cannot anchor on nothing: the request time is the
// fallback, because a charge with no custom_expiry at all would default the VA
// to 24 hours at the gateway.
func TestInitiate_NoHeldAt_FallsBackToNow(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	b.HeldAt = nil
	h.repo.initiateFn = chargingRepo(b, "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Equal(t, fixedNow, h.gw.lastCharge.HeldAt)
}

// The amount is the sum of booking_items computed under the row lock, not a
// client-supplied or denormalised figure.
func TestInitiate_ChargesTheLockedTotal(t *testing.T) {
	h := newHarness(t)
	b := heldBooking(entity.StatusHeld)
	b.Total = 4500000
	h.repo.initiateFn = chargingRepo(b, "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Equal(t, int64(4500000), h.gw.lastCharge.GrossAmount)
}

// The breaker is read INSIDE the charge closure, which the repository only
// calls when a charge is actually needed. A replay of §5.2 reaches no gateway,
// so it must keep answering while the breaker is open - otherwise a
// double-clicked Pay button turns a working payment into a 503.
func TestInitiate_ReplayWorksWhileBreakerOpen(t *testing.T) {
	h := newHarness(t)
	h.brk.allowFn = func(context.Context) (bool, error) { return false, nil }
	b := heldBooking(entity.StatusAwaitingPayment)
	h.repo.initiateFn = func(context.Context, portout.InitiateInput, portout.ChargeFunc) (portout.InitiateOutcome, error) {
		// The real repository returns the stored instructions without calling
		// the closure at all.
		return portout.InitiateOutcome{
			Booking:      b,
			OrderID:      "RB-00912:1",
			Instructions: map[string]string{"bank": "bca", "va_number": "8277081234567890"},
			Replayed:     true,
		}, nil
	}

	out, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodBankTransfer, Bank: entity.BankBCA})
	require.NoError(t, err)
	assert.Equal(t, dto.StatusPending, out.Status)
	assert.Equal(t, "8277081234567890", out.Instructions["va_number"])
	assert.Equal(t, 0, h.gw.chargeCalls)
}

func TestInitiate_BreakerOpen_Is503(t *testing.T) {
	h := newHarness(t)
	h.brk.allowFn = func(context.Context) (bool, error) { return false, nil }
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	assertAppError(t, err, 503, apperror.CodePaymentGatewayUnavailable)
	assert.Equal(t, 0, h.gw.chargeCalls)
}

// The breaker exists to spare Midtrans, not to become a second thing that can
// refuse payments. An unreadable breaker lets the charge through.
func TestInitiate_BreakerUnreadable_ChargesAnyway(t *testing.T) {
	h := newHarness(t)
	h.brk.allowFn = func(context.Context) (bool, error) {
		return false, errors.New("redis: connection refused")
	}
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Equal(t, 1, h.gw.chargeCalls)
}

// A timeout is NOT a declined payment: the instrument may well exist at the
// gateway. The answer is 503 so the client does not send the customer back to
// pick another method, the breaker counts a failure, and the transaction rolls
// back with payment_ref still NULL for the reconciler to resolve.
func TestInitiate_GatewayTimeout_Is503AndCountsAFailure(t *testing.T) {
	h := newHarness(t)
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")
	h.gw.chargeFn = func(context.Context, portout.ChargeRequest) (portout.ChargeResult, error) {
		return portout.ChargeResult{}, errors.New("context deadline exceeded")
	}

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	assertAppError(t, err, 503, apperror.CodePaymentGatewayUnavailable)
	assert.Equal(t, 1, h.brk.failures)
	assert.Equal(t, 0, h.brk.successes)
}

func TestInitiate_Success_RecordsBreakerSuccess(t *testing.T) {
	h := newHarness(t)
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Equal(t, 1, h.brk.successes)
	assert.Equal(t, 0, h.brk.failures)
}

// A breaker bookkeeping failure must never fail a charge that succeeded: the
// money has moved and the customer is owed their instrument.
func TestInitiate_BreakerBookkeepingFailure_DoesNotFailTheCharge(t *testing.T) {
	h := newHarness(t)
	h.brk.successErr = errors.New("redis: write failed")
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	out, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Equal(t, dto.StatusPending, out.Status)
}

// A synchronous refusal is a 200 with status FAILED: the REQUEST succeeded,
// the payment did not. The attempt counter has been raised inside the
// transaction, so the customer picks another method and calls §5.2 again - not
// §5.3, which exists only to void a still-pending instrument.
func TestInitiate_SynchronousDecline_Is200Failed(t *testing.T) {
	for _, status := range []string{"deny", "cancel", "failure"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")
			h.gw.chargeFn = func(context.Context, portout.ChargeRequest) (portout.ChargeResult, error) {
				return portout.ChargeResult{TransactionStatus: status, StatusMessage: "refused"}, nil
			}

			out, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
				dto.InitiateRequest{Method: entity.MethodGoPay})
			require.NoError(t, err)
			assert.Equal(t, dto.StatusFailed, out.Status)
			// The breaker saw a healthy gateway: a refusal is an answer.
			assert.Equal(t, 1, h.brk.successes)
			assert.Equal(t, 0, h.brk.failures)
		})
	}
}

func TestInitiate_WrongStatus_Is409(t *testing.T) {
	h := newHarness(t)
	h.repo.initiateFn = func(context.Context, portout.InitiateInput, portout.ChargeFunc) (portout.InitiateOutcome, error) {
		return portout.InitiateOutcome{}, portout.ErrInvalidStatus
	}

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	assertAppError(t, err, 409, apperror.CodeBookingStatusInvalid)
}

func TestInitiate_UnknownBooking_Is404(t *testing.T) {
	h := newHarness(t)
	h.repo.initiateFn = func(context.Context, portout.InitiateInput, portout.ChargeFunc) (portout.InitiateOutcome, error) {
		return portout.InitiateOutcome{}, portout.ErrNotFound
	}

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodGoPay})
	assertAppError(t, err, 404, apperror.CodeNotFound)
}

// order_id is derived server-side from stored state and never taken from the
// request, which is what makes a double-clicked Pay button idempotent at
// Midtrans rather than a second instrument.
func TestInitiate_PassesStoredStateToRepository(t *testing.T) {
	h := newHarness(t)
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc",
		dto.InitiateRequest{Method: entity.MethodBankTransfer, Bank: entity.BankBNI})
	require.NoError(t, err)

	assert.Equal(t, "RB-00912", h.repo.lastInitiate.BookingID)
	assert.Equal(t, "grant-abc", h.repo.lastInitiate.GrantToken)
	assert.Equal(t, entity.MethodBankTransfer, h.repo.lastInitiate.Method)
	assert.Equal(t, entity.BankBNI, h.repo.lastInitiate.Bank)
	assert.Equal(t, fixedNow, h.repo.lastInitiate.Now)
}
