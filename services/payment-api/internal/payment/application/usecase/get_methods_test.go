package usecase

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

func TestGetMethods_OK(t *testing.T) {
	h := newHarness(t)
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return heldBooking(entity.StatusHeld), nil
	}

	out, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	require.Len(t, out.Methods, 2)
	assert.Equal(t, entity.MethodBankTransfer, out.Methods[0].ID)
	assert.Len(t, out.Methods[0].Banks, 4)
	assert.Equal(t, entity.MethodGoPay, out.Methods[1].ID)
	assert.Empty(t, out.Methods[1].Banks)
	assert.Equal(t, "https://cdn.example/icons/bank.svg", out.Methods[0].IconURL)
}

// The catalogue is static, so the ownership lookup is the only thing standing
// between an enumerated booking id and a 200. Without it the endpoint would
// confirm which ids exist.
func TestGetMethods_UnknownOrUnownedBooking_Is404(t *testing.T) {
	h := newHarness(t)
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return entity.Booking{}, portout.ErrNotFound
	}

	_, err := h.uc.GetMethods(context.Background(), "RB-00001", "grant-abc")
	require.Error(t, err)

	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusNotFound, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeNotFound, appErr.ErrorCode)
}

func TestGetMethods_PassesGrantToRepository(t *testing.T) {
	h := newHarness(t)
	var gotID, gotGrant string
	h.repo.getOwnedFn = func(_ context.Context, id, grant string) (entity.Booking, error) {
		gotID, gotGrant = id, grant
		return heldBooking(entity.StatusHeld), nil
	}

	_, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	assert.Equal(t, "RB-00912", gotID)
	assert.Equal(t, "grant-abc", gotGrant)
}

// An open breaker greys the choice out in the UI rather than letting the
// customer discover the outage after pressing Pay.
func TestGetMethods_BreakerOpen_MethodsDisabled(t *testing.T) {
	h := newHarness(t)
	h.brk.allowFn = func(context.Context) (bool, error) { return false, nil }

	out, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	for _, m := range out.Methods {
		assert.False(t, m.Enabled, m.ID)
	}
}

// A Redis blip must not hide every payment method. Guessing "enabled" costs at
// worst a 503 at charge time, which §5.2 already answers properly; guessing
// "disabled" would make the sale look closed.
func TestGetMethods_BreakerUnreadable_FailsOpen(t *testing.T) {
	h := newHarness(t)
	h.brk.allowFn = func(context.Context) (bool, error) {
		return false, errors.New("redis: connection refused")
	}

	out, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	for _, m := range out.Methods {
		assert.True(t, m.Enabled, m.ID)
	}
}

// A repository failure that is not ErrNotFound must stay a 500, not be
// flattened into a 404: "the database is down" and "no such booking" need
// different responses from an operator's point of view.
// ── the operator kill switch (migration 0014) ───────────────────────────────

func TestGetMethods_ASwitchedOffBankIsNotOffered(t *testing.T) {
	h := newHarness(t)
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return heldBooking(entity.StatusHeld), nil
	}
	h.sw.off = offBank(entity.MethodBankTransfer, entity.BankBCA)

	out, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	require.Len(t, out.Methods, 2)
	assert.True(t, out.Methods[0].Enabled, "the method still works through the other three banks")
	require.Len(t, out.Methods[0].Banks, 3)
	for _, b := range out.Methods[0].Banks {
		assert.NotEqual(t, entity.BankBCA, b.Code)
	}
}

func TestGetMethods_ASwitchedOffMethodIsOfferedAsDisabled(t *testing.T) {
	h := newHarness(t)
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return heldBooking(entity.StatusHeld), nil
	}
	h.sw.off = offMethod(entity.MethodGoPay)

	out, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	require.Len(t, out.Methods, 2, "still listed, so the customer is told rather than left guessing")
	assert.Equal(t, entity.MethodGoPay, out.Methods[1].ID)
	assert.False(t, out.Methods[1].Enabled)
	assert.True(t, out.Methods[0].Enabled)
}

// The switch subtracts; it never adds. A row for a method the code cannot
// charge must not put that method in front of a customer, because §5.2 would
// then refuse it — or worse, mint an instrument nothing can shape.
func TestGetMethods_ASwitchForAnUnknownMethodIsInert(t *testing.T) {
	h := newHarness(t)
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return heldBooking(entity.StatusHeld), nil
	}
	h.sw.off = offMethod("credit_card")

	out, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	require.Len(t, out.Methods, 2)
	assert.True(t, out.Methods[0].Enabled)
	assert.True(t, out.Methods[1].Enabled)
	for _, m := range out.Methods {
		assert.NotEqual(t, "credit_card", m.ID)
	}
}

// A kill switch whose own storage is unreachable must not be able to take
// payments down. Guessing "nothing is off" is the only safe direction: the
// worst case is that a switched-off method is offered during a database
// outage, in which case §5.2 cannot charge anything anyway.
func TestGetMethods_SwitchStoreUnreadable_OffersEveryMethod(t *testing.T) {
	h := newHarness(t)
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return heldBooking(entity.StatusHeld), nil
	}
	h.sw.err = errors.New("pq: connection refused")

	out, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)
	require.Len(t, out.Methods, 2)
	assert.True(t, out.Methods[0].Enabled)
	assert.Len(t, out.Methods[0].Banks, 4)
}

// Ownership is still resolved first. A switch must not turn this endpoint into
// an oracle that answers before the booking is checked.
func TestGetMethods_UnownedBookingIs404EvenWithSwitchesSet(t *testing.T) {
	h := newHarness(t)
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return entity.Booking{}, portout.ErrNotFound
	}
	h.sw.off = offMethod(entity.MethodGoPay)

	_, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusNotFound, appErr.HTTPStatus)
	assert.Zero(t, h.sw.calls, "the switch store is not even consulted")
}

func TestGetMethods_RepositoryError_IsNotMappedTo404(t *testing.T) {
	h := newHarness(t)
	boom := errors.New("postgres: connection reset")
	h.repo.getOwnedFn = func(context.Context, string, string) (entity.Booking, error) {
		return entity.Booking{}, boom
	}

	_, err := h.uc.GetMethods(context.Background(), "RB-00912", "grant-abc")
	assert.ErrorIs(t, err, boom)
}
