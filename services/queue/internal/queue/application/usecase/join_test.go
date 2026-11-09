package usecase_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	queueredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/turnstile"
)

var errBoom = errors.New("boom")

// faultyStore fails one chosen operation.
type faultyStore struct {
	*queueredis.Store
	failOn string
}

func (s faultyStore) ClaimAccount(ctx context.Context, ev rediskeys.Event, a, qt, replace string, ttl time.Duration) (bool, string, error) {
	if s.failOn == "claim" {
		return false, "", errBoom
	}
	return s.Store.ClaimAccount(ctx, ev, a, qt, replace, ttl)
}

func (s faultyStore) IssueTicket(ctx context.Context, ev rediskeys.Event) (int64, error) {
	if s.failOn == "incr" {
		return 0, errBoom
	}
	return s.Store.IssueTicket(ctx, ev)
}

func (s faultyStore) SaveEngagement(ctx context.Context, e domain.Engagement, ttl time.Duration) error {
	if s.failOn == "hset" {
		return errBoom
	}
	return s.Store.SaveEngagement(ctx, e, ttl)
}

func (s faultyStore) AddToLine(ctx context.Context, ev rediskeys.Event, qt string, n int64) error {
	if s.failOn == "zadd" {
		return errBoom
	}
	return s.Store.AddToLine(ctx, ev, qt, n)
}

type nopMetrics struct{}

func (nopMetrics) Incr(string)            {}
func (nopMetrics) Record(string, float64) {}

func setup(t *testing.T, failOn string) (*usecase.Join, *goredis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, _ := rediskeys.NewEvent("E1")
	ctx := context.Background()
	rdb.HSet(ctx, ev.QueueHash(), "state", "OPEN", "now_serving", 10, "name", "Konser", "issued", 20)

	issuer, _ := token.NewIssuer("secret", "rusuh-user", time.Hour)
	bearer, _, _ := issuer.Issue("acc-1", "a@example.com", true)
	cfg := usecase.DefaultConfig()
	cfg.ClaimWait = 50 * time.Millisecond
	n := 0
	j := usecase.NewJoin(cfg, usecase.Deps{
		Store:   faultyStore{Store: queueredis.NewStore(rdb), failOn: failOn},
		Captcha: turnstile.Mock{},
		Limiter: ratelimit.New(rdb),
		Tokens:  issuer,
		Metrics: nopMetrics{},
		NewToken: func() string {
			n++
			return "qt_TEST" + string(rune('A'+n))
		},
	})
	return j, rdb, bearer
}

func TestJoin_storeFailuresAre503AndReleaseTheAccount(t *testing.T) {
	for _, op := range []string{"claim", "incr", "hset", "zadd"} {
		t.Run(op, func(t *testing.T) {
			j, rdb, bearer := setup(t, op)
			_, err := j.Join(context.Background(), usecase.JoinInput{EventID: "E1", CaptchaToken: "c", Bearer: bearer, ClientIP: "1.2.3.4"})
			var ae *apperror.AppError
			if !errors.As(err, &ae) || ae.HTTPStatus != http.StatusServiceUnavailable {
				t.Fatalf("Join() error = %v, want 503", err)
			}
			if n, _ := rdb.Exists(context.Background(), "acct:{E1}:acc-1").Result(); n != 0 {
				t.Error("acct:{E1}:acc-1 left behind after a failed join")
			}
		})
	}
}

// acct points to a token that never appears (within ClaimWait): the pointer
// is stale, so the join replaces it and issues a fresh place.
func TestJoin_stalePointerIsReplaced(t *testing.T) {
	j, rdb, bearer := setup(t, "")
	ctx := context.Background()
	rdb.Set(ctx, "acct:{E1}:acc-1", "qt_OTHER", time.Hour)
	res, err := j.Join(ctx, usecase.JoinInput{EventID: "E1", CaptchaToken: "c", Bearer: bearer, ClientIP: "1.2.3.4"})
	if err != nil || res.Engagement.TicketNo != 21 {
		t.Fatalf("Join() = %+v, %v; want ticket 21", res, err)
	}
	if v, _ := rdb.Get(ctx, "acct:{E1}:acc-1").Result(); v != res.Engagement.QueueToken {
		t.Errorf("acct = %q, want %q", v, res.Engagement.QueueToken)
	}
}

