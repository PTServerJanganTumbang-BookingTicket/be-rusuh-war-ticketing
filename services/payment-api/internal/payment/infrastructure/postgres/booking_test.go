package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// ── GetOwned ──────────────────────────────────────────────────────────────────

func TestGetOwned_OK(t *testing.T) {
	db := &mockDB{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowValues(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1"))...)
	}}

	b, err := newRepo(db).GetOwned(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)

	assert.Equal(t, "RB-00912", b.ID)
	assert.Equal(t, "EV-1", b.EventID)
	assert.Equal(t, entity.StatusAwaitingPayment, b.Status)
	assert.Equal(t, "RB-00912:1", b.PaymentRef)
	assert.Equal(t, "grant-abc", b.GrantToken)
}

// Ownership is in the WHERE clause, not in a comparison after the read.
// Filtering afterwards would still have fetched - and potentially logged - a
// row belonging to someone else.
func TestGetOwned_FiltersByGrantInSQL(t *testing.T) {
	db := &mockDB{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowValues(bookingValues(entity.StatusHeld)...)
	}}

	_, err := newRepo(db).GetOwned(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)

	require.Len(t, db.queries, 1)
	assert.Contains(t, db.queries[0], "WHERE id = $1 AND grant_token = $2")
	assert.Equal(t, []any{"RB-00912", "grant-abc"}, db.args[0])
}

// Booking ids are enumerable, and a valid grant proves the bearer holds a
// slot, not that the slot is this booking's. 403 would confirm the row exists,
// so both cases answer 404.
func TestGetOwned_NoMatch_Is404NotForbidden(t *testing.T) {
	db := &mockDB{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}

	_, err := newRepo(db).GetOwned(context.Background(), "RB-00912", "someone-elses-grant")
	assert.ErrorIs(t, err, portout.ErrNotFound)
}

func TestGetOwned_DriverError_IsInternal(t *testing.T) {
	db := &mockDB{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowErr(errors.New("connection reset"))
	}}

	_, err := newRepo(db).GetOwned(context.Background(), "RB-00912", "grant-abc")
	require.Error(t, err)
	assert.NotErrorIs(t, err, portout.ErrNotFound)
	assert.NotContains(t, err.Error(), "connection reset")
}

// ── Initiate ──────────────────────────────────────────────────────────────────

// initiateTx builds a transaction that answers the three reads Initiate makes
// in order: the locked booking, the item total, then whatever the path needs.
func initiateTx(booking []any, total int64) *mockTx {
	tx := &mockTx{}
	tx.queryRowFn = func(_ context.Context, sql string, _ ...any) pgx.Row {
		switch {
		case strings.Contains(sql, "FOR UPDATE"):
			return rowValues(booking...)
		case strings.Contains(sql, "SUM(unit_price)"):
			return rowValues(total)
		default:
			return rowErr(pgx.ErrNoRows)
		}
	}
	return tx
}

func TestInitiate_HeldBooking_ChargesAndCommits(t *testing.T) {
	tx := initiateTx(bookingValues(entity.StatusHeld), 3000000)
	db := &mockDB{tx: tx}

	var gotOrderID string
	var gotTotal int64
	out, err := newRepo(db).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(_ context.Context, b entity.Booking, orderID string) (portout.ChargeResult, error) {
			gotOrderID, gotTotal = orderID, b.Total
			return portout.ChargeResult{
				TransactionStatus: "pending", StatusCode: "201",
				Instructions: map[string]string{"qr_url": "https://q"},
			}, nil
		})
	require.NoError(t, err)

	// order_id is derived from stored state, never from the client.
	assert.Equal(t, "RB-00912:0", gotOrderID)
	// The amount comes from booking_items, summed under the lock.
	assert.Equal(t, int64(3000000), gotTotal)

	assert.Equal(t, entity.StatusAwaitingPayment, out.Booking.Status)
	assert.Equal(t, "RB-00912:0", out.OrderID)
	assert.Equal(t, "https://q", out.Instructions["qr_url"])
	assert.False(t, out.Declined)
	assert.False(t, out.Replayed)
	assert.Equal(t, 1, tx.commits)
}

// The row is locked FOR UPDATE and the lock SPANS the charge: two concurrent
// Pay clicks have to serialise somewhere, and the booking row is the only
// thing both of them touch.
func TestInitiate_LocksTheRowAndChargesInsideTheLock(t *testing.T) {
	tx := initiateTx(bookingValues(entity.StatusHeld), 3000000)
	db := &mockDB{tx: tx}

	var writesBeforeCharge int
	_, err := newRepo(db).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			writesBeforeCharge = len(tx.execs)
			return portout.ChargeResult{TransactionStatus: "pending", StatusCode: "201"}, nil
		})
	require.NoError(t, err)

	assert.Contains(t, tx.queries[0], "FOR UPDATE")
	// Nothing is written before the gateway answers, so a failed charge rolls
	// back a transaction that changed nothing.
	assert.Equal(t, 0, writesBeforeCharge)
	assert.NotEmpty(t, tx.execs)
}

