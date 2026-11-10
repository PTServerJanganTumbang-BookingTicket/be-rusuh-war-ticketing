package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/valueobject"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// signed builds a notification whose signature_key is correct for its own
// fields, so a test that wants a mismatch has to ask for one explicitly.
func signed(n dto.Notification) dto.Notification {
	n.SignatureKey = valueobject.SignatureKey(n.OrderID, n.StatusCode, n.GrossAmount, testServerKey)
	return n
}

func settlement() dto.Notification {
	return signed(dto.Notification{
		OrderID:           "RB-00912:1",
		StatusCode:        "200",
		GrossAmount:       "3000000.00",
		TransactionStatus: valueobject.TxSettlement,
		FraudStatus:       valueobject.FraudAccept,
		PaymentType:       entity.MethodBankTransfer,
		TransactionID:     "tx-1",
	})
}

func TestHandleNotification_Settlement_Confirms(t *testing.T) {
	h := newHarness(t)
	h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{
			Action:     valueobject.ActionConfirm,
			Applied:    true,
			Booking:    heldBooking(entity.StatusConfirmed),
			EventID:    "EV-1",
			QueueToken: "qt-1",
			AccountID:  "ACC-1",
		}, nil
	}

	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))

	require.Equal(t, 1, h.creds.settleCalls)
	assert.Equal(t, "EV-1", h.creds.lastSettle.EventID)
	assert.Equal(t, "RB-00912", h.creds.lastSettle.BookingID)
	assert.Equal(t, "qt-1", h.creds.lastSettle.QueueToken)
	assert.Equal(t, "ACC-1", h.creds.lastSettle.AccountID)
	assert.Equal(t, h.cfg.SettledTombstoneTTL, h.creds.lastSettle.TombstoneTTL)
}

// Nothing is read, written or even parsed on an unverified body: the webhook
// is reachable by anyone who learns the URL, and the signature is the only
// authentication there is.
func TestHandleNotification_BadSignature_TouchesNothing(t *testing.T) {
	h := newHarness(t)
	n := settlement()
	n.SignatureKey = "0000000000000000000000000000000000000000000000000000000000000000" +
		"0000000000000000000000000000000000000000000000000000000000000000"

	err := h.uc.HandleNotification(context.Background(), n)
	require.Error(t, err)
	assert.Equal(t, 0, h.repo.notifyCalls)
	assert.Equal(t, 0, h.creds.settleCalls)
}

func TestHandleNotification_EmptySignature_IsRejected(t *testing.T) {
	h := newHarness(t)
	n := settlement()
	n.SignatureKey = ""

	assert.Error(t, h.uc.HandleNotification(context.Background(), n))
	assert.Equal(t, 0, h.repo.notifyCalls)
}

// The signature is computed from gross_amount exactly as received, which is
// why the field travels as a string all the way here. A notification whose
// amount was tampered with no longer matches.
func TestHandleNotification_TamperedAmount_IsRejected(t *testing.T) {
	h := newHarness(t)
	n := settlement()
	n.GrossAmount = "1.00"

	assert.Error(t, h.uc.HandleNotification(context.Background(), n))
	assert.Equal(t, 0, h.repo.notifyCalls)
}

func TestHandleNotification_MalformedOrderID_IsRejected(t *testing.T) {
	h := newHarness(t)
	n := settlement()
	n.OrderID = "RB-00912"
	n = signed(n) // correctly signed, but still unparseable

	err := h.uc.HandleNotification(context.Background(), n)
	assert.ErrorIs(t, err, valueobject.ErrMalformedOrderID)
	assert.Equal(t, 0, h.repo.notifyCalls)
}

// Derived, because Midtrans sends no event id of any kind. Not transaction_id:
// that is stable for the lifetime of one transaction, so pending and
// settlement would share a value and the second would be swallowed.
func TestHandleNotification_GatewayEventIDIsPerEvent(t *testing.T) {
	h := newHarness(t)
	var ids []string
	h.repo.notifyFn = func(_ context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		ids = append(ids, in.GatewayEventID)
		return portout.NotificationOutcome{}, nil
	}

	pending := settlement()
	pending.TransactionStatus = valueobject.TxPending
	pending.StatusCode = "201"
	pending = signed(pending)

	require.NoError(t, h.uc.HandleNotification(context.Background(), pending))
	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))

	require.Len(t, ids, 2)
	// order_id carries the attempt, so the key is per attempt AND per event.
	assert.Equal(t, "RB-00912:1:pending:201", ids[0])
	assert.Equal(t, "RB-00912:1:settlement:200", ids[1])
	assert.NotEqual(t, ids[0], ids[1])
}

