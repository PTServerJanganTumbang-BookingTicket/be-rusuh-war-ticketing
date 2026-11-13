package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

type fakeMe struct {
	in  usecase.MeInput
	res usecase.MeResult
	err error
}

func (f *fakeMe) Get(_ context.Context, in usecase.MeInput) (usecase.MeResult, error) {
	f.in = in
	return f.res, f.err
}

func serveMe(t *testing.T, f *fakeMe, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(nil, Options{}).WithMe(f)
	mux := http.NewServeMux()
	RegisterRoutes(mux, h)
	req := httptest.NewRequest(http.MethodGet, "/events/E1/queue/me", nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func data(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Meta map[string]any `json:"meta"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return body.Data
}

var t0 = time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)

func TestMeAnswersEveryStateWithTheContractsFields(t *testing.T) {
	exp, pay := t0.Add(5*time.Minute), t0.Add(10*time.Minute)
	cases := []struct {
		name string
		res  usecase.MeResult
		want map[string]any
	}{
		{"queued", usecase.MeResult{State: domain.MeQueued, TicketNo: 31842, NowServing: 31600},
			map[string]any{"state": "QUEUED", "ticket_no": 31842.0, "now_serving": 31600.0}},
		{"granted", usecase.MeResult{State: domain.MeGranted, GrantToken: "gt_1", BookingExpiresAt: exp, ServerTime: t0},
			map[string]any{"state": "GRANTED", "grant_token": "gt_1", "booking_expires_at": "2026-11-10T19:05:00Z", "server_time": "2026-11-10T19:00:00.000Z"}},
		{"selecting", usecase.MeResult{State: domain.MeSelecting, GrantToken: "gt_1", BookingID: "RB-1", BookingExpiresAt: exp, ServerTime: t0},
			map[string]any{"state": "SELECTING", "grant_token": "gt_1", "booking_id": "RB-1", "booking_expires_at": "2026-11-10T19:05:00Z", "server_time": "2026-11-10T19:00:00.000Z"}},
		{"held", usecase.MeResult{State: domain.MeHeld, GrantToken: "gt_1", BookingID: "RB-1", PaymentExpiresAt: pay, ServerTime: t0},
			map[string]any{"state": "HELD", "grant_token": "gt_1", "booking_id": "RB-1", "payment_expires_at": "2026-11-10T19:10:00Z", "server_time": "2026-11-10T19:00:00.000Z"}},
		{"awaiting payment", usecase.MeResult{State: domain.MeAwaitingPayment, GrantToken: "gt_1", BookingID: "RB-1", PaymentExpiresAt: pay, PaymentRef: "pay_1", ServerTime: t0},
			map[string]any{"state": "AWAITING_PAYMENT", "grant_token": "gt_1", "booking_id": "RB-1", "payment_expires_at": "2026-11-10T19:10:00Z", "payment_ref": "pay_1", "server_time": "2026-11-10T19:00:00.000Z"}},
		{"confirmed", usecase.MeResult{State: domain.MeConfirmed, BookingID: "RB-1"}, map[string]any{"state": "CONFIRMED", "booking_id": "RB-1"}},
		{"expired", usecase.MeResult{State: domain.MeExpired}, map[string]any{"state": "EXPIRED"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := serveMe(t, &fakeMe{res: c.res}, nil)
			got := data(t, rec)
			if rec.Code != 200 || len(got) != len(c.want) {
				t.Fatalf("status %d data %v, want exactly %v", rec.Code, got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", cc)
			}
		})
	}
}

func TestMePassesTheCallersCredentialsToTheUseCase(t *testing.T) {
	f := &fakeMe{res: usecase.MeResult{State: domain.MeExpired}}
	serveMe(t, f, func(r *http.Request) {
		r.Header.Set("X-Device-Id", "dev-1")
		r.Header.Set("Authorization", "Bearer abc")
		r.Header.Set(HeaderQueueToken, "from-header")
	})
	want := usecase.MeInput{EventID: "E1", DeviceID: "dev-1", Bearer: "abc", QueueToken: "from-header"}
	if f.in != want {
		t.Fatalf("input %+v, want %+v", f.in, want)
	}
}

func TestMeIgnoresAnOldQueueTokenCookie(t *testing.T) {
	f := &fakeMe{res: usecase.MeResult{State: domain.MeExpired}}
	serveMe(t, f, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "queue_token", Value: "from-cookie"}) })
	if f.in.QueueToken != "" {
		t.Fatalf("queue token %q, want the cookie ignored", f.in.QueueToken)
	}
}

func TestMeNeverSetsACookie(t *testing.T) {
	for _, res := range []usecase.MeResult{
		{State: domain.MeQueued, QueueToken: "qt_9"},   // Bearer-only recovery
		{State: domain.MeConfirmed, BookingID: "RB-1"}, // tombstone
		{State: domain.MeExpired},
	} {
		rec := serveMe(t, &fakeMe{res: res}, nil)
		if c := rec.Header().Values("Set-Cookie"); len(c) != 0 {
			t.Fatalf("%s: Set-Cookie = %v, want none", res.State, c)
		}
	}
}

func foreign(code string) error {
	return fmt.Errorf("%w: %w", usecase.ErrForeignToken, apperror.New(http.StatusUnauthorized, "40100", "milik orang lain").WithCode(code))
}

func TestMeAForeignTokenIs401AndSetsNoCookie(t *testing.T) {
	rec := serveMe(t, &fakeMe{err: foreign(domain.CodeDeviceMismatch)}, func(r *http.Request) { r.Header.Set(HeaderQueueToken, "q") })
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "DEVICE_MISMATCH") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if c := rec.Header().Values("Set-Cookie"); len(c) != 0 {
		t.Fatalf("Set-Cookie = %v, want none", c)
	}
}

func TestMeErrorsKeepTheirStatusAndCode(t *testing.T) {
	rec := serveMe(t, &fakeMe{err: apperror.ErrBadRequest("x").WithCode(domain.CodeDeviceIDRequired)}, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "DEVICE_ID_REQUIRED") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	rec = serveMe(t, &fakeMe{err: errors.New("boom")}, nil)
	if rec.Code != 500 && rec.Code != 503 {
		t.Fatalf("an unexpected error gave %d", rec.Code)
	}
}

func TestMeRejectsAnUnreadableAuthorizationHeader(t *testing.T) {
	f := &fakeMe{res: usecase.MeResult{State: domain.MeExpired}}
	rec := serveMe(t, f, func(r *http.Request) { r.Header.Set("Authorization", "Basic abc") })
	if rec.Code != 401 || f.in.EventID != "" {
		t.Fatalf("status %d, use case called with %+v", rec.Code, f.in)
	}
}

func TestMeIsNotMountedWithoutAUseCase(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHandler(nil, Options{}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events/E1/queue/me", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestMeOmitsADeadlineItDoesNotHaveInsteadOfSendingYearOne(t *testing.T) {
	rec := serveMe(t, &fakeMe{res: usecase.MeResult{State: domain.MeGranted, GrantToken: "gt_1", ServerTime: t0}}, nil)
	got := data(t, rec)
	if _, has := got["booking_expires_at"]; has {
		t.Fatalf("data %v carries a deadline that was never set", got)
	}
	if got["grant_token"] != "gt_1" {
		t.Fatalf("data %v", got)
	}
}
