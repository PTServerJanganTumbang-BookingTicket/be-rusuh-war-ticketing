package usecase

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

type fakeServing struct {
	s     domain.Serving
	found bool
	err   error
	calls int
}

func (f *fakeServing) Serving(_ context.Context, _ rediskeys.Event) (domain.Serving, bool, error) {
	f.calls++
	return f.s, f.found, f.err
}

var servingNow = time.Date(2026, 11, 13, 9, 12, 0, 412_000_000, time.UTC)

func newGetServing(f *fakeServing) *GetServing {
	return NewGetServing(f, func() time.Time { return servingNow })
}

func appErr(t *testing.T, err error) *apperror.AppError {
	t.Helper()
	var ae *apperror.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not an AppError", err)
	}
	return ae
}

func TestServingAnswersEverySaleStateWithTheCountersAndTheTable(t *testing.T) {
	for _, st := range []domain.SaleState{domain.StateScheduled, domain.StateOpen, domain.StateFullBooked, domain.StateClosed} {
		f := &fakeServing{s: domain.Serving{State: st, NowServing: 31600, RatePerMin: 318}, found: true}
		res, err := newGetServing(f).Get(context.Background(), "evt_konser_a")
		if err != nil {
			t.Fatalf("%s: Get() = %v", st, err)
		}
		if res.State != st || res.NowServing != 31600 || res.RatePerMin != 318 {
			t.Errorf("%s: result %+v", st, res.Serving)
		}
		if !res.ServerTime.Equal(servingNow) || res.PollAfter != domain.DefaultPollAfter || len(res.PollTiers) != 4 {
			t.Errorf("%s: server_time %v, poll_after %v, %d tiers", st, res.ServerTime, res.PollAfter, len(res.PollTiers))
		}
	}
}

func TestServingIsNotFoundForAnEventThatIsNotSeeded(t *testing.T) {
	cases := map[string]*fakeServing{
		"state missing": {found: false},
	}
	for name, f := range cases {
		_, err := newGetServing(f).Get(context.Background(), "evt_konser_a")
		if ae := appErr(t, err); ae.HTTPStatus != http.StatusNotFound || ae.ErrorCode != domain.CodeNotFound {
			t.Errorf("%s: got %d %s, want 404 NOT_FOUND", name, ae.HTTPStatus, ae.ErrorCode)
		}
	}
}

func TestServingRefusesAMalformedEventIDWithoutReadingRedis(t *testing.T) {
	f := &fakeServing{found: true, s: domain.Serving{State: domain.StateOpen}}
	_, err := newGetServing(f).Get(context.Background(), "bad{id}")
	if ae := appErr(t, err); ae.HTTPStatus != http.StatusNotFound {
		t.Errorf("got %d, want 404", ae.HTTPStatus)
	}
	if f.calls != 0 {
		t.Errorf("Redis read %d times for a malformed id", f.calls)
	}
}

func TestServingIsUnavailableWithRetryAfterWhenRedisFails(t *testing.T) {
	_, err := newGetServing(&fakeServing{err: errors.New("redis down")}).Get(context.Background(), "evt_konser_a")
	ae := appErr(t, err)
	if ae.HTTPStatus != http.StatusServiceUnavailable || ae.RetryAfter != time.Second {
		t.Errorf("got %d retry %v, want 503 retry 1s", ae.HTTPStatus, ae.RetryAfter)
	}
}

func TestServingIsUnavailableNotNotFoundForAnUnknownState(t *testing.T) {
	f := &fakeServing{s: domain.Serving{State: "PAUSED"}, found: true}
	_, err := newGetServing(f).Get(context.Background(), "evt_konser_a")
	if ae := appErr(t, err); ae.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", ae.HTTPStatus)
	}
}
