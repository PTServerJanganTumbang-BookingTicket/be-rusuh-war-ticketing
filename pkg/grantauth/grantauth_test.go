package grantauth_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
)

var pepper = []byte("test-pepper")

const device = "d3f1a9c4b70e42f88a1c5e6072b9d4af"

const expiredToken = "expired.jwt.token"

// sessions signs with one secret; other signs with another, so its tokens must be refused. The verifier answers
// ErrExpired for expiredToken, which an Issuer cannot produce without waiting.
var (
	sessions = mustIssuer("session-secret")
	other    = mustIssuer("another-secret")
)

func mustIssuer(secret string) *token.Issuer {
	i, err := token.NewIssuer(secret, "rusuh-user", time.Hour)
	if err != nil {
		panic(err)
	}
	return i
}

type verifier struct{}

func (verifier) Verify(raw string) (token.Claims, error) {
	if raw == expiredToken {
		return token.Claims{}, token.ErrExpired
	}
	return sessions.Verify(raw)
}

func bearer(t *testing.T, i *token.Issuer, account string) map[string]string {
	t.Helper()
	tok, _, err := i.Issue(account, account+"@example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + tok}
}

func with(h map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

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
	r.handler = grantauth.Middleware(rdb, grantauth.Config{DevicePepper: pepper, Tokens: verifier{}})(next)
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
	rec := r.call(t, with(bearer(t, sessions, "acc_1"), grantauth.HeaderGrantToken, "gt_acc"))
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

func TestAnAccountGrantWithoutAnAuthorizationHeaderIsUnauthorized(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_acc"}), http.StatusUnauthorized, grantauth.CodeUnauthorized)
}

func TestAnAccountGrantWithAnUnreadableAuthorizationHeaderIsATokenInvalid(t *testing.T) {
	for name, header := range map[string]string{"basic scheme": "Basic abc", "no token": "Bearer ", "bare word": "Bearer", "garbage": "Bearer not-a-jwt"} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.accountGrant("gt_acc")
			assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_acc", "Authorization": header}),
				http.StatusUnauthorized, grantauth.CodeTokenInvalid)
		})
	}
}

func TestAnAccountGrantWithAnExpiredTokenIsTokenExpired(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_acc", "Authorization": "Bearer " + expiredToken}),
		http.StatusUnauthorized, grantauth.CodeTokenExpired)
}

func TestAnAccountGrantWithATokenSignedByAnotherSecretIsATokenInvalid(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	assertError(t, r.call(t, with(bearer(t, other, "acc_1"), grantauth.HeaderGrantToken, "gt_acc")),
		http.StatusUnauthorized, grantauth.CodeTokenInvalid)
}

func TestAnAccountGrantWithTheValidTokenOfAnotherAccountIsUnauthorized(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	r := newRig(t)
	r.accountGrant("gt_acc")
	defer func() {
		if !strings.Contains(logs.String(), "metric=GrantAccountMismatch") {
			t.Errorf("log = %q, want metric=GrantAccountMismatch", logs.String())
		}
	}()
	assertError(t, r.call(t, with(bearer(t, sessions, "acc_2"), grantauth.HeaderGrantToken, "gt_acc")),
		http.StatusUnauthorized, grantauth.CodeUnauthorized)
	if r.got != nil {
		t.Fatal("the handler must not run for another account's session")
	}
}

func TestAnAccountGrantWithItsOwnTokenPassesAndTheGrantKeepsItsAccount(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	// a device id, if sent, is not checked for an account grant
	rec := r.call(t, with(bearer(t, sessions, "acc_1"), grantauth.HeaderGrantToken, "gt_acc", grantauth.HeaderDeviceID, "whatever"))
	want := grantauth.Grant{Token: "gt_acc", EventID: "evt_a", BookingID: "RB-2", QueueToken: "qt_2", AccountID: "acc_1"}
	if rec.Code != http.StatusNoContent || r.got == nil || *r.got != want {
		t.Fatalf("status = %d, grant = %+v; want a pass with %+v", rec.Code, r.got, want)
	}
}

