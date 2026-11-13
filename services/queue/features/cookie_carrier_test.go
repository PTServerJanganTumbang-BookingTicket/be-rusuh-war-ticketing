package features

import (
	"net/http"
	"testing"
)

// The queue never reads or writes the queue_token cookie (removed 2026-11-13: a browser's cookie was presented when
// the customer joined another event). A foreign token in X-Queue-Token is refused; the same token in an old cookie is
// not read at all, so the caller simply gets a new place. Neither answer sets a cookie.
func TestForeignToken_isRefusedInTheHeaderAndIgnoredInAnOldCookie(t *testing.T) {
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
		carrier    string
		wantStatus int
	}{{"header", http.StatusUnauthorized}, {"cookie", http.StatusOK}} {
		r := w.do(joinReq{eventID: "E1", deviceID: "dev-laptop-000000002", queueToken: "qt_G", carrier: c.carrier})
		if r.status != c.wantStatus {
			t.Fatalf("%s carrier: status %d, want %d", c.carrier, r.status, c.wantStatus)
		}
		if sc := r.header.Values("Set-Cookie"); len(sc) != 0 {
			t.Fatalf("%s carrier: Set-Cookie = %v, want none", c.carrier, sc)
		}
	}
}