// held_at and payment_expires_at are deliberately NOT written: both were set
// at HELD. Re-writing held_at would re-anchor custom_expiry and hand a fresh
// ten minutes to every charge, so BR-05 would never bind.
func TestInitiate_DoesNotTouchTheDeadline(t *testing.T) {
	tx := initiateTx(bookingValues(entity.StatusHeld), 3000000)
	db := &mockDB{tx: tx}

	_, err := newRepo(db).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			return portout.ChargeResult{TransactionStatus: "pending", StatusCode: "201"}, nil
		})
	require.NoError(t, err)

	for _, c := range tx.execs {
		assert.NotContains(t, c.sql, "held_at")
		assert.NotContains(t, c.sql, "payment_expires_at")
	}
}

// A replay: AWAITING_PAYMENT means the charge already happened. No gateway
// call, no writes, and the SAME instructions - a double-clicked Pay button is
// not a failure.
func TestInitiate_Replay_ReturnsStoredInstructionsWithoutCharging(t *testing.T) {
	stored, err := json.Marshal(storedCharge{Instructions: map[string]string{
		"bank": "bca", "va_number": "8277081234567890",
	}})
	require.NoError(t, err)

	tx := &mockTx{}
	tx.queryRowFn = func(_ context.Context, sql string, _ ...any) pgx.Row {
		switch {
		case strings.Contains(sql, "FOR UPDATE"):
			return rowValues(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1"))...)
		case strings.Contains(sql, "FROM payment_events"):
			return rowValues(stored)
		}
		return rowErr(pgx.ErrNoRows)
	}
	db := &mockDB{tx: tx}

	charged := false
	out, err := newRepo(db).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodBankTransfer, Bank: entity.BankBCA, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			charged = true
			return portout.ChargeResult{}, nil
		})
	require.NoError(t, err)

	assert.False(t, charged)
	assert.True(t, out.Replayed)
	assert.Equal(t, "RB-00912:1", out.OrderID)
	assert.Equal(t, "8277081234567890", out.Instructions["va_number"])
	assert.Empty(t, tx.execs, "a replay must write nothing")
	assert.Equal(t, 1, tx.commits)
}

// AWAITING_PAYMENT with no stored charge means something other than this
// service wrote the row. Empty instructions beat a 500: the client still has
// the payment_id and can poll §5.4.
func TestInitiate_Replay_NoStoredCharge_StillSucceeds(t *testing.T) {
	tx := &mockTx{}
	tx.queryRowFn = func(_ context.Context, sql string, _ ...any) pgx.Row {
		if strings.Contains(sql, "FOR UPDATE") {
			return rowValues(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1"))...)
		}
		return rowErr(pgx.ErrNoRows)
	}
	db := &mockDB{tx: tx}

	out, err := newRepo(db).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			t.Fatal("must not charge on a replay")
			return portout.ChargeResult{}, nil
		})
	require.NoError(t, err)
	assert.True(t, out.Replayed)
	assert.Empty(t, out.Instructions)
}

// A stored charge whose payload cannot be decoded is treated the same way:
// the customer gets their payment_id and polls, rather than a 500.
func TestInitiate_Replay_CorruptStoredCharge_StillSucceeds(t *testing.T) {
	tx := &mockTx{}
	tx.queryRowFn = func(_ context.Context, sql string, _ ...any) pgx.Row {
		switch {
		case strings.Contains(sql, "FOR UPDATE"):
			return rowValues(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1"))...)
		case strings.Contains(sql, "FROM payment_events"):
			return rowValues([]byte("not json"))
		}
		return rowErr(pgx.ErrNoRows)
	}

	out, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			return portout.ChargeResult{}, nil
		})
	require.NoError(t, err)
	assert.NotNil(t, out.Instructions)
	assert.Empty(t, out.Instructions)
}

// Every other status is a 409 reached with NO gateway contact: charging a
// CONFIRMED or EXPIRED booking would take money for seats that are settled or
// resold.
func TestInitiate_WrongStatus_Is409WithoutCharging(t *testing.T) {
	for _, st := range []entity.BookingStatus{
		entity.StatusConfirmed, entity.StatusExpired, entity.StatusLapsed,
		entity.StatusAbandoned, entity.StatusGranted, entity.StatusSelecting,
		entity.StatusPendingCancellation,
	} {
		t.Run(string(st), func(t *testing.T) {
			tx := initiateTx(bookingValues(st), 3000000)
			charged := false

			_, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
				portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
				func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
					charged = true
					return portout.ChargeResult{}, nil
				})

			assert.ErrorIs(t, err, portout.ErrInvalidStatus)
			assert.False(t, charged)
			assert.Equal(t, 0, tx.commits)
			assert.Equal(t, 1, tx.rollbacks)
		})
	}
}

func TestInitiate_UnknownOrUnowned_Is404(t *testing.T) {
	tx := &mockTx{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}

	_, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "nope", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			return portout.ChargeResult{}, nil
		})
	assert.ErrorIs(t, err, portout.ErrNotFound)
}

