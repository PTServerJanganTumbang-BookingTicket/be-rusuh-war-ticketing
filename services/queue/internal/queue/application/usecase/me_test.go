package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

// meStore answers only what /queue/me asks. Any other QueueStore method panics on the nil embedded interface,
// which is how the tests prove, for example, that a waiting customer never reaches Aurora or a write.
type meStore struct {
	outbound.QueueStore
	engagements map[string]domain.Engagement
	reread      map[string]domain.Engagement // what a second read of the same token returns (a grant landing mid-request)
	reads       map[string]int
	acct        map[string]string
	nowServing  int64
	inLine      map[string]bool
	settled     map[string]string
	err         error
}

func (s *meStore) Engagement(_ context.Context, qt string) (domain.Engagement, bool, error) {
	if s.err != nil {
		return domain.Engagement{}, false, s.err
	}
	s.reads[qt]++
	if e, ok := s.reread[qt]; ok && s.reads[qt] > 1 {
		return e, true, nil
	}
	e, ok := s.engagements[qt]
	return e, ok, nil
}
func (s *meStore) AccountToken(_ context.Context, _ rediskeys.Event, account string) (string, bool, error) {
	qt, ok := s.acct[account]
	return qt, ok, s.err
}
func (s *meStore) NowServing(context.Context, rediskeys.Event) (int64, error) {
	return s.nowServing, s.err
}
func (s *meStore) InLine(_ context.Context, _ rediskeys.Event, qt string) (bool, error) {
	return s.inLine[qt], s.err
}
func (s *meStore) Settled(_ context.Context, qt string) (string, bool, error) {
	id, ok := s.settled[qt]
	return id, ok, s.err
}

type meDetails struct {
	rows  map[string]domain.BookingDetail
	calls int
	err   error
}

func (d *meDetails) Detail(_ context.Context, _, qt string) (domain.BookingDetail, bool, error) {
	d.calls++
	r, ok := d.rows[qt]
	return r, ok, d.err
}

type countMetrics struct{ counts map[string]int }

func (m *countMetrics) Incr(n string)          { m.counts[n]++ }
func (m *countMetrics) Record(string, float64) {}

type meTokens struct{ subject string }

func (t meTokens) Verify(raw string) (token.Claims, error) {
	if raw == "bad" {
		return token.Claims{}, errors.New("bad token")
	}
	return token.Claims{Subject: t.subject}, nil
}

var now0 = time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)

type meFixture struct {
	store   *meStore
	details *meDetails
	metrics *countMetrics
	me      *Me
}

func newMe(t *testing.T) *meFixture {
	t.Helper()
	f := &meFixture{
		store: &meStore{engagements: map[string]domain.Engagement{}, reread: map[string]domain.Engagement{}, reads: map[string]int{},
			acct: map[string]string{}, inLine: map[string]bool{}, settled: map[string]string{}, nowServing: 100},
		details: &meDetails{rows: map[string]domain.BookingDetail{}},
		metrics: &countMetrics{counts: map[string]int{}},
	}
	cfg := DefaultConfig()
	cfg.DevicePepper = []byte("pepper")
	f.me = NewMe(cfg, Deps{Store: f.store, Details: f.details, Metrics: f.metrics, Tokens: meTokens{subject: "acc-1"}, Clock: func() time.Time { return now0 }})
	return f
}

func (f *meFixture) guest(qt string, ticket int64, deviceID string) domain.Engagement {
	e := domain.Engagement{QueueToken: qt, EventID: "E1", TicketNo: ticket, DeviceHash: caller{pepper: []byte("pepper")}.deviceHash(deviceID)}
	f.store.engagements[qt] = e
	return e
}

func (f *meFixture) member(qt string, ticket int64, account string) domain.Engagement {
	e := domain.Engagement{QueueToken: qt, EventID: "E1", TicketNo: ticket, AccountID: account}
	f.store.engagements[qt] = e
	return e
}

func code(err error) string {
	var ae *apperror.AppError
	if errors.As(err, &ae) {
		return ae.ErrorCode
	}
	return ""
}