func TestJoin_releaseKeepsAnotherJoinsClaim(t *testing.T) {
	s := queueredis.NewStore(goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()}))
	ev, _ := rediskeys.NewEvent("E1")
	ctx := context.Background()
	if ok, _, err := s.ClaimAccount(ctx, ev, "acc-1", "qt_WINNER", "", time.Hour); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := s.ReleaseAccount(ctx, ev, "acc-1", "qt_LOSER"); err != nil {
		t.Fatal(err)
	}
	if qt, ok, _ := s.AccountToken(ctx, ev, "acc-1"); !ok || qt != "qt_WINNER" {
		t.Fatalf("release of another token removed the claim: %q %v", qt, ok)
	}
	if err := s.ReleaseAccount(ctx, ev, "acc-1", "qt_WINNER"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.AccountToken(ctx, ev, "acc-1"); ok {
		t.Fatal("own claim not released")
	}
	if err := s.ReleaseAccount(ctx, ev, "acc-1", "qt_WINNER"); err != nil {
		t.Fatalf("release of a missing key: %v", err)
	}
}

// A logged-in caller presenting a guest's queue_token does not take it over:
// it gets its own place, and acct is claimed (BR-07 cannot be bypassed).
func TestJoin_loggedInCallerDoesNotTakeOverAGuestToken(t *testing.T) {
	j, rdb, bearer := setup(t, "")
	ctx := context.Background()
	guest, err := j.Join(ctx, usecase.JoinInput{EventID: "E1", CaptchaToken: "c", DeviceID: "dev-aaaaaaaaaaaaaaaa", ClientIP: "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := j.Join(ctx, usecase.JoinInput{EventID: "E1", CaptchaToken: "c", Bearer: bearer, QueueToken: guest.Engagement.QueueToken, ClientIP: "1.2.3.5"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome == usecase.Rejoined || res.Engagement.QueueToken == guest.Engagement.QueueToken {
		t.Fatalf("logged-in caller took over the guest place: %+v", res)
	}
	if v, _ := rdb.Get(ctx, "acct:{E1}:acc-1").Result(); v != res.Engagement.QueueToken {
		t.Fatalf("acct = %q, want %q", v, res.Engagement.QueueToken)
	}
	// A second device of the same account now gets the same place back.
	again, err := j.Join(ctx, usecase.JoinInput{EventID: "E1", CaptchaToken: "c", Bearer: bearer, ClientIP: "1.2.3.6"})
	if err != nil || again.Outcome != usecase.Rejoined || again.Engagement.QueueToken != res.Engagement.QueueToken {
		t.Fatalf("second device = %+v, %v", again, err)
	}
}

// hashDevice is hex(HMAC-SHA256(pepper, id)), computed independently of the code under test.
func hashDevice(pepper []byte, id string) string {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(id))
	return hex.EncodeToString(m.Sum(nil))
}

