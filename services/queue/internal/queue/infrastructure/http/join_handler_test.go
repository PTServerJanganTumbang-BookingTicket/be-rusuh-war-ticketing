package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/core"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

type fakeJoin struct {
	in  usecase.JoinInput
	res usecase.JoinResult
	err error
}

func (f *fakeJoin) Join(_ context.Context, in usecase.JoinInput) (usecase.JoinResult, error) {
	f.in = in
	return f.res, f.err
}

func serveJoin(t *testing.T, f *fakeJoin, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(f, Options{SSEBaseURL: "https://events.my-rusuh.my.id/"})
	mux := http.NewServeMux()
	RegisterRoutes(mux, h)
	req := httptest.NewRequest(http.MethodPost, "/events/E1/queue/join", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:4444"
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func joined(outcome usecase.Outcome) usecase.JoinResult {
	return usecase.JoinResult{
		Outcome:    outcome,
		Engagement: domain.Engagement{QueueToken: "qt_A", TicketNo: 120},
		NowServing: 100, EventName: "Konser", State: "QUEUED",
	}
}

func TestAFirstJoinAnswersWithTheTicketPositionAndStreamAndSetsNoCookie(t *testing.T) {
	f := &fakeJoin{res: joined(usecase.Queued)}
	rec := serveJoin(t, f, `{"captcha_token":"cap"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	d := data(t, rec)
	if d["ticket_no"] != float64(120) || d["now_serving"] != float64(100) || d["position"] != float64(20) || d["queue_token"] != "qt_A" {
		t.Fatalf("data = %v", d)
	}
	if d["sse_url"] != "https://events.my-rusuh.my.id/events/E1/queue/stream" {
		t.Fatalf("sse_url = %v", d["sse_url"])
	}
	if ev, _ := d["event"].(map[string]any); ev["id"] != "E1" || ev["name"] != "Konser" {
		t.Fatalf("event = %v", d["event"])
	}
	if _, has := d["rejoined"]; has {
		t.Fatal("a first join must not say rejoined")
	}
	if c := rec.Header().Values("Set-Cookie"); len(c) != 0 {
		t.Fatalf("Set-Cookie = %v, want none", c)
	}
	if f.in.EventID != "E1" || f.in.CaptchaToken != "cap" || f.in.ClientIP != "203.0.113.9" {
		t.Fatalf("input = %+v", f.in)
	}
}

func TestARejoinSaysSoAndReportsTheEngagementStateAndSetsNoCookie(t *testing.T) {
	rec := serveJoin(t, &fakeJoin{res: joined(usecase.Rejoined)}, `{}`, nil)
	d := data(t, rec)
	if d["rejoined"] != true || d["state"] != "QUEUED" || d["position"] != float64(20) || d["queue_token"] != "qt_A" {
		t.Fatalf("data = %v", d)
	}
	if c := rec.Header().Values("Set-Cookie"); len(c) != 0 {
		t.Fatalf("Set-Cookie = %v, want none", c)
	}
}

func TestJoinPassesTheCallersCredentialsToTheUseCase(t *testing.T) {
	f := &fakeJoin{res: joined(usecase.Queued)}
	serveJoin(t, f, `{}`, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer jwt")
		r.Header.Set("X-Device-Id", "dev-1")
		r.Header.Set(HeaderQueueToken, "from-header")
	})
	if f.in.Bearer != "jwt" || f.in.DeviceID != "dev-1" || f.in.QueueToken != "from-header" {
		t.Fatalf("input = %+v", f.in)
	}
}

// A queue_token cookie left in a browser by an older release is never read: it caused the wrong place to be presented
// when the customer joined another event.
func TestJoinIgnoresAnOldQueueTokenCookie(t *testing.T) {
	f := &fakeJoin{res: joined(usecase.Queued)}
	serveJoin(t, f, `{}`, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "queue_token", Value: "from-cookie"}) })
	if f.in.QueueToken != "" {
		t.Fatalf("queue token = %q, want the cookie ignored", f.in.QueueToken)
	}
}

func TestJoinRejectsAnUnreadableBodyOrAuthorizationHeaderWithoutCallingTheUseCase(t *testing.T) {
	f := &fakeJoin{}
	if rec := serveJoin(t, f, `not json`, nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), domain.CodeValidation) {
		t.Fatalf("bad body: %d %s", rec.Code, rec.Body)
	}
	rec := serveJoin(t, f, `{}`, func(r *http.Request) { r.Header.Set("Authorization", "Basic xx") })
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), domain.CodeTokenInvalid) {
		t.Fatalf("bad auth: %d %s", rec.Code, rec.Body)
	}
	if f.in.EventID != "" {
		t.Fatal("the use case ran for a request that should have been rejected")
	}
}

func TestJoinErrorsKeepTheirStatusAndSetNoCookie(t *testing.T) {
	f := &fakeJoin{err: apperror.ErrBadRequest("captcha").WithCode("CAPTCHA_FAILED")}
	if rec := serveJoin(t, f, `{}`, nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "CAPTCHA_FAILED") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("%d %s cookies=%v", rec.Code, rec.Body, rec.Result().Cookies())
	}

	f.err = foreign("TOKEN_FOREIGN")
	rec := serveJoin(t, f, `{}`, func(r *http.Request) { r.Header.Set(HeaderQueueToken, "x") })
	if rec.Code != 401 || len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("%d Set-Cookie=%v", rec.Code, rec.Header().Values("Set-Cookie"))
	}
}

func TestTheStreamURLIsRelativeWithoutABaseAndEscapesTheEventID(t *testing.T) {
	h := NewHandler(nil, Options{})
	if got := h.SSEURL("a b"); got != "/events/a%20b/queue/stream" {
		t.Fatalf("got %q", got)
	}
}

func TestClientIPPrefersTheGatewaysSourceAndIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	if got := ClientIP(r); got != "10.0.0.1" {
		t.Fatalf("peer = %q", got)
	}
	r.RemoteAddr = "bare"
	if got := ClientIP(r); got != "bare" {
		t.Fatalf("no port = %q", got)
	}
	gw, err := (&core.RequestAccessorV2{}).EventToRequestWithContext(context.Background(), events.APIGatewayV2HTTPRequest{
		RawPath: "/events/E1/queue/join",
		RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
			Method: "POST", Path: "/events/E1/queue/join", SourceIP: "198.51.100.7"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ClientIP(gw); got != "198.51.100.7" {
		t.Fatalf("gateway = %q", got)
	}
}