func TestAGuestGrantIgnoresTheAuthorizationHeader(t *testing.T) {
	headers := map[string]map[string]string{
		"none":             {},
		"a valid token":    bearer(t, sessions, "acc_9"),
		"an invalid token": {"Authorization": "Bearer not-a-jwt"},
		"an expired token": {"Authorization": "Bearer " + expiredToken},
		"another scheme":   {"Authorization": "Basic abc"},
	}
	for name, h := range headers {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.guestGrant("gt_guest")
			rec := r.call(t, with(h, grantauth.HeaderGrantToken, "gt_guest", grantauth.HeaderDeviceID, device))
			if rec.Code != http.StatusNoContent || r.got == nil || r.got.DeviceHash == "" {
				t.Fatalf("status = %d, grant = %+v; want a pass as the guest", rec.Code, r.got)
			}
		})
	}
}

func TestADeadGrantIsReportedBeforeABadToken(t *testing.T) {
	r := newRig(t)
	assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_nope", "Authorization": "Bearer not-a-jwt"}),
		http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid)
}

func TestTheMiddlewareRefusesToBuildWithoutATokenVerifier(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Middleware without Tokens must panic at construction, not fail the first account request")
		}
	}()
	grantauth.Middleware(nil, grantauth.Config{DevicePepper: pepper})
}

func TestBearerTokenReadsTheSchemeCaseInsensitivelyAndTrimsTheToken(t *testing.T) {
	cases := []struct {
		header string
		token  string
		ok     bool
	}{
		{"", "", true},
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"Bearer  abc ", "abc", true},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Bearer    ", "", false}, // whitespace only: rejected, not a silent guest
		{"Basic abc", "", false},
		{"Bearer abc.def.ghi", "abc.def.ghi", true},
		{"abc", "", false},
	}
	for _, c := range cases {
		tok, ok := grantauth.BearerToken(c.header)
		if tok != c.token || ok != c.ok {
			t.Errorf("BearerToken(%q) = (%q, %v), want (%q, %v)", c.header, tok, ok, c.token, c.ok)
		}
	}
}

// --- Payment session (ADR-007, docs/07-payment/06-grant-guard.md §12) ---

func newSessionRig(t *testing.T) *rig {
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
	r.handler = grantauth.MiddlewareWithSession(rdb, grantauth.Config{DevicePepper: pepper, Tokens: verifier{}})(next)
	return r
}

func (r *rig) session(session, grantToken string) {
	if err := r.mr.Set(rediskeys.PaymentSession(session), grantToken); err != nil {
		panic(err)
	}
}

func TestAPaymentSessionStandsInForItsGrantWithoutDeviceOrBearer(t *testing.T) {
	for name, setup := range map[string]func(*rig){
		"guest":   func(r *rig) { r.guestGrant("gt_guest"); r.session("ps_1", "gt_guest") },
		"account": func(r *rig) { r.accountGrant("gt_guest"); r.session("ps_1", "gt_guest") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newSessionRig(t)
			setup(r)
			rec := r.call(t, map[string]string{grantauth.HeaderPaymentSession: "ps_1"})
			if rec.Code != http.StatusNoContent || r.got == nil {
				t.Fatalf("status = %d, reached handler = %v; want a pass", rec.Code, r.got != nil)
			}
			if r.got.Token != "gt_guest" || !r.got.ViaSession {
				t.Fatalf("grant = %+v; want the session's grant, marked ViaSession", *r.got)
			}
		})
	}
}

func TestAnUnknownPaymentSessionIsRejectedAsInvalid(t *testing.T) {
	r := newSessionRig(t)
	assertError(t, r.call(t, map[string]string{grantauth.HeaderPaymentSession: "ps_nope"}),
		http.StatusUnauthorized, grantauth.CodePaymentSessionInvalid)
}