// A booking with no seats cannot be charged. Reported as a status problem
// rather than an internal error: it means the hold never completed.
func TestInitiate_NoSeats_Is409(t *testing.T) {
	for _, total := range []int64{0, -1} {
		tx := initiateTx(bookingValues(entity.StatusHeld), total)
		_, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
			portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
			func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
				t.Fatal("must not charge a booking with no seats")
				return portout.ChargeResult{}, nil
			})
		assert.ErrorIs(t, err, portout.ErrInvalidStatus)
	}
}

// The attempt counter comes from the row, so a booking that has already been
// declined twice derives a fresh order_id rather than reusing one Midtrans
// would refuse.
func TestInitiate_OrderIDUsesStoredAttemptCount(t *testing.T) {
	tx := initiateTx(bookingValues(entity.StatusHeld, withAttempts(3)), 3000000)

	var gotOrderID string
	_, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(_ context.Context, _ entity.Booking, orderID string) (portout.ChargeResult, error) {
			gotOrderID = orderID
			return portout.ChargeResult{TransactionStatus: "pending", StatusCode: "201"}, nil
		})
	require.NoError(t, err)
	assert.Equal(t, "RB-00912:3", gotOrderID)
}

// A timeout rolls back with payment_ref still NULL. The instrument may exist
// at the gateway, so the truth is left to the reconciler rather than guessed
// at here.
func TestInitiate_ChargeFails_RollsBackWithNothingWritten(t *testing.T) {
	tx := initiateTx(bookingValues(entity.StatusHeld), 3000000)
	boom := errors.New("context deadline exceeded")

	_, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			return portout.ChargeResult{}, boom
		})

	assert.ErrorIs(t, err, boom)
	assert.Empty(t, tx.execs)
	assert.Equal(t, 0, tx.commits)
	assert.Equal(t, 1, tx.rollbacks)
}

// A synchronous refusal COMMITS: payment_attempts has to move, or the next
// charge reuses an order_id Midtrans already refused and the customer cannot
// retry at all.
func TestInitiate_Declined_RaisesAttemptsAndCommits(t *testing.T) {
	tx := initiateTx(bookingValues(entity.StatusHeld), 3000000)

	out, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			return portout.ChargeResult{TransactionStatus: "deny", StatusCode: "402", StatusMessage: "refused"}, nil
		})
	require.NoError(t, err)

	assert.True(t, out.Declined)
	assert.Equal(t, "refused", out.DeclineReason)
	// Back to HELD, not EXPIRED: the payment failed, the claim did not.
	assert.Equal(t, entity.StatusHeld, out.Booking.Status)
	assert.Equal(t, 1, out.Booking.PaymentAttempts)

	updates := tx.execMatching("UPDATE bookings", "payment_attempts = payment_attempts + 1")
	require.Len(t, updates, 1)
	assert.Contains(t, updates[0].sql, "payment_ref = NULL")
	assert.Contains(t, updates[0].sql, "status = 'HELD'")
	assert.Equal(t, 1, tx.commits)
}

// The charge response is stored so a replay can return byte-identical
// instructions without calling the gateway again.
func TestInitiate_StoresTheChargeResponse(t *testing.T) {
	tx := initiateTx(bookingValues(entity.StatusHeld), 3000000)

	_, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodBankTransfer, Bank: entity.BankBCA, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			return portout.ChargeResult{
				TransactionStatus: "pending", StatusCode: "201",
				Instructions: map[string]string{"bank": "bca", "va_number": "8277081234567890"},
				Raw:          []byte(`{"status_code":"201"}`),
			}, nil
		})
	require.NoError(t, err)

	inserts := tx.execMatching("INSERT INTO payment_events")
	require.Len(t, inserts, 1)
	// Keyed so a redelivered notification cannot collide with it, and
	// idempotent in its own right.
	assert.Contains(t, inserts[0].sql, "ON CONFLICT (gateway_event_id) DO NOTHING")
	assert.Contains(t, inserts[0].args, "RB-00912:0:charge:201")

	var stored storedCharge
	raw, ok := inserts[0].args[len(inserts[0].args)-1].([]byte)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal(raw, &stored))
	assert.Equal(t, "8277081234567890", stored.Instructions["va_number"])
	assert.JSONEq(t, `{"status_code":"201"}`, string(stored.Gateway))
}

func TestInitiate_BeginFails_IsInternal(t *testing.T) {
	db := &mockDB{beginErr: errors.New("too many connections")}

	_, err := newRepo(db).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			t.Fatal("must not charge without a transaction")
			return portout.ChargeResult{}, nil
		})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "too many connections")
}

// A commit failure after a successful charge is an internal error, not a
// decline: the money may have moved and the reconciler has to resolve it.
func TestInitiate_CommitFails_IsInternal(t *testing.T) {
	tx := initiateTx(bookingValues(entity.StatusHeld), 3000000)
	tx.commitErr = errors.New("could not serialize access")

	_, err := newRepo(&mockDB{tx: tx}).Initiate(context.Background(),
		portout.InitiateInput{BookingID: "RB-00912", GrantToken: "grant-abc", Method: entity.MethodGoPay, Now: fixedNow},
		func(context.Context, entity.Booking, string) (portout.ChargeResult, error) {
			return portout.ChargeResult{TransactionStatus: "pending", StatusCode: "201"}, nil
		})
	require.Error(t, err)
	assert.NotErrorIs(t, err, portout.ErrInvalidStatus)
	assert.NotContains(t, err.Error(), "serialize")
}