// A presented token that is not the caller's is a 401 that also matches
// ErrForeignToken (the HTTP layer clears the cookie on it), and issues nothing.
func TestJoin_foreignTokenIs401AndMatchesErrForeignToken(t *testing.T) {
	j, rdb, bearer := setup(t, "")
	ctx := context.Background()
	guest, err := j.Join(ctx, usecase.JoinInput{EventID: "E1", CaptchaToken: "c", DeviceID: "dev-aaaaaaaaaaaaaaaa", ClientIP: "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := j.Join(ctx, usecase.JoinInput{EventID: "E1", CaptchaToken: "c", Bearer: bearer, ClientIP: "1.2.3.5"})
	if err != nil {
		t.Fatal(err)
	}
	issuer, _ := token.NewIssuer("secret", "rusuh-user", time.Hour)
	other, _, _ := issuer.Issue("acc-2", "b@example.com", true)
	issued, _ := rdb.HGet(ctx, "queue:{E1}", "issued").Int()
	cases := map[string]struct {
		in   usecase.JoinInput
		code string
	}{
		"guest token, other device":    {usecase.JoinInput{DeviceID: "dev-bbbbbbbbbbbbbbbb", QueueToken: guest.Engagement.QueueToken}, domain.CodeDeviceMismatch},
		"account token, guest":         {usecase.JoinInput{DeviceID: "dev-aaaaaaaaaaaaaaaa", QueueToken: owned.Engagement.QueueToken}, domain.CodeUnauthorized},
		"account token, other account": {usecase.JoinInput{Bearer: other, QueueToken: owned.Engagement.QueueToken}, domain.CodeUnauthorized},
	}
	for name, c := range cases {
		in := c.in
		in.EventID, in.CaptchaToken, in.ClientIP = "E1", "c", "1.2.3.9"
		_, err := j.Join(ctx, in)
		var ae *apperror.AppError
		if !errors.Is(err, usecase.ErrForeignToken) || !errors.As(err, &ae) || ae.HTTPStatus != http.StatusUnauthorized || ae.ErrorCode != c.code {
			t.Fatalf("%s: err = %v, want 401 %s", name, err, c.code)
		}
	}
	// CAPTCHA runs before the ownership check: no captcha + foreign token is 400, not 401.
	_, err = j.Join(ctx, usecase.JoinInput{EventID: "E1", DeviceID: "dev-bbbbbbbbbbbbbbbb", QueueToken: guest.Engagement.QueueToken, ClientIP: "1.2.3.10"})
	var ae *apperror.AppError
	if !errors.As(err, &ae) || ae.ErrorCode != domain.CodeCaptchaRequired {
		t.Fatalf("no captcha + foreign token = %v, want 400 CAPTCHA_REQUIRED", err)
	}
	if n, _ := rdb.HGet(ctx, "queue:{E1}", "issued").Int(); n != issued {
		t.Fatalf("issued moved from %d to %d", issued, n)
	}
}

// failingBookings errors on every query.
type failingBookings struct{}

func (failingBookings) ByQueueToken(context.Context, string, string) (domain.Booking, bool, error) {
	return domain.Booking{}, false, errBoom
}

// After the turn has come, a failed booking lookup is a 503, never a QUEUED
// answer that would re-add the served token to the line.
func TestJoin_servedTicketWithBookingErrorIs503AndNotReadded(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	ev, _ := rediskeys.NewEvent("E1")
	rdb.HSet(ctx, ev.QueueHash(), "state", "OPEN", "now_serving", 100, "issued", 120)
	store := queueredis.NewStore(rdb)
	// Bound to the caller's device, so the test reaches the booking lookup.
	_ = store.SaveEngagement(ctx, domain.Engagement{QueueToken: "qt_SERVED", EventID: "E1", TicketNo: 90,
		DeviceHash: hashDevice(nil, "dev-aaaaaaaaaaaaaaaa"), JoinedAt: time.Now()}, time.Hour)
	j := usecase.NewJoin(usecase.DefaultConfig(), usecase.Deps{
		Store: store, Bookings: failingBookings{}, Captcha: turnstile.Mock{}, Limiter: ratelimit.New(rdb),
		Metrics: nopMetrics{}, NewToken: func() string { return "qt_NEW" },
	})
	_, err := j.Join(ctx, usecase.JoinInput{EventID: "E1", CaptchaToken: "c", DeviceID: "dev-aaaaaaaaaaaaaaaa", QueueToken: "qt_SERVED", ClientIP: "1.2.3.4"})
	var ae *apperror.AppError
	if !errors.As(err, &ae) || ae.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("Join() error = %v, want 503", err)
	}
	if n, _ := rdb.ZCard(ctx, ev.ByTicket()).Result(); n != 0 {
		t.Fatal("served token was put back into by_ticket")
	}
}

// ADR-006 §8.14: there is no straight-in. Even with free places and a controller that
// grants at once, join answers "queued" without waiting for the grant.
func TestJoin_withFreePlacesStillAnswersQueuedAtOnce(t *testing.T) {
	j, rdb, bearer := setup(t, "")
	ctx := context.Background()
	ev, _ := rediskeys.NewEvent("E1")
	rdb.HSet(ctx, ev.QueueHash(), "free_slots", 20, "free_slots_at", time.Now().Unix()) // what the old straight-in rule read
	go func() {                                                                         // a stand-in controller that grants the first token it sees
		for i := 0; i < 200; i++ {
			if keys, _ := rdb.Keys(ctx, "token:*").Result(); len(keys) > 0 {
				rdb.HSet(ctx, keys[0], "grant_token", "gt_1", "booking_expires_at", time.Now().Add(5*time.Minute).UTC().Format(time.RFC3339))
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	start := time.Now()
	res, err := j.Join(ctx, usecase.JoinInput{EventID: "E1", CaptchaToken: "c", Bearer: bearer, ClientIP: "1.2.3.4"})
	if err != nil || res.Outcome != usecase.Queued {
		t.Fatalf("Join() = %+v, %v; want Queued", res, err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("Join took %v; it must not wait for a grant", took)
	}
}
