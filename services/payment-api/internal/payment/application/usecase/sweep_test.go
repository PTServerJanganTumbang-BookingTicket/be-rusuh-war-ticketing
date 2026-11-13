package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/valueobject"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// ── sweep-specific test doubles ────────────────────────────────────────────────

type fakeSweepRepo struct {
	fakeRepo
	dueFn func(ctx context.Context, now time.Time, graceSeconds, limit int) ([]portout.AwaitingCandidate, error)
}

func (r *fakeSweepRepo) DueAwaitingPayment(ctx context.Context, now time.Time, graceSeconds, limit int) ([]portout.AwaitingCandidate, error) {
	if r.dueFn != nil {
		return r.dueFn(ctx, now, graceSeconds, limit)
	}
	return nil, nil
}

type fakeHolds struct {
	releaseFn    func(ctx context.Context, eventID string, seats []portout.SeatCount, niks []string) (int, error)
	releaseCalls int
	lastEventID  string
	lastSeats    []portout.SeatCount
	lastNIKs     []string
}

func (h *fakeHolds) Release(ctx context.Context, eventID string, seats []portout.SeatCount, niks []string) (int, error) {
	h.releaseCalls++
	h.lastEventID = eventID
	h.lastSeats = seats
	h.lastNIKs = niks
	if h.releaseFn != nil {
		return h.releaseFn(ctx, eventID, seats, niks)
	}
	return 0, nil
}

type sweepHarness struct {
	uc    *SweepUsecase
	repo  *fakeSweepRepo
	gw    *fakeSweepGateway
	brk   *fakeBreaker
	creds *fakeCredentials
	holds *fakeHolds
	cfg   SweepConfig
}

type fakeSweepGateway struct {
	statusFn    func(ctx context.Context, orderID string) (portout.StatusResult, error)
	expireFn    func(ctx context.Context, orderID string) error
	statusCalls int
	expireCalls int
	lastOrderID string
}

func (g *fakeSweepGateway) Charge(ctx context.Context, req portout.ChargeRequest) (portout.ChargeResult, error) {
	return portout.ChargeResult{}, nil
}

func (g *fakeSweepGateway) Cancel(ctx context.Context, orderID string) error { return nil }

func (g *fakeSweepGateway) Expire(ctx context.Context, orderID string) error {
	g.expireCalls++
	g.lastOrderID = orderID
	if g.expireFn != nil {
		return g.expireFn(ctx, orderID)
	}
	return nil
}

func (g *fakeSweepGateway) Status(ctx context.Context, orderID string) (portout.StatusResult, error) {
	g.statusCalls++
	g.lastOrderID = orderID
	if g.statusFn != nil {
		return g.statusFn(ctx, orderID)
	}
	return portout.StatusResult{
		TransactionStatus: "expire",
		FraudStatus:       "accept",
		StatusCode:        "407",
	}, nil
}

func newSweepHarness(t *testing.T) *sweepHarness {
	t.Helper()
	h := &sweepHarness{
		repo:  &fakeSweepRepo{},
		gw:    &fakeSweepGateway{},
		brk:   &fakeBreaker{},
		creds: &fakeCredentials{},
		holds: &fakeHolds{},
		cfg: SweepConfig{
			GraceSeconds:        120,
			BatchSize:           100,
			StuckMinutes:        30,
			MaxPaymentAttempts:  5,
			SettledTombstoneTTL: time.Hour,
		},
	}
	h.uc = NewSweep(h.repo, h.gw, h.brk, h.creds, h.holds, h.cfg)
	h.uc.now = func() time.Time { return fixedNow }
	return h
}

func dueCandidate(id, paymentRef string) portout.AwaitingCandidate {
	return portout.AwaitingCandidate{
		ID:               id,
		PaymentRef:       paymentRef,
		PaymentExpiresAt: fixedNow.Add(-5 * time.Minute),
	}
}

// ── sweep tests ────────────────────────────────────────────────────────────────

func TestSweep_PendingAtGateway_ExpiresAndReleasesOnce(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.gw.statusFn = func(ctx context.Context, orderID string) (portout.StatusResult, error) {
		return portout.StatusResult{
			TransactionStatus: "expire",
			FraudStatus:       "accept",
			StatusCode:        "407",
		}, nil
	}
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{
			Action:  valueobject.ActionExpire,
			Applied: true,
			EventID: "EV-1",
			Seats:   []portout.SeatCount{{CategoryID: "CAT-1", Qty: 2}},
			NIKs:    []string{"nik-hmac-1"},
		}, nil
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 1, result.Expired)
	assert.Equal(t, 0, result.Settled)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Duplicate)
	assert.Equal(t, 1, h.gw.expireCalls)
	assert.Equal(t, 1, h.holds.releaseCalls)
	assert.Equal(t, "EV-1", h.holds.lastEventID)
}