// ── Retry ─────────────────────────────────────────────────────────────────────

func retryTx(booking []any) *mockTx {
	return &mockTx{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowValues(booking...)
	}}
}

func TestRetry_CancelsThenCommits(t *testing.T) {
	tx := retryTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1"), withAttempts(1)))

	var cancelledWhenWrites int
	var gotOrderID string
	out, err := newRepo(&mockDB{tx: tx}).Retry(context.Background(),
		portout.RetryInput{BookingID: "RB-00912", GrantToken: "grant-abc", Now: fixedNow, MinRemaining: time.Minute, MaxAttempts: 5},
		func(_ context.Context, orderID string) error {
			gotOrderID = orderID
			cancelledWhenWrites = len(tx.execs)
			return nil
		})
	require.NoError(t, err)

	assert.Equal(t, "RB-00912:1", gotOrderID)
	assert.Equal(t, "RB-00912:1", out.CancelledOrderID)
	assert.Equal(t, 2, out.Attempt)
	assert.Equal(t, entity.StatusHeld, out.Booking.Status)
	assert.Empty(t, out.Booking.PaymentRef)
	// The cancel happens BEFORE the commit. Reversed, a failed cancel would
	// leave a raised attempt counter next to a still-payable VA.
	assert.Equal(t, 0, cancelledWhenWrites)
	assert.Equal(t, 1, tx.commits)
}

// Only a live, pending payment can be cancelled. HELD after a decline is
// excluded on purpose: there is nothing at the gateway to void.
func TestRetry_OnlyFromAwaitingPayment(t *testing.T) {
	for _, st := range []entity.BookingStatus{
		entity.StatusHeld, entity.StatusConfirmed, entity.StatusExpired,
		entity.StatusLapsed, entity.StatusPendingCancellation, entity.StatusGranted,
	} {
		t.Run(string(st), func(t *testing.T) {
			tx := retryTx(bookingValues(st, withPaymentRef("RB-00912:1")))
			cancelled := false

			_, err := newRepo(&mockDB{tx: tx}).Retry(context.Background(),
				portout.RetryInput{BookingID: "RB-00912", GrantToken: "grant-abc", Now: fixedNow, MinRemaining: time.Minute, MaxAttempts: 5},
				func(context.Context, string) error { cancelled = true; return nil })

			assert.ErrorIs(t, err, portout.ErrInvalidStatus)
			assert.False(t, cancelled)
			assert.Equal(t, 0, tx.commits)
		})
	}
}

// Checked BEFORE the gateway is called: an attempt that cannot be granted must
// not void a working instrument on the way to being refused.
func TestRetry_MaxAttempts_RefusesBeforeCancelling(t *testing.T) {
	tx := retryTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:5"), withAttempts(5)))
	cancelled := false

	_, err := newRepo(&mockDB{tx: tx}).Retry(context.Background(),
		portout.RetryInput{BookingID: "RB-00912", GrantToken: "grant-abc", Now: fixedNow, MinRemaining: time.Minute, MaxAttempts: 5},
		func(context.Context, string) error { cancelled = true; return nil })

	assert.ErrorIs(t, err, portout.ErrMaxAttempts)
	assert.False(t, cancelled)
	assert.Equal(t, 0, tx.commits)
}

// Below the floor the switch is refused rather than served: a new virtual
// account with seconds on it only moves the failure somewhere more painful.
// The window is never extended, so the remainder only ever shrinks.
func TestRetry_TooLittleTimeLeft_RefusesBeforeCancelling(t *testing.T) {
	cases := map[string]time.Time{
		"30 seconds left": fixedNow.Add(30 * time.Second),
		"already expired": fixedNow.Add(-1 * time.Second),
	}
	for name, exp := range cases {
		t.Run(name, func(t *testing.T) {
			tx := retryTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1"), withExpiresAt(exp)))
			cancelled := false

			_, err := newRepo(&mockDB{tx: tx}).Retry(context.Background(),
				portout.RetryInput{BookingID: "RB-00912", GrantToken: "grant-abc", Now: fixedNow, MinRemaining: time.Minute, MaxAttempts: 5},
				func(context.Context, string) error { cancelled = true; return nil })

			assert.ErrorIs(t, err, portout.ErrWindowClosed)
			assert.False(t, cancelled)
		})
	}
}

