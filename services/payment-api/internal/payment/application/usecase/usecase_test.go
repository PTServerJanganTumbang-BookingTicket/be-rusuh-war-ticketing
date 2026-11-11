package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/inbound"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// ── test doubles ──────────────────────────────────────────────────────────────
//
// Hand written rather than generated: these doubles have to RUN the charge and
// cancel closures the repository is given, because where the breaker is read
// and when the gateway is called relative to the row lock is most of what
// these tests are checking.

type fakeRepo struct {
	getOwnedFn func(ctx context.Context, bookingID, grantToken string) (entity.Booking, error)
	initiateFn func(ctx context.Context, in portout.InitiateInput, charge portout.ChargeFunc) (portout.InitiateOutcome, error)
	retryFn    func(ctx context.Context, in portout.RetryInput, cancel portout.CancelFunc) (portout.RetryOutcome, error)
	notifyFn   func(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error)

	lastInitiate portout.InitiateInput
	lastRetry    portout.RetryInput
	lastNotify   portout.NotificationInput
	notifyCalls  int
}

func (r *fakeRepo) GetOwned(ctx context.Context, bookingID, grantToken string) (entity.Booking, error) {
	if r.getOwnedFn != nil {
		return r.getOwnedFn(ctx, bookingID, grantToken)
	}
	return entity.Booking{ID: bookingID}, nil
}

func (r *fakeRepo) Initiate(ctx context.Context, in portout.InitiateInput, charge portout.ChargeFunc) (portout.InitiateOutcome, error) {
	r.lastInitiate = in
	if r.initiateFn != nil {
		return r.initiateFn(ctx, in, charge)
	}
	return portout.InitiateOutcome{}, nil
}

func (r *fakeRepo) Retry(ctx context.Context, in portout.RetryInput, cancel portout.CancelFunc) (portout.RetryOutcome, error) {
	r.lastRetry = in
	if r.retryFn != nil {
		return r.retryFn(ctx, in, cancel)
	}
	return portout.RetryOutcome{}, nil
}

func (r *fakeRepo) ApplyNotification(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
	r.notifyCalls++
	r.lastNotify = in
	if r.notifyFn != nil {
		return r.notifyFn(ctx, in)
	}
	return portout.NotificationOutcome{}, nil
}

type fakeGateway struct {
	chargeFn func(ctx context.Context, req portout.ChargeRequest) (portout.ChargeResult, error)
	cancelFn func(ctx context.Context, orderID string) error

	lastCharge  portout.ChargeRequest
	chargeCalls int
	cancelCalls int
}

func (g *fakeGateway) Charge(ctx context.Context, req portout.ChargeRequest) (portout.ChargeResult, error) {
	g.chargeCalls++
	g.lastCharge = req
	if g.chargeFn != nil {
		return g.chargeFn(ctx, req)
	}
	return portout.ChargeResult{TransactionStatus: "pending"}, nil
}

func (g *fakeGateway) Cancel(ctx context.Context, orderID string) error {
	g.cancelCalls++
	if g.cancelFn != nil {
		return g.cancelFn(ctx, orderID)
	}
	return nil
}

func (g *fakeGateway) Expire(ctx context.Context, orderID string) error { return nil }

func (g *fakeGateway) Status(ctx context.Context, orderID string) (portout.StatusResult, error) {
	return portout.StatusResult{}, nil
}

// fakeSwitches stands in for the operator kill switch. The zero value has
// nothing switched off, which is the normal state and keeps every existing
// test reading as it did before the switch existed.
type fakeSwitches struct {
	off   entity.Disabled
	err   error
	calls int
}

func (s *fakeSwitches) Disabled(ctx context.Context) (entity.Disabled, error) {
	s.calls++
	return s.off, s.err
}

// offMethod and offBank build the set the adapter would return.
func offMethod(id string) entity.Disabled {
	return entity.Disabled{entity.MethodSwitch{MethodID: id}: {}}
}

func offBank(methodID, bank string) entity.Disabled {
	return entity.Disabled{entity.MethodSwitch{MethodID: methodID, BankCode: bank}: {}}
}

type fakeBreaker struct {
	allowFn    func(ctx context.Context) (bool, error)
	successes  int
	failures   int
	successErr error
	failureErr error
}

func (b *fakeBreaker) Allow(ctx context.Context) (bool, error) {
	if b.allowFn != nil {
		return b.allowFn(ctx)
	}
	return true, nil
}

func (b *fakeBreaker) RecordSuccess(ctx context.Context) error {
	b.successes++
	return b.successErr
}

func (b *fakeBreaker) RecordFailure(ctx context.Context) error {
	b.failures++
	return b.failureErr
}

type fakeCredentials struct {
	settleFn    func(ctx context.Context, in portout.SettleInput) error
	lastSettle  portout.SettleInput
	settleCalls int
}

func (c *fakeCredentials) SettleEngagement(ctx context.Context, in portout.SettleInput) error {
	c.settleCalls++
	c.lastSettle = in
	if c.settleFn != nil {
		return c.settleFn(ctx, in)
	}
	return nil
}

// ── fixtures ──────────────────────────────────────────────────────────────────

// fixedNow is the instant every test clock is pinned to, so TTL arithmetic is
// checked rather than slept through.
var fixedNow = time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)

const testServerKey = "SB-Mid-server-test"

func testConfig() Config {
	return Config{
		IconBase:            "https://cdn.example/icons",
		PaymentTTL:          10 * time.Minute,
		MinRetryRemaining:   60 * time.Second,
		MaxPaymentAttempts:  5,
		SettledTombstoneTTL: time.Hour,
		ServerKey:           testServerKey,
	}
}

type harness struct {
	uc    portin.IPaymentUsecase
	repo  *fakeRepo
	gw    *fakeGateway
	brk   *fakeBreaker
	creds *fakeCredentials
	sw    *fakeSwitches
	cfg   Config
}

func newHarness(t *testing.T, mutate ...func(*harness)) *harness {
	t.Helper()
	h := &harness{
		repo:  &fakeRepo{},
		gw:    &fakeGateway{},
		brk:   &fakeBreaker{},
		creds: &fakeCredentials{},
		sw:    &fakeSwitches{},
		cfg:   testConfig(),
	}
	for _, m := range mutate {
		m(h)
	}
	uc := New(h.repo, h.gw, h.brk, h.creds, h.sw, h.cfg).(*PaymentUsecase)
	uc.now = func() time.Time { return fixedNow }
	h.uc = uc
	return h
}

// heldBooking is a booking held five minutes ago, so exactly five of the ten
// minute window remain at fixedNow.
func heldBooking(status entity.BookingStatus) entity.Booking {
	held := fixedNow.Add(-5 * time.Minute)
	exp := held.Add(10 * time.Minute)
	return entity.Booking{
		ID:               "RB-00912",
		EventID:          "EV-1",
		Status:           status,
		HeldAt:           &held,
		PaymentExpiresAt: &exp,
		Total:            3000000,
		GrantToken:       "grant-abc",
	}
}
