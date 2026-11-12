package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

type fakeServingGetter struct {
	eventID string
	res     usecase.ServingResult
	err     error
}

func (f *fakeServingGetter) Get(_ context.Context, eventID string) (usecase.ServingResult, error) {
	f.eventID = eventID
	return f.res, f.err
}

func openServing() usecase.ServingResult {
	return usecase.ServingResult{
		Serving:    domain.Serving{State: domain.StateOpen, NowServing: 31600, RatePerMin: 318},
		ServerTime: time.Date(2026, 11, 13, 9, 12, 0, 412_000_000, time.UTC),
		PollAfter:  domain.DefaultPollAfter,
		PollTiers:  domain.PollTiers(),
	}
}

func serveServing(t *testing.T, f *fakeServingGetter, target string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(nil, Options{}).WithServing(f)
	mux := http.NewServeMux()
	RegisterRoutes(mux, h)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestServingAnswersTheContractsBodyCacheableForOneSecond(t *testing.T) {
	f := &fakeServingGetter{res: openServing()}
	rec := serveServing(t, f, "/events/evt_konser_a/queue/serving", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=1" {
		t.Errorf("Cache-Control = %q", got)
	}
	if f.eventID != "evt_konser_a" {
		t.Errorf("event id passed = %q", f.eventID)
	}
	var body struct {
		Meta map[string]any  `json:"meta"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := `{"now_serving":31600,"rate_per_min":318,"state":"OPEN","server_time":"2026-11-13T09:12:00.412Z",` +
		`"poll_after_ms":1000,"poll_tiers":[{"max_position":10,"poll_after_ms":1000},` +
		`{"max_position":100,"poll_after_ms":2000},{"max_position":1000,"poll_after_ms":5000},` +
		`{"max_position":null,"poll_after_ms":10000}]}`
	if string(body.Data) != want {
		t.Errorf("data =\n%s\nwant\n%s", body.Data, want)
	}
	if body.Meta["code"] != float64(200) || body.Meta["server_time"] == nil {
		t.Errorf("meta = %v", body.Meta)
	}
}

func TestServingIgnoresEverythingTheCallerSendsAndSetsNoCookie(t *testing.T) {
	restore := apinizer.SetClock(func() time.Time { return time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)
	plain := serveServing(t, &fakeServingGetter{res: openServing()}, "/events/evt_konser_a/queue/serving", nil)
	busy := serveServing(t, &fakeServingGetter{res: openServing()}, "/events/evt_konser_a/queue/serving?position=5&x=1",
		func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer not-a-token")
			r.Header.Set("X-Device-Id", "dev-1")
			r.Header.Set(HeaderQueueToken, "qt_X")
			r.AddCookie(&http.Cookie{Name: "queue_token", Value: "qt_Y"})
		})
	if busy.Code != http.StatusOK || plain.Body.String() != busy.Body.String() {
		t.Errorf("answers differ:\n%s\n%s", plain.Body, busy.Body)
	}
	if c := busy.Header().Values("Set-Cookie"); len(c) != 0 {
		t.Errorf("Set-Cookie = %v", c)
	}
}

func TestServingErrorsAreNeverCached(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		retry  string
	}{
		{"not found", apperror.ErrNotFound("Event tidak ditemukan").WithCode(domain.CodeNotFound), http.StatusNotFound, ""},
		{"unavailable", apperror.New(http.StatusServiceUnavailable, "50300", "x").WithRetryAfter(time.Second), http.StatusServiceUnavailable, "1"},
	}
	for _, c := range cases {
		rec := serveServing(t, &fakeServingGetter{err: c.err}, "/events/evt_konser_a/queue/serving", nil)
		if rec.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.status)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", c.name, got)
		}
		if got := rec.Header().Get("Retry-After"); got != c.retry {
			t.Errorf("%s: Retry-After = %q, want %q", c.name, got, c.retry)
		}
	}
}

func TestServingIsOnlyMountedWhenTheUseCaseIsWired(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHandler(nil, Options{}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events/E1/queue/serving", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unwired route answered %d", rec.Code)
	}
}