// Midtrans refusing to cancel something already paid is a real outcome, not a
// transport failure: nothing changes and the webhook confirms the booking.
func TestRetry_AlreadySettled_ChangesNothing(t *testing.T) {
	tx := retryTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	_, err := newRepo(&mockDB{tx: tx}).Retry(context.Background(),
		portout.RetryInput{BookingID: "RB-00912", GrantToken: "grant-abc", Now: fixedNow, MinRemaining: time.Minute, MaxAttempts: 5},
		func(context.Context, string) error { return portout.ErrAlreadySettled })

	assert.ErrorIs(t, err, portout.ErrAlreadySettled)
	assert.Empty(t, tx.execs)
	assert.Equal(t, 0, tx.commits)
	assert.Equal(t, 1, tx.rollbacks)
}

// The cancel failed, so the old instrument is still valid. payment_attempts is
// deliberately NOT raised: the customer may simply pay the VA they already
// have.
func TestRetry_CancelFails_RaisesNothing(t *testing.T) {
	tx := retryTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	_, err := newRepo(&mockDB{tx: tx}).Retry(context.Background(),
		portout.RetryInput{BookingID: "RB-00912", GrantToken: "grant-abc", Now: fixedNow, MinRemaining: time.Minute, MaxAttempts: 5},
		func(context.Context, string) error { return errors.New("context deadline exceeded") })

	assert.ErrorIs(t, err, portout.ErrGatewayUnavailable)
	assert.Empty(t, tx.execMatching("payment_attempts"))
	assert.Equal(t, 0, tx.commits)
}

func TestRetry_UnknownOrUnowned_Is404(t *testing.T) {
	tx := &mockTx{queryRowFn: func(context.Context, string, ...any) pgx.Row { return rowErr(pgx.ErrNoRows) }}

	_, err := newRepo(&mockDB{tx: tx}).Retry(context.Background(),
		portout.RetryInput{BookingID: "RB-00912", GrantToken: "nope", Now: fixedNow, MinRemaining: time.Minute, MaxAttempts: 5},
		func(context.Context, string) error { return nil })
	assert.ErrorIs(t, err, portout.ErrNotFound)
}

// The UPDATE is guarded on the status it read, so a booking that moved between
// the lock and the write cannot be dragged backwards.
func TestRetry_UpdateIsGuardedOnStatus(t *testing.T) {
	tx := retryTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	_, err := newRepo(&mockDB{tx: tx}).Retry(context.Background(),
		portout.RetryInput{BookingID: "RB-00912", GrantToken: "grant-abc", Now: fixedNow, MinRemaining: time.Minute, MaxAttempts: 5},
		func(context.Context, string) error { return nil })
	require.NoError(t, err)

	updates := tx.execMatching("UPDATE bookings")
	require.Len(t, updates, 1)
	assert.Contains(t, updates[0].sql, "AND status = 'AWAITING_PAYMENT'")
}

// ── ApplyNotification ─────────────────────────────────────────────────────────

func notification(status, fraud, orderID string) portout.NotificationInput {
	return portout.NotificationInput{
		BookingID:         "RB-00912",
		GatewayEventID:    orderID + ":" + status + ":200",
		OrderID:           orderID,
		TransactionStatus: status,
		FraudStatus:       fraud,
		PaymentType:       entity.MethodBankTransfer,
		GrossAmount:       3000000,
		Raw:               []byte(`{"order_id":"` + orderID + `"}`),
		Now:               fixedNow,
		MaxAttempts:       5,
	}
}

// notifyTx answers the booking lock plus the item reads the confirm path
// makes.
func notifyTx(booking []any, items ...[]any) *mockTx {
	tx := &mockTx{}
	tx.queryRowFn = func(_ context.Context, sql string, _ ...any) pgx.Row {
		switch {
		case strings.Contains(sql, "FROM bookings"):
			return rowValues(booking...)
		case strings.Contains(sql, "INSERT INTO tickets"):
			return rowValues("ticket-row")
		case strings.Contains(sql, "FROM events"):
			return rowValues("Konser", "2026-04-01", "GBK")
		}
		return rowErr(pgx.ErrNoRows)
	}
	tx.queryFn = func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
		if strings.Contains(sql, "FROM booking_items") {
			return rowsOf(items...), nil
		}
		return &mockRows{}, nil
	}
	return tx
}

// One ticket per booking_item, one e-ticket email per holder, all inside the
// transaction that set the status - there is no instant at which a booking is
// CONFIRMED and its email is not queued.
// paid_at is written in the same statement as the CONFIRMED transition, from
// the gateway's own settlement time (migration 0013). §5.4 reports it, and
// before the column existed it reported the payment DEADLINE instead.
func TestApplyNotification_Confirm_StoresTheGatewaySettlementTime(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))
	settled := time.Date(2026, 3, 1, 10, 8, 30, 0, time.UTC)

	_, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(), portout.NotificationInput{
		BookingID: "RB-00912", OrderID: "RB-00912:1",
		GatewayEventID:    "RB-00912:1:settlement:200",
		TransactionStatus: "settlement", FraudStatus: "accept",
		PaidAt: &settled, Now: fixedNow, MaxAttempts: 5,
	})
	require.NoError(t, err)

	confirms := tx.execMatching("status = 'CONFIRMED'")
	require.Len(t, confirms, 1)
	assert.Contains(t, confirms[0].sql, "paid_at = COALESCE($2, now())",
		"one statement, so a confirmed booking never lacks a payment time")
	require.Len(t, confirms[0].args, 2)
	assert.Equal(t, &settled, confirms[0].args[1])
}

