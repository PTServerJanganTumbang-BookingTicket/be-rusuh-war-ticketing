package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	queueredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/turnstile"
)

// ADR-006 §8.21: a place whose booking has ended (LAPSED, EXPIRED, ABANDONED, CONFIRMED) is not given back on join.
// The caller gets a new ticket number and a new queue_token at the back of the line. A live place is still rejoined.

var (
	endedStatuses = []string{"LAPSED", "EXPIRED", "ABANDONED", "CONFIRMED"}
	liveStatuses  = []string{"GRANTED", "SELECTING", "HELD", "AWAITING_PAYMENT", "PENDING_CANCELLATION"}
)

const (
	guestDevice = "dev-aaaaaaaaaaaaaaaa"
	oldToken    = "qt_OLD"
	issuedStart = 120 // queue:{E1}.issued before the join; a new place gets 121
)

// bookingsByToken is the bookings table seen by join: status per queue_token.
type bookingsByToken map[string]string

func (b bookingsByToken) ByQueueToken(_ context.Context, _ string, qt string) (domain.Booking, bool, error) {
	s, ok := b[qt]
	return domain.Booking{Status: s}, ok, nil
}

type endedFixture struct {
	join   *usecase.Join
	rdb    *goredis.Client
	bearer string
}

// newEndedFixture has one served place (ticket 90, now_serving 100) whose booking has the given status ("" = no
// booking row yet). owner is "guest" (bound to guestDevice) or "account" (acc-1, with acct pointing at it).
func newEndedFixture(t *testing.T, owner, status string) endedFixture {
	t.Helper()
	rdb := goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	ev, _ := rediskeys.NewEvent("E1")
	rdb.HSet(ctx, ev.QueueHash(), "state", "OPEN", "now_serving", 100, "name", "Konser", "issued", issuedStart)

	store := queueredis.NewStore(rdb)
	e := domain.Engagement{QueueToken: oldToken, EventID: "E1", TicketNo: 90, JoinedAt: time.Now(), GrantToken: "gt_OLD"}
	if owner == "account" {
		e.AccountID = "acc-1"
		rdb.Set(ctx, ev.Acct("acc-1"), oldToken, time.Hour)
	} else {
		e.DeviceHash = hashDevice(nil, guestDevice)
	}
	if err := store.SaveEngagement(ctx, e, time.Hour); err != nil {
		t.Fatal(err)
	}
	books := bookingsByToken{}
	if status != "" {
		books[oldToken] = status
	}

	issuer, _ := token.NewIssuer("secret", "rusuh-user", time.Hour)
	bearer, _, _ := issuer.Issue("acc-1", "a@example.com", true)
	j := usecase.NewJoin(usecase.DefaultConfig(), usecase.Deps{
		Store: store, Bookings: books, Captcha: turnstile.Mock{}, Limiter: ratelimit.New(rdb),
		Tokens: issuer, Metrics: nopMetrics{}, NewToken: func() string { return "qt_NEW" },
	})
	return endedFixture{join: j, rdb: rdb, bearer: bearer}
}

func (f endedFixture) issued(t *testing.T) int {
	t.Helper()
	n, err := f.rdb.HGet(context.Background(), "queue:{E1}", "issued").Int()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f endedFixture) acct(t *testing.T) string {
	t.Helper()
	v, _ := f.rdb.Get(context.Background(), "acct:{E1}:acc-1").Result()
	return v
}

func assertNewPlace(t *testing.T, f endedFixture, res usecase.JoinResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if res.Outcome == usecase.Rejoined || res.Engagement.QueueToken == oldToken {
		t.Fatalf("got the ended place back: %+v", res)
	}
	if res.Engagement.QueueToken != "qt_NEW" || res.Engagement.TicketNo != issuedStart+1 {
		t.Fatalf("new place = token %q ticket %d, want qt_NEW ticket %d", res.Engagement.QueueToken, res.Engagement.TicketNo, issuedStart+1)
	}
	if f.issued(t) != issuedStart+1 {
		t.Fatalf("issued = %d, want %d", f.issued(t), issuedStart+1)
	}
	if n, _ := f.rdb.ZScore(context.Background(), "queue:{E1}:by_ticket", "qt_NEW").Result(); n != float64(issuedStart+1) {
		t.Fatalf("new token not in the line at %d (score %v)", issuedStart+1, n)
	}
}