func TestMeMapsEveryBookingStatus(t *testing.T) {
	exp := now0.Add(5 * time.Minute)
	pay := now0.Add(10 * time.Minute)
	cases := []struct {
		status string
		want   string
		check  func(r MeResult) bool
	}{
		{"GRANTED", domain.MeGranted, func(r MeResult) bool {
			return r.GrantToken == "G1" && r.BookingExpiresAt.Equal(exp) && r.BookingID == ""
		}},
		{"SELECTING", domain.MeSelecting, func(r MeResult) bool { return r.BookingID == "RB-1" && r.GrantToken == "G1" }},
		{"HELD", domain.MeHeld, func(r MeResult) bool {
			return r.BookingID == "RB-1" && r.GrantToken == "G1" && r.PaymentExpiresAt.Equal(pay) && r.BookingExpiresAt.IsZero()
		}},
		{"AWAITING_PAYMENT", domain.MeAwaitingPayment, func(r MeResult) bool {
			return r.PaymentRef == "pay_1" && r.PaymentExpiresAt.Equal(pay) && r.BookingID == "RB-1" && r.GrantToken == "G1"
		}},
		// Past its deadline, in the 30 s grace: still AWAITING_PAYMENT with the real deadline, which tells the client to
		// show "memverifikasi pembayaran", not the expired screen (api-contracts §3.3, v2.27).
		{"PENDING_CANCELLATION", domain.MeAwaitingPayment, func(r MeResult) bool {
			return r.PaymentRef == "pay_1" && r.PaymentExpiresAt.Equal(pay) && r.BookingID == "RB-1" && r.GrantToken == "G1"
		}},
		{"CONFIRMED", domain.MeConfirmed, func(r MeResult) bool { return r.BookingID == "RB-1" && r.GrantToken == "" }},
		{"EXPIRED", domain.MeExpired, func(r MeResult) bool { return r.GrantToken == "" && r.BookingID == "" }},
		{"LAPSED", domain.MeExpired, func(r MeResult) bool { return r.GrantToken == "" }},
		{"ABANDONED", domain.MeExpired, func(r MeResult) bool { return r.GrantToken == "" }},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			f := newMe(t)
			e := f.guest("qt1", 50, "dev-1")
			e.GrantToken = "G1"
			f.store.engagements["qt1"] = e
			f.details.rows["qt1"] = domain.BookingDetail{ID: "RB-1", Status: c.status, GrantToken: "G1", BookingExpiresAt: exp, PaymentExpiresAt: pay, PaymentRef: "pay_1"}
			r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"})
			if err != nil || r.State != c.want || !c.check(r) || !r.ServerTime.Equal(now0) {
				t.Fatalf("result %+v err %v, want state %s", r, err, c.want)
			}
		})
	}
}

func TestMeAWaitingCustomerIsAnsweredFromRedisAlone(t *testing.T) {
	f := newMe(t)
	f.guest("qt1", 150, "dev-1") // behind now_serving 100, no grant yet
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"})
	if err != nil || r.State != domain.MeQueued || r.TicketNo != 150 || r.NowServing != 100 {
		t.Fatalf("result %+v err %v", r, err)
	}
	if f.details.calls != 0 {
		t.Fatal("a waiting customer reached Aurora")
	}
}

func TestMePendingGrantIsQueuedAndAlmostThere(t *testing.T) {
	f := newMe(t)
	f.guest("qt1", 90, "dev-1")
	f.store.inLine["qt1"] = true // admitted (90 <= 100), grant not written yet
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"})
	if err != nil || r.State != domain.MeQueued || r.TicketNo > r.NowServing {
		t.Fatalf("result %+v err %v", r, err)
	}
	if f.metrics.counts[domain.MetricLostGrant] != 0 {
		t.Fatal("a pending grant was reported as lost")
	}
}