// A notification with no usable timestamp still has to leave a CONFIRMED row
// with a paid_at, because §5.4 must answer with something and the alternative
// is faking it from the deadline again. COALESCE in SQL rather than a Go
// fallback, so the value comes from the same clock as the transition.
func TestApplyNotification_Confirm_WithoutAGatewayTimeFallsBackInSQL(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	_, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(), portout.NotificationInput{
		BookingID: "RB-00912", OrderID: "RB-00912:1",
		GatewayEventID:    "RB-00912:1:settlement:200",
		TransactionStatus: "settlement", FraudStatus: "accept",
		PaidAt: nil, Now: fixedNow, MaxAttempts: 5,
	})
	require.NoError(t, err)

	confirms := tx.execMatching("status = 'CONFIRMED'")
	require.Len(t, confirms, 1)
	assert.Contains(t, confirms[0].sql, "COALESCE($2, now())")
	assert.Nil(t, confirms[0].args[1], "and the database supplies now()")
}

func TestApplyNotification_Settlement_ConfirmsIssuesAndEnqueues(t *testing.T) {
	items := [][]any{
		{"bi-1", "EV-1", "cat-1", "Festival", int64(1500000), "hmac-1", "3171****33", "a@example"},
		{"bi-2", "EV-1", "cat-1", "Festival", int64(1500000), "hmac-2", "3171****44", "b@example"},
	}
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")), items...)

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("settlement", "accept", "RB-00912:1"))
	require.NoError(t, err)

	assert.True(t, out.Applied)
	assert.Equal(t, entity.StatusConfirmed, out.Booking.Status)
	assert.Equal(t, "EV-1", out.EventID)
	assert.Equal(t, "qt-1", out.QueueToken)
	assert.Equal(t, "ACC-1", out.AccountID)

	require.Len(t, tx.execMatching("UPDATE bookings", "status = 'CONFIRMED'"), 1)
	assert.Len(t, tx.execMatching("INSERT INTO outbox"), 2, "one e-ticket email per holder")
	assert.Equal(t, 1, tx.commits)
}

// The idempotency row goes in FIRST, so a redelivery aborts before it can
// touch a status.
func TestApplyNotification_RecordsTheEventBeforeAnythingElse(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	_, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("pending", "", "RB-00912:1"))
	require.NoError(t, err)

	require.NotEmpty(t, tx.execs)
	assert.Contains(t, tx.execs[0].sql, "INSERT INTO payment_events")
	assert.Contains(t, tx.execs[0].args, "RB-00912:1:pending:200")
}

// The UNIQUE on gateway_event_id is the guard, and the rollback is the whole
// point: a redelivered settlement must not issue a second set of tickets.
func TestApplyNotification_Duplicate_RollsBack(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))
	tx.execFn = func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
		if strings.Contains(sql, "INSERT INTO payment_events") {
			return pgconn.CommandTag{}, &pgconn.PgError{Code: "23505"}
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}

	_, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("settlement", "accept", "RB-00912:1"))

	assert.ErrorIs(t, err, portout.ErrDuplicateEvent)
	assert.Empty(t, tx.execMatching("INSERT INTO tickets"))
	assert.Equal(t, 0, tx.commits)
	assert.Equal(t, 1, tx.rollbacks)
}

// A foreign-key failure means the booking does not exist: an order_id we never
// issued. Reported as not-found so the webhook logs it and still answers 200.
func TestApplyNotification_ForeignKeyFailure_Is404(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment))
	tx.execFn = func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
		if strings.Contains(sql, "INSERT INTO payment_events") {
			return pgconn.CommandTag{}, &pgconn.PgError{Code: "23503"}
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}

	_, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("settlement", "accept", "RB-00912:1"))
	assert.ErrorIs(t, err, portout.ErrNotFound)
}

// A notification naming a superseded attempt is recorded and otherwise
// ignored: applying it would move a booking on the strength of an instrument
// that is no longer live.
func TestApplyNotification_StaleAttempt_IsRecordedOnly(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:2")))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("settlement", "accept", "RB-00912:1"))
	require.NoError(t, err)

	assert.False(t, out.Applied)
	assert.Empty(t, tx.execMatching("status = 'CONFIRMED'"))
	assert.Equal(t, 1, tx.commits)
}

// The late-settlement path is exempt from that gate: by definition no
// instrument is live any more, so there is nothing to compare against - and
// real money arrived.
func TestApplyNotification_LateSettlement_MarksDisputedWithoutTransitioning(t *testing.T) {
	for _, st := range []entity.BookingStatus{entity.StatusExpired, entity.StatusLapsed} {
		t.Run(string(st), func(t *testing.T) {
			tx := notifyTx(bookingValues(st, withPaymentRef("")))

			out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
				notification("settlement", "accept", "RB-00912:1"))
			require.NoError(t, err)

			assert.True(t, out.Applied)
			// No status change: the seats are gone.
			assert.Empty(t, tx.execMatching("status = 'CONFIRMED'"))
			require.Len(t, tx.execMatching("payment_disputed = true"), 1)
			// A notice routes the customer to support, with the payment
			// reference attached.
			require.Len(t, tx.execMatching("INSERT INTO outbox"), 1)
			assert.Equal(t, 1, tx.commits)
		})
	}
}