func TestSweep_SettlementAtGateway_ConfirmsAndDoesNotRelease(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.gw.statusFn = func(ctx context.Context, orderID string) (portout.StatusResult, error) {
		return portout.StatusResult{
			TransactionStatus: "settlement",
			FraudStatus:       "accept",
			StatusCode:        "200",
			SettlementTime:    "2026-03-01 16:55:00",
		}, nil
	}
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{
			Action:     valueobject.ActionConfirm,
			Applied:    true,
			EventID:    "EV-1",
			Booking:    heldBooking("CONFIRMED"),
			QueueToken: "qt-1",
			AccountID:  "ACC-1",
		}, nil
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, result.Expired)
	assert.Equal(t, 1, result.Settled)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Duplicate)
	assert.Equal(t, 0, h.gw.expireCalls, "settlement should not call Expire")
	assert.Equal(t, 0, h.holds.releaseCalls, "settlement should not release holds")
	assert.Equal(t, 1, h.creds.settleCalls)
}

// The oversell guard: if Applied is false, Release must not be called.
// Calling Release twice double-credits inventory.
func TestSweep_AlreadyExpiredByWebhook_DoesNotReleaseTwice(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		// The webhook already handled this, so Applied is false.
		return portout.NotificationOutcome{
			Action:  valueobject.ActionExpire,
			Applied: false,
		}, nil
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, result.Expired, "not applied means not counted as expired by this sweep")
	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 0, h.holds.releaseCalls, "must NOT release when Applied is false")
}

func TestSweep_GatewayUnreachable_SkipsAndLeavesBookingAlone(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.gw.statusFn = func(ctx context.Context, orderID string) (portout.StatusResult, error) {
		return portout.StatusResult{}, errors.New("connection refused")
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, result.Expired)
	assert.Equal(t, 0, result.Settled)
	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 0, h.repo.notifyCalls, "no notification should be applied when gateway is unreachable")
	assert.Equal(t, 1, h.brk.failures, "failure should be recorded")
}

func TestSweep_BreakerOpen_DoesNotCallTheGateway(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.brk.allowFn = func(ctx context.Context) (bool, error) {
		return false, nil
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 0, h.gw.statusCalls, "breaker open should prevent gateway calls")
}

func TestSweep_EmptyPaymentRef_IsSkippedWithoutAGatewayCall(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{
			{ID: "RB-001", PaymentRef: "", PaymentExpiresAt: fixedNow.Add(-5 * time.Minute)},
		}, nil
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 0, h.gw.statusCalls, "empty payment_ref should skip without gateway call")
}

func TestSweep_UnfundedCapture_NeitherExpiresNorConfirms(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.gw.statusFn = func(ctx context.Context, orderID string) (portout.StatusResult, error) {
		return portout.StatusResult{
			TransactionStatus: "capture",
			FraudStatus:       "accept",
			StatusCode:        "200",
		}, nil
	}
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{
			Action:  valueobject.ActionIgnoreUnfunded,
			Applied: false,
		}, nil
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, result.Expired)
	assert.Equal(t, 0, result.Settled)
	assert.Equal(t, 1, result.Skipped)
}

func TestSweep_Duplicate_CountsAsDuplicateNotSkipped(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{}, portout.ErrDuplicateEvent
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, result.Expired)
	assert.Equal(t, 0, result.Settled)
	assert.Equal(t, 0, result.Skipped, "duplicate should NOT be counted as skipped")
	assert.Equal(t, 1, result.Duplicate, "duplicate should be counted in its own bucket")
}

// F2: The sweep must use the gateway's settlement time, not its own clock.
func TestSweep_SettledPaidAtComesFromTheGatewayNotTheClock(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	// Gateway returns a settlement time well before "now"
	gatewaySettlement := "2026-03-01 16:00:00" // WIB = 09:00 UTC
	h.gw.statusFn = func(ctx context.Context, orderID string) (portout.StatusResult, error) {
		return portout.StatusResult{
			TransactionStatus: "settlement",
			FraudStatus:       "accept",
			StatusCode:        "200",
			SettlementTime:    gatewaySettlement,
			TransactionTime:   "2026-03-01 15:55:00",
		}, nil
	}

	var capturedPaidAt *time.Time
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		capturedPaidAt = in.PaidAt
		return portout.NotificationOutcome{
			Action:  valueobject.ActionConfirm,
			Applied: true,
		}, nil
	}

	_, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	require.NotNil(t, capturedPaidAt, "PaidAt must not be nil for a settlement")
	// The gateway time "2026-03-01 16:00:00" WIB = "2026-03-01 09:00:00" UTC
	expected := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, *capturedPaidAt, "PaidAt should be the gateway's settlement time, not now")
	assert.NotEqual(t, fixedNow, *capturedPaidAt, "PaidAt should differ from the sweep's clock")
}