func TestMeAdmittedButMissingFromTheLineIsALostGrant(t *testing.T) {
	f := newMe(t)
	f.guest("qt1", 90, "dev-1") // not in line, no grant in the hash
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"})
	if err != nil || r.State != domain.MeQueued || f.metrics.counts[domain.MetricLostGrant] != 1 {
		t.Fatalf("result %+v err %v metrics %v", r, err, f.metrics.counts)
	}
}

func TestMeAGrantLandingMidRequestIsNotLost(t *testing.T) {
	f := newMe(t)
	f.guest("qt1", 90, "dev-1")
	granted := f.store.engagements["qt1"]
	granted.GrantToken = "G1"
	f.store.reread["qt1"] = granted // admission-ctl copied the grant and ZREMed between our two reads
	f.details.rows["qt1"] = domain.BookingDetail{ID: "RB-1", Status: "GRANTED", GrantToken: "G1", BookingExpiresAt: now0.Add(time.Minute)}
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"})
	if err != nil || r.State != domain.MeGranted || f.metrics.counts[domain.MetricLostGrant] != 0 {
		t.Fatalf("result %+v err %v metrics %v", r, err, f.metrics.counts)
	}
}

func TestMeAGrantWithoutABookingRowIsALostGrant(t *testing.T) {
	f := newMe(t)
	e := f.guest("qt1", 90, "dev-1")
	e.GrantToken = "G1"
	f.store.engagements["qt1"] = e
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"})
	if err != nil || r.State != domain.MeQueued || f.metrics.counts[domain.MetricLostGrant] != 1 {
		t.Fatalf("result %+v err %v metrics %v", r, err, f.metrics.counts)
	}
}

func TestMeATombstoneAnswersConfirmedAndClearsTheCookie(t *testing.T) {
	f := newMe(t)
	f.store.settled["qt1"] = "RB-9"
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"})
	if err != nil || r.State != domain.MeConfirmed || r.BookingID != "RB-9" || !r.ClearCookie {
		t.Fatalf("result %+v err %v", r, err)
	}
}

func TestMeNoTokenAndNoTombstoneIsExpiredAndKeepsTheCookie(t *testing.T) {
	f := newMe(t)
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "gone"})
	if err != nil || r.State != domain.MeExpired || r.ClearCookie {
		t.Fatalf("result %+v err %v", r, err)
	}
}

func TestMeATokenOfAnotherEventIsTreatedAsMissing(t *testing.T) {
	f := newMe(t)
	e := f.guest("qt1", 50, "dev-1")
	e.EventID = "E2"
	f.store.engagements["qt1"] = e
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"})
	if err != nil || r.State != domain.MeExpired {
		t.Fatalf("result %+v err %v", r, err)
	}
}

func TestMeRejectsATokenThatIsNotTheCallers(t *testing.T) {
	f := newMe(t)
	f.guest("guest", 50, "dev-1")
	f.member("member", 51, "acc-OTHER")

	_, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-2", QueueToken: "guest"})
	if code(err) != domain.CodeDeviceMismatch || !errors.Is(err, ErrForeignToken) {
		t.Errorf("guest token from another device: %v", err)
	}
	_, err = f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "member"})
	if code(err) != domain.CodeUnauthorized || !errors.Is(err, ErrForeignToken) {
		t.Errorf("account token presented by a guest: %v", err)
	}
	if f.metrics.counts[domain.MetricDeviceMismatch] != 1 || f.metrics.counts[domain.MetricTokenAccountMismatch] != 1 {
		t.Errorf("metrics %v", f.metrics.counts)
	}
}

func TestMeAGuestMustSendADeviceId(t *testing.T) {
	f := newMe(t)
	f.guest("qt1", 50, "dev-1")
	_, err := f.me.Get(context.Background(), MeInput{EventID: "E1", QueueToken: "qt1"})
	if code(err) != domain.CodeDeviceIDRequired {
		t.Fatalf("error %v, want DEVICE_ID_REQUIRED", err)
	}
}

func TestMeNoCredentialIsUnauthorized(t *testing.T) {
	f := newMe(t)
	_, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1"})
	if code(err) != domain.CodeUnauthorized {
		t.Fatalf("error %v, want UNAUTHORIZED", err)
	}
}