// Back to HELD, not EXPIRED: the payment failed, the claim did not, so the
// seats stay with the customer until the TTL.
func TestApplyNotification_Decline_ReturnsToHeldAndRaisesAttempts(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1"), withAttempts(1)))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("deny", "", "RB-00912:1"))
	require.NoError(t, err)

	assert.True(t, out.Applied)
	assert.Equal(t, entity.StatusHeld, out.Booking.Status)
	assert.Equal(t, 2, out.Booking.PaymentAttempts)
	updates := tx.execMatching("UPDATE bookings", "status = 'HELD'")
	require.Len(t, updates, 1)
	assert.Contains(t, updates[0].sql, "payment_attempts = payment_attempts + 1")
	// Attempts remain, so there is nothing to tell the customer yet.
	assert.Empty(t, tx.execMatching("INSERT INTO outbox"))
}

// Out of attempts: tell the customer, rather than letting them discover it by
// pressing Pay again.
func TestApplyNotification_DeclineAtLastAttempt_NotifiesTheCustomer(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:4"), withAttempts(4)))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("deny", "", "RB-00912:4"))
	require.NoError(t, err)

	assert.True(t, out.Applied)
	require.Len(t, tx.execMatching("INSERT INTO outbox"), 1)
}

// Expiry frees the NIKs. Without that the NIK stays locked for the event even
// though its booking is dead, and BR-02 would block the customer from ever
// buying again.
func TestApplyNotification_Expire_ReleasesNIKClaims(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("expire", "", "RB-00912:1"))
	require.NoError(t, err)

	assert.True(t, out.Applied)
	assert.Equal(t, entity.StatusExpired, out.Booking.Status)
	require.Len(t, tx.execMatching("DELETE FROM nik_claims"), 1)
	require.Len(t, tx.execMatching("INSERT INTO outbox"), 1)
}

// Raced to a terminal state between the lock and the write. Recorded, not
// forced: a zero-row UPDATE means someone else already decided.
func TestApplyNotification_UpdateAffectsNoRows_DowngradesToRecordOnly(t *testing.T) {
	cases := map[string]string{
		"settlement": "settlement",
		"decline":    "deny",
		"expire":     "expire",
	}
	for name, status := range cases {
		t.Run(name, func(t *testing.T) {
			tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))
			tx.execFn = func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
				if strings.Contains(sql, "UPDATE bookings") {
					return pgconn.NewCommandTag("UPDATE 0"), nil
				}
				return pgconn.NewCommandTag("INSERT 0 1"), nil
			}

			out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
				notification(status, "accept", "RB-00912:1"))
			require.NoError(t, err)

			assert.False(t, out.Applied)
			assert.Empty(t, tx.execMatching("INSERT INTO tickets"))
			assert.Empty(t, tx.execMatching("INSERT INTO outbox"))
			assert.Equal(t, 1, tx.commits)
		})
	}
}

// capture without fraud_status=accept is authorised but NOT funded: issuing
// tickets would hand out seats for money never received.
func TestApplyNotification_UnfundedCapture_ChangesNothing(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("capture", "challenge", "RB-00912:1"))
	require.NoError(t, err)

	assert.False(t, out.Applied)
	assert.Empty(t, tx.execMatching("UPDATE bookings"))
	assert.Empty(t, tx.execMatching("INSERT INTO tickets"))
	// Still recorded, so reconciliation can see it.
	require.Len(t, tx.execMatching("INSERT INTO payment_events"), 1)
	assert.Equal(t, 1, tx.commits)
}

// pending is the normal state of an unpaid VA: recorded, nothing else.
func TestApplyNotification_Pending_IsRecordedOnly(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("pending", "", "RB-00912:1"))
	require.NoError(t, err)

	assert.False(t, out.Applied)
	assert.Empty(t, tx.execMatching("UPDATE bookings"))
	assert.Equal(t, 1, tx.commits)
}

// A callback landing during the 30-second grace is the entire purpose of
// having a grace, so PENDING_CANCELLATION is accepted alongside
// AWAITING_PAYMENT.
func TestApplyNotification_ConfirmAcceptsTheGracePeriod(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusPendingCancellation, withPaymentRef("RB-00912:1")))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("settlement", "accept", "RB-00912:1"))
	require.NoError(t, err)

	assert.True(t, out.Applied)
	updates := tx.execMatching("status = 'CONFIRMED'")
	require.Len(t, updates, 1)
	assert.Contains(t, updates[0].sql, "'AWAITING_PAYMENT', 'PENDING_CANCELLATION'")
}

