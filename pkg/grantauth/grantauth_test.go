package grantauth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

var pepper = []byte("test-pepper")

const device = "d3f1a9c4b70e42f88a1c5e6072b9d4af"

type rig struct {
	mr      *miniredis.Miniredis
	handler http.Handler
	got     *grantauth.Grant
}

func newRig(t *testing.T) *rig {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	r := &rig{mr: mr}
	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		g, ok := grantauth.FromContext(req.Context())
		if ok {
			r.got = &g
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.handler = grantauth.Middleware(rdb, grantauth.Config{DevicePepper: pepper})(next)
	return r
}

func (r *rig) guestGrant(token string) {
	r.mr.HSet(rediskeys.Grant(token),
		rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, "RB-1", rediskeys.FieldQueueToken, "qt_1",
		rediskeys.FieldDeviceHash, hmacHex(pepper, device))
}

func (r *rig) accountGrant(token string) {
	r.mr.HSet(rediskeys.Grant(token),
		rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, "RB-2", rediskeys.FieldQueueToken, "qt_2",
		rediskeys.FieldAccountID, "acc_1")
}

func (r *rig) call(t *testing.T, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/events/evt_a/tickets", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec
}

func hmacHex(key []byte, msg string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body.String())
	}
	var body struct {
		Meta struct {
			ErrorCode string `json:"error_code"`
		} `json:"meta"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the contract envelope: %v (%s)", err, rec.Body.String())
	}
	if body.Meta.ErrorCode != code {
		t.Fatalf("error_code = %q, want %q", body.Meta.ErrorCode, code)
	}
	if body.Data == nil {
		t.Fatal("data must be {} on an error, not null or missing")
	}
}

func TestARequestWithoutAGrantTokenIsRejectedAsMissing(t *testing.T) {
	r := newRig(t)
	assertError(t, r.call(t, map[string]string{grantauth.HeaderDeviceID: device}), http.StatusUnauthorized, grantauth.CodeGrantTokenMissing)
}

func TestAnUnknownGrantTokenIsRejectedAsInvalid(t *testing.T) {
	r := newRig(t)
	assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_nope", grantauth.HeaderDeviceID: device}),
		http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid)
}

func TestAGuestGrantWithoutADeviceIDIsABadRequest(t *testing.T) {
	r := newRig(t)
	r.guestGrant("gt_guest")
	assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_guest"}), http.StatusBadRequest, grantauth.CodeDeviceIDRequired)
}

func TestAGuestGrantFromAnotherDeviceIsRejected(t *testing.T) {
	r := newRig(t)
	r.guestGrant("gt_guest")
	assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_guest", grantauth.HeaderDeviceID: "another-device-0001"}),
		http.StatusUnauthorized, grantauth.CodeDeviceMismatch)
}

func TestAGuestGrantFromItsOwnDevicePassesWithEveryField(t *testing.T) {
	r := newRig(t)
	r.guestGrant("gt_guest")
	rec := r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_guest", grantauth.HeaderDeviceID: device})
	if rec.Code != http.StatusNoContent || r.got == nil {
		t.Fatalf("status = %d, grant in context = %v; want the request to reach the handler", rec.Code, r.got != nil)
	}
	want := grantauth.Grant{Token: "gt_guest", EventID: "evt_a", BookingID: "RB-1", QueueToken: "qt_1", DeviceHash: hmacHex(pepper, device)}
	if *r.got != want {
		t.Fatalf("grant = %+v, want %+v", *r.got, want)
	}
}

func TestALoggedInGrantPassesWithoutADeviceID(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	rec := r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_acc"})
	if rec.Code != http.StatusNoContent || r.got == nil || r.got.AccountID != "acc_1" || r.got.DeviceHash != "" {
		t.Fatalf("status = %d, grant = %+v; want a pass with account acc_1", rec.Code, r.got)
	}
}

func TestAGrantWithBothOrNeitherIdentityOrWithoutAQueueTokenIsRejectedAsInvalid(t *testing.T) {
	r := newRig(t)
	r.mr.HSet(rediskeys.Grant("gt_both"), rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, "RB-3",
		rediskeys.FieldQueueToken, "qt_3", rediskeys.FieldAccountID, "acc_1", rediskeys.FieldDeviceHash, hmacHex(pepper, device))
	r.mr.HSet(rediskeys.Grant("gt_none"), rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, "RB-4",
		rediskeys.FieldQueueToken, "qt_4")
	r.mr.HSet(rediskeys.Grant("gt_noqt"), rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, "RB-5",
		rediskeys.FieldDeviceHash, hmacHex(pepper, device))
	for _, tok := range []string{"gt_both", "gt_none", "gt_noqt"} {
		t.Run(tok, func(t *testing.T) {
			assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: tok, grantauth.HeaderDeviceID: device}),
				http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid)
		})
	}
}

func TestRedisBeingDownIsA503NeverAPass(t *testing.T) {
	r := newRig(t)
	r.guestGrant("gt_guest")
	r.mr.SetError("connection refused")
	rec := r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_guest", grantauth.HeaderDeviceID: device})
	if rec.Code != http.StatusServiceUnavailable || r.got != nil {
		t.Fatalf("status = %d, reached handler = %v; want 503 and no pass", rec.Code, r.got != nil)
	}
}

func TestDeviceHashIsTheHexHMACQueueJoinStores(t *testing.T) {
	if got, want := grantauth.DeviceHash(pepper, device), hmacHex(pepper, device); got != want {
		t.Fatalf("DeviceHash = %q, want %q", got, want)
	}
	if got := grantauth.DeviceHash(pepper, ""); got != "" {
		t.Fatalf("an empty device id must hash to empty, got %q", got)
	}
}