func TestMeBearerFindsTheAccountsTokenAndReissuesTheCookie(t *testing.T) {
	f := newMe(t)
	f.member("qt-acc", 150, "acc-1")
	f.store.acct["acc-1"] = "qt-acc"
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", Bearer: "good"})
	if err != nil || r.State != domain.MeQueued || r.QueueToken != "qt-acc" || !r.SetCookie {
		t.Fatalf("result %+v err %v", r, err)
	}
}

func TestMeBearerWithoutAnAcctIsUnauthorized(t *testing.T) {
	f := newMe(t)
	_, err := f.me.Get(context.Background(), MeInput{EventID: "E1", Bearer: "good"})
	if code(err) != domain.CodeUnauthorized {
		t.Fatalf("error %v, want UNAUTHORIZED", err)
	}
}

func TestMeBearerWinsOverAGuestCookie(t *testing.T) {
	f := newMe(t)
	f.guest("guest-qt", 20, "dev-1")
	f.member("qt-acc", 150, "acc-1")
	f.store.acct["acc-1"] = "qt-acc"
	r, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", Bearer: "good", QueueToken: "guest-qt"})
	if err != nil || r.QueueToken != "qt-acc" || r.TicketNo != 150 {
		t.Fatalf("result %+v err %v: the guest place must not be taken over", r, err)
	}
}

func TestMeBadBearerIsRejected(t *testing.T) {
	f := newMe(t)
	_, err := f.me.Get(context.Background(), MeInput{EventID: "E1", Bearer: "bad"})
	if code(err) != domain.CodeTokenInvalid {
		t.Fatalf("error %v, want TOKEN_INVALID", err)
	}
}

func TestMeUnknownEventIsNotFound(t *testing.T) {
	f := newMe(t)
	_, err := f.me.Get(context.Background(), MeInput{EventID: "bad{id", DeviceID: "d", QueueToken: "q"})
	if code(err) != domain.CodeNotFound {
		t.Fatalf("error %v, want NOT_FOUND", err)
	}
}

func TestMeRedisOrAuroraFailureIsUnavailable(t *testing.T) {
	f := newMe(t)
	f.guest("qt1", 50, "dev-1")
	f.store.err = errors.New("redis down")
	if _, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"}); err == nil || errors.Is(err, ErrForeignToken) {
		t.Fatalf("redis failure gave %v, want a 503", err)
	}

	f = newMe(t)
	e := f.guest("qt1", 50, "dev-1")
	e.GrantToken = "G1"
	f.store.engagements["qt1"] = e
	f.details.err = errors.New("aurora down")
	if _, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"}); err == nil {
		t.Fatal("an Aurora failure was hidden")
	}
}

func TestMeAnUnknownStatusIsAnError(t *testing.T) {
	f := newMe(t)
	e := f.guest("qt1", 50, "dev-1")
	e.GrantToken = "G1"
	f.store.engagements["qt1"] = e
	f.details.rows["qt1"] = domain.BookingDetail{ID: "RB-1", Status: "WEIRD", GrantToken: "G1"}
	if _, err := f.me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"}); err == nil {
		t.Fatal("an unknown status was mapped silently")
	}
}

func TestMeWithoutAnAuroraConnectionIsUnavailableNotAPanic(t *testing.T) {
	f := newMe(t)
	e := f.guest("qt1", 50, "dev-1")
	e.GrantToken = "G1"
	f.store.engagements["qt1"] = e
	cfg := DefaultConfig()
	cfg.DevicePepper = []byte("pepper")
	me := NewMe(cfg, Deps{Store: f.store, Metrics: f.metrics, Tokens: meTokens{subject: "acc-1"}}) // no Details
	if _, err := me.Get(context.Background(), MeInput{EventID: "E1", DeviceID: "dev-1", QueueToken: "qt1"}); err == nil {
		t.Fatal("a granted customer got an answer without a database")
	}
}