// F1: The sweep must use the configured TombstoneTTL, not zero.
func TestSweep_SettleUsesTheConfiguredTombstoneTTL(t *testing.T) {
	h := newSweepHarness(t)
	h.cfg.SettledTombstoneTTL = 45 * time.Minute
	h.uc = NewSweep(h.repo, h.gw, h.brk, h.creds, h.holds, h.cfg)
	h.uc.now = func() time.Time { return fixedNow }

	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.gw.statusFn = func(ctx context.Context, orderID string) (portout.StatusResult, error) {
		return portout.StatusResult{
			TransactionStatus: "settlement",
			FraudStatus:       "accept",
			StatusCode:        "200",
		}, nil
	}
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{
			Action:     valueobject.ActionConfirm,
			Applied:    true,
			EventID:    "EV-1",
			Booking:    heldBooking("CONFIRMED"),
			QueueToken: "qt-1",
		}, nil
	}

	_, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, h.creds.settleCalls)
	assert.Equal(t, 45*time.Minute, h.creds.lastSettle.TombstoneTTL, "must use configured TTL, not zero")
	assert.NotZero(t, h.creds.lastSettle.TombstoneTTL, "TombstoneTTL must not be zero")
}

func TestSweep_BatchSizeBoundsTheGatewayCalls(t *testing.T) {
	h := newSweepHarness(t)
	h.cfg.BatchSize = 2

	candidates := []portout.AwaitingCandidate{
		dueCandidate("RB-001", "order-1"),
		dueCandidate("RB-002", "order-2"),
		dueCandidate("RB-003", "order-3"),
	}
	h.repo.dueFn = func(ctx context.Context, now time.Time, graceSeconds, limit int) ([]portout.AwaitingCandidate, error) {
		assert.Equal(t, 2, limit, "batch size should be passed to the query")
		if len(candidates) > limit {
			return candidates[:limit], nil
		}
		return candidates, nil
	}
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		return portout.NotificationOutcome{Action: valueobject.ActionExpire, Applied: true, EventID: "EV-1"}, nil
	}

	h.uc = NewSweep(h.repo, h.gw, h.brk, h.creds, h.holds, h.cfg)
	h.uc.now = func() time.Time { return fixedNow }

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 2, h.gw.statusCalls, "should process at most batch size candidates")
	assert.Equal(t, 2, result.Expired)
}

// Counters must always sum to candidate count.
func TestSweep_CountersSumToCandidateCount(t *testing.T) {
	h := newSweepHarness(t)
	candidates := []portout.AwaitingCandidate{
		dueCandidate("RB-001", "order-1"),                                                // will be expired
		dueCandidate("RB-002", "order-2"),                                                // will be settled
		{ID: "RB-003", PaymentRef: "", PaymentExpiresAt: fixedNow.Add(-5 * time.Minute)}, // skipped: no ref
		dueCandidate("RB-004", "order-4"),                                                // will be duplicate
		dueCandidate("RB-005", "order-5"),                                                // gateway error: skipped
	}
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return candidates, nil
	}

	callCount := 0
	h.gw.statusFn = func(ctx context.Context, orderID string) (portout.StatusResult, error) {
		callCount++
		if orderID == "order-5" {
			return portout.StatusResult{}, errors.New("timeout")
		}
		if orderID == "order-2" {
			return portout.StatusResult{TransactionStatus: "settlement", StatusCode: "200"}, nil
		}
		return portout.StatusResult{TransactionStatus: "expire", StatusCode: "407"}, nil
	}

	notifyCount := 0
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		notifyCount++
		if in.OrderID == "order-4" {
			return portout.NotificationOutcome{}, portout.ErrDuplicateEvent
		}
		if in.TransactionStatus == "settlement" {
			return portout.NotificationOutcome{Action: valueobject.ActionConfirm, Applied: true, EventID: "EV-1", Booking: heldBooking("CONFIRMED")}, nil
		}
		return portout.NotificationOutcome{Action: valueobject.ActionExpire, Applied: true, EventID: "EV-1"}, nil
	}

	result, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	total := result.Expired + result.Settled + result.Skipped + result.Duplicate
	assert.Equal(t, len(candidates), total, "counters must sum to candidate count")
	assert.Equal(t, 1, result.Expired)
	assert.Equal(t, 1, result.Settled)
	assert.Equal(t, 2, result.Skipped) // no ref + gateway error
	assert.Equal(t, 1, result.Duplicate)
}

// F3: Holds release should be called unconditionally on an applied expire.
func TestSweep_ExpireCallsReleaseUnconditionally(t *testing.T) {
	h := newSweepHarness(t)
	h.repo.dueFn = func(context.Context, time.Time, int, int) ([]portout.AwaitingCandidate, error) {
		return []portout.AwaitingCandidate{dueCandidate("RB-001", "order-1")}, nil
	}
	h.repo.notifyFn = func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
		// No seats but has NIKs - the edge case F3 handles
		return portout.NotificationOutcome{
			Action:  valueobject.ActionExpire,
			Applied: true,
			EventID: "EV-1",
			Seats:   nil,
			NIKs:    []string{"nik-hmac-1"},
		}, nil
	}

	_, err := h.uc.SweepAwaitingPayment(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 1, h.holds.releaseCalls, "Release should be called even with no seats")
}
