package usecase_test

import (
	"context"
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
	rdb.Set(ctx, ev.QueueState(), "OPEN", 0)
	rdb.HSet(ctx, ev.QueueHash(), "now_serving", 10, "name", "Konser")
	rdb.Set(ctx, ev.QueueIssued(), 20, 0)

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
