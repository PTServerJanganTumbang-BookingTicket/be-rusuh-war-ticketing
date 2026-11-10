package features

import (
	"net/http"
	"testing"
)

// Review finding: a foreign token sent in X-Queue-Token is rejected, but the
// caller's own cookie (not the carrier) must not be cleared. A foreign token
// sent in the cookie is cleared.
func TestForeignToken_clearsTheCookieOnlyWhenTheCookieCarriedIt(t *testing.T) {
	w := newWorld(false)
	if err := w.start(); err != nil {
		t.Fatal(err)
	}
	defer w.stop()
	if err := w.seedEvent("E1", "OPEN", ptr(int64(10)), ptr(int64(20)), ""); err != nil {
		t.Fatal(err)
	}
	if err := w.seedEngagementOn("E1", "qt_G", 15, "", w.hashDevice("dev-phone-0000000001"), true, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		carrier   string
		wantClear bool
	}{{"header", false}, {"cookie", true}} {
		r := w.do(joinReq{eventID: "E1", deviceID: "dev-laptop-000000002", queueToken: "qt_G", carrier: c.carrier})
		if r.status != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", c.carrier, r.status)
		}
		_, err := r.cookie("queue_token")
		if cleared := err == nil; cleared != c.wantClear {
			t.Fatalf("%s carrier: cookie cleared = %v, want %v", c.carrier, cleared, c.wantClear)
		}
	}
}