func TestJoin_aGuestWhosePlaceEndedGetsANewTicketAndANewToken(t *testing.T) {
	for _, status := range endedStatuses {
		t.Run(status, func(t *testing.T) {
			f := newEndedFixture(t, "guest", status)
			res, err := f.join.Join(context.Background(), usecase.JoinInput{
				EventID: "E1", CaptchaToken: "c", DeviceID: guestDevice, QueueToken: oldToken, ClientIP: "1.2.3.4"})
			assertNewPlace(t, f, res, err)
		})
	}
}

func TestJoin_anAccountWhosePlaceEndedGetsANewPlaceAndAcctMovesToIt(t *testing.T) {
	for _, status := range endedStatuses {
		t.Run(status, func(t *testing.T) {
			f := newEndedFixture(t, "account", status)
			res, err := f.join.Join(context.Background(), usecase.JoinInput{
				EventID: "E1", CaptchaToken: "c", Bearer: f.bearer, ClientIP: "1.2.3.4"})
			assertNewPlace(t, f, res, err)
			if got := f.acct(t); got != "qt_NEW" {
				t.Fatalf("acct = %q, want qt_NEW", got)
			}
		})
	}
}

func TestJoin_anAccountPresentingItsEndedTokenGetsANewPlaceAndAcctMovesToIt(t *testing.T) {
	for _, status := range endedStatuses {
		t.Run(status, func(t *testing.T) {
			f := newEndedFixture(t, "account", status)
			res, err := f.join.Join(context.Background(), usecase.JoinInput{
				EventID: "E1", CaptchaToken: "c", Bearer: f.bearer, QueueToken: oldToken, ClientIP: "1.2.3.4"})
			assertNewPlace(t, f, res, err)
			if got := f.acct(t); got != "qt_NEW" {
				t.Fatalf("acct = %q, want qt_NEW", got)
			}
		})
	}
}

func TestJoin_afterANewPlaceTheNextJoinRejoinsTheNewPlace(t *testing.T) {
	f := newEndedFixture(t, "account", "LAPSED")
	ctx := context.Background()
	in := usecase.JoinInput{EventID: "E1", CaptchaToken: "c", Bearer: f.bearer, ClientIP: "1.2.3.4"}
	if _, err := f.join.Join(ctx, in); err != nil {
		t.Fatal(err)
	}
	again, err := f.join.Join(ctx, in)
	if err != nil || again.Outcome != usecase.Rejoined || again.Engagement.QueueToken != "qt_NEW" {
		t.Fatalf("second join = %+v, %v; want rejoined qt_NEW", again, err)
	}
	if f.issued(t) != issuedStart+1 {
		t.Fatalf("issued = %d, want %d (no third place)", f.issued(t), issuedStart+1)
	}
}

func TestJoin_aLivePlaceIsStillRejoinedWithTheSameToken(t *testing.T) {
	for _, status := range append([]string{""}, liveStatuses...) {
		for _, owner := range []string{"guest", "account"} {
			name := status
			if name == "" {
				name = "no booking yet"
			}
			t.Run(name+"/"+owner, func(t *testing.T) {
				f := newEndedFixture(t, owner, status)
				in := usecase.JoinInput{EventID: "E1", CaptchaToken: "c", QueueToken: oldToken, ClientIP: "1.2.3.4"}
				if owner == "account" {
					in.Bearer = f.bearer
				} else {
					in.DeviceID = guestDevice
				}
				res, err := f.join.Join(context.Background(), in)
				if err != nil || res.Outcome != usecase.Rejoined || res.Engagement.QueueToken != oldToken {
					t.Fatalf("Join() = %+v, %v; want rejoined %s", res, err, oldToken)
				}
				want := status
				if want == "" {
					want = domain.EngagementQueued
				}
				if res.State != want {
					t.Fatalf("state = %q, want %q", res.State, want)
				}
				if f.issued(t) != issuedStart {
					t.Fatalf("issued moved to %d", f.issued(t))
				}
				if owner == "account" && f.acct(t) != oldToken {
					t.Fatalf("acct = %q, want %s", f.acct(t), oldToken)
				}
			})
		}
	}
}