func TestAPaymentSessionDiesTheMomentItsGrantIsDeleted(t *testing.T) {
	r := newSessionRig(t)
	r.guestGrant("gt_guest")
	r.session("ps_1", "gt_guest")
	r.mr.Del(rediskeys.Grant("gt_guest"))
	assertError(t, r.call(t, map[string]string{grantauth.HeaderPaymentSession: "ps_1"}),
		http.StatusUnauthorized, grantauth.CodePaymentSessionInvalid)
	if r.got != nil {
		t.Fatal("a session without its grant must never reach the handler")
	}
}

func TestAPaymentSessionPointingAtAMalformedGrantIsRejectedAsInvalid(t *testing.T) {
	r := newSessionRig(t)
	r.mr.HSet(rediskeys.Grant("gt_none"), rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, "RB-4",
		rediskeys.FieldQueueToken, "qt_4")
	r.session("ps_1", "gt_none")
	assertError(t, r.call(t, map[string]string{grantauth.HeaderPaymentSession: "ps_1"}),
		http.StatusUnauthorized, grantauth.CodePaymentSessionInvalid)
}

func TestAPaymentSessionTogetherWithAGrantTokenIsABadRequest(t *testing.T) {
	r := newSessionRig(t)
	r.guestGrant("gt_guest")
	r.session("ps_1", "gt_guest")
	assertError(t, r.call(t, map[string]string{grantauth.HeaderPaymentSession: "ps_1", grantauth.HeaderGrantToken: "gt_guest"}),
		http.StatusBadRequest, "VALIDATION_ERROR")
}

func TestAStrayDeviceIDOrAuthorizationNextToAPaymentSessionIsIgnored(t *testing.T) {
	r := newSessionRig(t)
	r.accountGrant("gt_acc")
	r.session("ps_1", "gt_acc")
	rec := r.call(t, with(bearer(t, other, "someone_else"),
		grantauth.HeaderPaymentSession, "ps_1", grantauth.HeaderDeviceID, "another-device-0001"))
	if rec.Code != http.StatusNoContent || r.got == nil || r.got.AccountID != "acc_1" {
		t.Fatalf("status = %d, grant = %+v; want a pass as acc_1", rec.Code, r.got)
	}
}

func TestTheSessionMiddlewareStillAcceptsAPlainGrant(t *testing.T) {
	r := newSessionRig(t)
	r.guestGrant("gt_guest")
	rec := r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_guest", grantauth.HeaderDeviceID: device})
	if rec.Code != http.StatusNoContent || r.got == nil || r.got.ViaSession {
		t.Fatalf("status = %d, grant = %+v; want a plain grant pass", rec.Code, r.got)
	}
	assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_guest"}), http.StatusBadRequest, grantauth.CodeDeviceIDRequired)
}

func TestRedisBeingDownOnTheSessionPathIsA503(t *testing.T) {
	r := newSessionRig(t)
	r.guestGrant("gt_guest")
	r.session("ps_1", "gt_guest")
	r.mr.SetError("connection refused")
	rec := r.call(t, map[string]string{grantauth.HeaderPaymentSession: "ps_1"})
	if rec.Code != http.StatusServiceUnavailable || r.got != nil {
		t.Fatalf("status = %d, reached handler = %v; want 503 and no pass", rec.Code, r.got != nil)
	}
}

func TestThePlainMiddlewareRefusesAPaymentSessionBeforeReadingRedis(t *testing.T) {
	r := newRig(t)
	r.guestGrant("gt_guest")
	r.session("ps_1", "gt_guest")
	r.mr.SetError("must not be called")
	for name, h := range map[string]map[string]string{
		"session only":      {grantauth.HeaderPaymentSession: "ps_1"},
		"session and grant": {grantauth.HeaderPaymentSession: "ps_1", grantauth.HeaderGrantToken: "gt_guest", grantauth.HeaderDeviceID: device},
	} {
		t.Run(name, func(t *testing.T) {
			assertError(t, r.call(t, h), http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}
