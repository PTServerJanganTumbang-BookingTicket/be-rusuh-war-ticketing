package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/valueobject"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// notified makes the repository answer every notification with out.
func notified(h *harness, out portout.NotificationOutcome) {
	h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
		return out, nil
	}
}

func TestAnAppliedSettlementCountsBookingsConfirmedOnceForItsEvent(t *testing.T) {
	h := newHarness(t)
	notified(h, portout.NotificationOutcome{Action: valueobject.ActionConfirm, Applied: true, EventID: "EV-1",
		Booking: heldBooking(entity.StatusConfirmed)})

	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
	assert.Equal(t, map[string]int{MetricBookingsConfirmed + "/EV-1": 1}, h.met.counts)
}

func TestARepeatedSettlementNotificationCountsNothing(t *testing.T) {
	h := newHarness(t)
	h.repo.notifyFn = func(context.Context, portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{}, portout.ErrDuplicateEvent // the same gateway event, redelivered
	}

	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
	assert.Empty(t, h.met.counts)
}

func TestASettlementThatChangedNothingCountsNothing(t *testing.T) {
	// A second, distinct settlement for a booking already CONFIRMED: recorded, not applied.
	h := newHarness(t)
	notified(h, portout.NotificationOutcome{Action: valueobject.ActionRecordOnly, EventID: "EV-1"})

	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
	assert.Empty(t, h.met.counts)
}

func TestAnAppliedExpireCountsBookingsPaymentExpired(t *testing.T) {
	h := newHarness(t)
	notified(h, portout.NotificationOutcome{Action: valueobject.ActionExpire, Applied: true, EventID: "EV-1",
		Booking: heldBooking(entity.StatusExpired)})

	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
	assert.Equal(t, map[string]int{MetricBookingsPaymentExpired + "/EV-1": 1}, h.met.counts)
}

func TestALateSettlementCountsLateSettlementAndLogsItsEventAndAmount(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := newHarness(t)
	notified(h, portout.NotificationOutcome{Action: valueobject.ActionLateSettlement, Applied: true, EventID: "EV-1",
		Booking: heldBooking(entity.StatusExpired)})

	require.NoError(t, h.uc.HandleNotification(context.Background(), settlement()))
	assert.Equal(t, map[string]int{MetricLateSettlement + "/EV-1": 1}, h.met.counts)

	var line map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var l map[string]any
		if json.Unmarshal(raw, &l) == nil && l["alarm"] == "LatePaymentRefund" {
			line = l
		}
	}
	require.NotNil(t, line, "no LatePaymentRefund log line in %s", logs.String())
	// The FR-36 disputed list is read from this line (docs/metrics.md §7).
	assert.Equal(t, "EV-1", line["event_id"])
	assert.Equal(t, "RB-00912", line["booking_id"])
	assert.Equal(t, "RB-00912:1", line["order_id"])
	assert.Equal(t, "3000000.00", line["gross_amount"])
}

func TestAFailedChargeCountsAMidtransError(t *testing.T) {
	h := newHarness(t)
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")
	h.gw.chargeFn = func(context.Context, portout.ChargeRequest) (portout.ChargeResult, error) {
		return portout.ChargeResult{}, errors.New("context deadline exceeded")
	}

	_, _ = h.uc.Initiate(context.Background(), "RB-00912", "grant-abc", dto.InitiateRequest{Method: entity.MethodGoPay})
	assert.Equal(t, 1, h.met.counts[MetricMidtransErrors])
}

func TestAFailedCancelCountsAMidtransError(t *testing.T) {
	h := newHarness(t)
	h.gw.cancelFn = func(context.Context, string) error { return errors.New("context deadline exceeded") }
	h.repo.retryFn = func(ctx context.Context, _ portout.RetryInput, cancel portout.CancelFunc) (portout.RetryOutcome, error) {
		if err := cancel(ctx, "RB-00912:1"); err != nil {
			return portout.RetryOutcome{}, portout.ErrGatewayUnavailable
		}
		return portout.RetryOutcome{}, nil
	}

	_, _ = h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assert.Equal(t, 1, h.met.counts[MetricMidtransErrors])
}

func TestASuccessfulChargeCountsNoMidtransError(t *testing.T) {
	h := newHarness(t)
	h.repo.initiateFn = chargingRepo(heldBooking(entity.StatusHeld), "RB-00912:1")

	_, err := h.uc.Initiate(context.Background(), "RB-00912", "grant-abc", dto.InitiateRequest{Method: entity.MethodGoPay})
	require.NoError(t, err)
	assert.Zero(t, h.met.counts[MetricMidtransErrors])
}

func TestACancelRefusedBecauseThePaymentIsAlreadyPaidIsNotAMidtransError(t *testing.T) {
	h := newHarness(t)
	h.gw.cancelFn = func(context.Context, string) error { return portout.ErrAlreadySettled }
	h.repo.retryFn = func(ctx context.Context, _ portout.RetryInput, cancel portout.CancelFunc) (portout.RetryOutcome, error) {
		return portout.RetryOutcome{}, cancel(ctx, "RB-00912:1")
	}

	_, _ = h.uc.Retry(context.Background(), "RB-00912", "grant-abc")
	assert.Zero(t, h.met.counts[MetricMidtransErrors])
}