func TestApplyNotification_BookingVanished_Is404(t *testing.T) {
	tx := &mockTx{queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}

	_, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("settlement", "accept", "RB-00912:1"))
	assert.ErrorIs(t, err, portout.ErrNotFound)
}

// The booking is locked by ID, not by payment_ref: payment_ref is cleared on a
// decline, so a later notification for that attempt would match nothing.
func TestApplyNotification_LocksByID(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	_, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("pending", "", "RB-00912:1"))
	require.NoError(t, err)

	var locked string
	for _, q := range tx.queries {
		if strings.Contains(q, "FROM bookings") {
			locked = q
		}
	}
	require.NotEmpty(t, locked)
	assert.Contains(t, locked, "WHERE id = $1 FOR UPDATE")
	assert.NotContains(t, locked, "payment_ref =")
}

// A guest booking has no account_id, so there is no BR-07 hold to release.
func TestApplyNotification_GuestBooking_ReportsNoAccount(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1"), withAccountID("")))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("settlement", "accept", "RB-00912:1"))
	require.NoError(t, err)

	assert.True(t, out.Applied)
	assert.Empty(t, out.AccountID)
	assert.Equal(t, "qt-1", out.QueueToken)
}

// ---- payment detail columns (0017, 0018) ----

// The read path is where these columns earn their keep: §5.4 answers from
// GetOwned alone, so a column that scans wrong is a wrong receipt.
func TestGetOwned_ReadsThePaymentDetailColumns(t *testing.T) {
	db := &mockDB{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowValues(bookingValues(entity.StatusAwaitingPayment,
			withPaymentRef("RB-00912:1"),
			withPaymentAmount(3000000),
			withPaymentStatus("pending"),
			withInstructions(`{"bank":"bca","va_number":"8801234567890"}`),
		)...)
	}}
	b, err := newRepo(db).GetOwned(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)

	assert.Equal(t, "RB-00912:1", b.PaymentRef)
	require.NotNil(t, b.PaymentAmount)
	assert.Equal(t, int64(3000000), *b.PaymentAmount)
	assert.Equal(t, "pending", b.PaymentStatus)
	assert.Equal(t, "bca", b.PaymentInstructions["bank"])
	assert.Equal(t, "8801234567890", b.PaymentInstructions["va_number"])
}

// NULL columns on a booking that was never charged must read as absent, not as
// a zero amount or an empty instruction set.
func TestGetOwned_NeverCharged_LeavesThePaymentDetailUnset(t *testing.T) {
	db := &mockDB{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowValues(bookingValues(entity.StatusHeld)...)
	}}
	b, err := newRepo(db).GetOwned(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err)

	assert.Nil(t, b.PaymentAmount)
	assert.Empty(t, b.PaymentStatus)
	assert.Nil(t, b.PaymentInstructions)
}

// Malformed JSON in the column must cost the instructions, not the booking: a
// customer with no VA number is in a bad way, one with no booking page is worse.
func TestGetOwned_UnreadableInstructions_StillReturnsTheBooking(t *testing.T) {
	db := &mockDB{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowValues(bookingValues(entity.StatusAwaitingPayment,
			withInstructions(`{"bank": NOT JSON`),
		)...)
	}}
	b, err := newRepo(db).GetOwned(context.Background(), "RB-00912", "grant-abc")
	require.NoError(t, err, "a bad instructions blob must not fail the read")
	assert.Equal(t, "RB-00912", b.ID)
	assert.Nil(t, b.PaymentInstructions)
}

// ---- payment_status is recorded only for a notification we acted on (0017) ----

// A decline IS applied, so the gateway's word is recorded. This is the case
// the column exists for: a booking back in HELD, retryable, showing exactly
// why the last attempt failed without anyone opening payment_events.
func TestApplyNotification_AppliedAction_RecordsTheGatewayStatus(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("deny", "", "RB-00912:1"))
	require.NoError(t, err)

	require.True(t, out.Applied)
	assert.Equal(t, "deny", out.Booking.PaymentStatus)
	require.Len(t, tx.execMatching("SET payment_status"), 1)
}

// The inverse, pinned deliberately rather than left to the two ChangesNothing
// tests above: an unfunded capture must NOT record 'capture'. It is authorised
// and not funded, and a payment_status of 'capture' reads as money received —
// the exact misreading ActionIgnoreUnfunded exists to prevent.
func TestApplyNotification_UnfundedCapture_DoesNotRecordCapture(t *testing.T) {
	tx := notifyTx(bookingValues(entity.StatusAwaitingPayment, withPaymentRef("RB-00912:1")))

	out, err := newRepo(&mockDB{tx: tx}).ApplyNotification(context.Background(),
		notification("capture", "challenge", "RB-00912:1"))
	require.NoError(t, err)

	assert.False(t, out.Applied)
	assert.Empty(t, out.Booking.PaymentStatus,
		"an unfunded capture must not leave the booking looking captured")
	assert.Empty(t, tx.execMatching("SET payment_status"))
	// The event itself is still in the log, where the full sequence belongs.
	require.Len(t, tx.execMatching("INSERT INTO payment_events"), 1)
}