// A redelivery. The UNIQUE on gateway_event_id tripped, the transaction rolled
// back, and the endpoint still answers 200 - a 4xx here would turn one event
// into a Midtrans retry storm.
func TestHandleNotification_Duplicate_IsNotAnError(t *testing.T) {
	h := newHarness(t)
	h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{}, portout.ErrDuplicateEvent
	}

	assert.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
	assert.Equal(t, 0, h.creds.settleCalls)
}

// Logged, not retried: an order_id we never issued can never succeed, so
// asking Midtrans to redeliver it forever is pointless.
func TestHandleNotification_UnknownBooking_IsReportedNotSwallowed(t *testing.T) {
	h := newHarness(t)
	h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{}, portout.ErrNotFound
	}

	assert.ErrorIs(t, h.uc.HandleNotification(context.Background(), settlement()), portout.ErrNotFound)
}

func TestHandleNotification_RepositoryFailure_IsReturnedForLogging(t *testing.T) {
	h := newHarness(t)
	boom := errors.New("postgres: connection reset")
	h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{}, boom
	}

	assert.ErrorIs(t, h.uc.HandleNotification(context.Background(), settlement()), boom)
	assert.Equal(t, 0, h.creds.settleCalls)
}

// Credential cleanup runs only for an APPLIED confirm. A recorded-only event
// must not release the account's BR-07 hold or delete the queue token.
func TestHandleNotification_NonConfirmActions_LeaveCredentialsAlone(t *testing.T) {
	cases := []struct {
		name    string
		action  valueobject.Action
		applied bool
	}{
		{"record only", valueobject.ActionRecordOnly, false},
		{"decline", valueobject.ActionDecline, true},
		{"expire", valueobject.ActionExpire, true},
		{"unfunded capture", valueobject.ActionIgnoreUnfunded, false},
		{"confirm not applied", valueobject.ActionConfirm, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
				return portout.NotificationOutcome{Action: c.action, Applied: c.applied}, nil
			}

			require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
			assert.Equal(t, 0, h.creds.settleCalls)
		})
	}
}

// Money arrived after the seats were resold. The transition is the
// repository's business; what this layer owes is an operator alarm, because
// there is deliberately no automated refund path.
func TestHandleNotification_LateSettlement_StillSucceedsAndSkipsCleanup(t *testing.T) {
	h := newHarness(t)
	h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{
			Action:  valueobject.ActionLateSettlement,
			Applied: true,
			Booking: heldBooking(entity.StatusExpired),
		}, nil
	}

	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
	assert.Equal(t, 0, h.creds.settleCalls)
}

// A Redis failure after the commit must never fail the request: the booking is
// confirmed and the tickets are issued. The worst case is a queue credential
// that outlives its engagement until its own TTL.
func TestHandleNotification_CleanupFailure_DoesNotFailTheWebhook(t *testing.T) {
	h := newHarness(t)
	h.creds.settleFn = func(context.Context, portout.SettleInput) error {
		return errors.New("redis: connection refused")
	}
	h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{
			Action: valueobject.ActionConfirm, Applied: true,
			Booking: heldBooking(entity.StatusConfirmed), EventID: "EV-1", QueueToken: "qt-1",
		}, nil
	}

	assert.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
}

// The raw body is stored for reconciliation, and the parsed amount is minor
// units for the column. The signature already used the original string, so
// dropping the fractional part here cannot break verification.
func TestHandleNotification_StoresRawAndParsedAmount(t *testing.T) {
	h := newHarness(t)
	h.repo.notifyFn = func(_ context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		assert.Equal(t, int64(3000000), in.GrossAmount)
		assert.Equal(t, "RB-00912", in.BookingID)
		assert.Equal(t, "RB-00912:1", in.OrderID)
		assert.Equal(t, valueobject.TxSettlement, in.TransactionStatus)
		assert.Equal(t, valueobject.FraudAccept, in.FraudStatus)
		assert.Equal(t, entity.MethodBankTransfer, in.PaymentType)
		assert.Equal(t, fixedNow, in.Now)
		assert.Equal(t, 5, in.MaxAttempts)

		var round dto.Notification
		require.NoError(t, json.Unmarshal(in.Raw, &round))
		// The stored body must keep gross_amount byte for byte, or a later
		// re-verification of the same event would fail.
		assert.Equal(t, "3000000.00", round.GrossAmount)
		return portout.NotificationOutcome{}, nil
	}

	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
	assert.Equal(t, 1, h.repo.notifyCalls)
}

// IDR has no circulating sub-unit and Midtrans always sends ".00", so the
// fractional part is dropped rather than rounded: rounding would invent a
// rupiah nobody was charged.
func TestParseGrossAmount(t *testing.T) {
	cases := map[string]int64{
		"3000000.00":   3000000,
		"3000000":      3000000,
		"3000000.99":   3000000,
		" 150000.00":   150000,
		"0.00":         0,
		"":             0,
		"not-a-number": 0,
		".50":          0,
	}
	for in, want := range cases {
		assert.Equal(t, want, parseGrossAmount(in), in)
	}
}
