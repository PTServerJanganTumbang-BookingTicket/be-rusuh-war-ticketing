package turnstile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

type nopMetrics struct{}

func (nopMetrics) Incr(string)            {}
func (nopMetrics) Record(string, float64) {}

func TestVerify_errorCodes(t *testing.T) {
	cases := map[string]domain.CaptchaResult{
		`{"success": false, "error-codes": ["internal-error"]}`:       domain.CaptchaUnavailable,
		`{"success": false, "error-codes": ["missing-input-secret"]}`: domain.CaptchaUnavailable,
		`{"success": false, "error-codes": ["bad-request"]}`:          domain.CaptchaInvalid,
		`{"success": false}`: domain.CaptchaInvalid,
	}
	for body, want := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		got := New(srv.URL, "s", time.Second, nopMetrics{}).Verify(context.Background(), "tok", "")
		srv.Close()
		if got != want {
			t.Errorf("%s: got %s, want %s", body, got, want)
		}
	}
}

func TestNew_defaultURLAndBadURL(t *testing.T) {
	if c := New("", "s", time.Second, nopMetrics{}); c.url != DefaultURL {
		t.Errorf("url = %q", c.url)
	}
	c := New("http://[::1", "s", time.Second, nopMetrics{})
	if got := c.Verify(context.Background(), "tok", ""); got != domain.CaptchaUnavailable {
		t.Errorf("bad URL: %s", got)
	}
}

func TestMock_rejectsListedTokens(t *testing.T) {
	m := Mock{Reject: map[string]bool{"bad": true}}
	if m.Verify(context.Background(), "bad", "") != domain.CaptchaInvalid || m.Verify(context.Background(), "ok", "") != domain.CaptchaValid {
		t.Fatal("Mock did not honour Reject")
	}
	if domain.CaptchaValid.String() != "valid" || domain.CaptchaInvalid.String() != "invalid" {
		t.Fatal("CaptchaResult.String")
	}
}
