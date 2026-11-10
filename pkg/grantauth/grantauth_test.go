package grantauth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
)

var pepper = []byte("test-pepper")

const jwtSecret = "test-jwt-secret"

const device = "d3f1a9c4b70e42f88a1c5e6072b9d4af"

type rig struct {
	mr       *miniredis.Miniredis
	handler  http.Handler
	got      *grantauth.Grant
	sessions *token.Issuer
}

func newRig(t *testing.T) *rig { return newRigWith(t, issuer(t)) }

// newRigWith builds the middleware with a given session verifier, so one test
// can pass nil and check that the guard fails closed instead of skipping §8.17.
func newRigWith(t *testing.T, sessions *token.Issuer) *rig {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	r := &rig{mr: mr, sessions: sessions}
	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		g, ok := grantauth.FromContext(req.Context())
		if ok {
			r.got = &g
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.handler = grantauth.Middleware(rdb, grantauth.Config{DevicePepper: pepper, Sessions: sessions})(next)
	return r
}

func issuer(t *testing.T) *token.Issuer {
	t.Helper()
	iss, err := token.NewIssuer(jwtSecret, "rusuh-user", time.Hour)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return iss
}

// bearerFor mints a valid session for accountID, as §1.3 would.
func (r *rig) bearerFor(t *testing.T, accountID string) string {
	t.Helper()
	raw, _, err := r.sessions.Issue(accountID, accountID+"@example.com", true)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return "Bearer " + raw
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

// No X-Device-Id, because the account is what binds an account grant
// (ADR-006 §8.9) - but the session for that account is now required (§8.17).
func TestALoggedInGrantPassesWithItsOwnSessionAndNoDeviceID(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	rec := r.call(t, map[string]string{
		grantauth.HeaderGrantToken:    "gt_acc",
		grantauth.HeaderAuthorization: r.bearerFor(t, "acc_1"),
	})
	if rec.Code != http.StatusNoContent || r.got == nil || r.got.AccountID != "acc_1" || r.got.DeviceHash != "" {
		t.Fatalf("status = %d, grant = %+v; want a pass with account acc_1", rec.Code, r.got)
	}
}

// §8.17: a leaked or copied grant is not enough to buy in someone's name.
func TestALoggedInGrantWithoutASessionIsUnauthorized(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	assertError(t, r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_acc"}),
		http.StatusUnauthorized, grantauth.CodeUnauthorized)
	if r.got != nil {
		t.Fatal("the handler must not be reached")
	}
}

// Another account's session is perfectly valid and still wrong: the token is
// not the problem, the pairing is, so the client logs in as the right account
// rather than refreshing.
func TestAnotherAccountsSessionIsUnauthorized(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	assertError(t, r.call(t, map[string]string{
		grantauth.HeaderGrantToken:    "gt_acc",
		grantauth.HeaderAuthorization: r.bearerFor(t, "acc_someone_else"),
	}), http.StatusUnauthorized, grantauth.CodeUnauthorized)
}

func TestAnExpiredSessionIsTokenExpired(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	// A separate issuer whose tokens are born expired, so no clock is faked.
	past, err := token.NewIssuer(jwtSecret, "rusuh-user", time.Nanosecond)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	raw, _, err := past.Issue("acc_1", "a@example.com", true)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	assertError(t, r.call(t, map[string]string{
		grantauth.HeaderGrantToken:    "gt_acc",
		grantauth.HeaderAuthorization: "Bearer " + raw,
	}), http.StatusUnauthorized, apperror.CodeTokenExpired)
}

func TestAForgedOrMalformedSessionIsTokenInvalid(t *testing.T) {
	other, err := token.NewIssuer("a-different-signing-key", "rusuh-user", time.Hour)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	forged, _, err := other.Issue("acc_1", "a@example.com", true)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	for name, header := range map[string]string{
		"signed with another key": "Bearer " + forged,
		"not a jwt":               "Bearer not.a.jwt",
		"empty after the scheme":  "Bearer ",
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.accountGrant("gt_acc")
			rec := r.call(t, map[string]string{
				grantauth.HeaderGrantToken:    "gt_acc",
				grantauth.HeaderAuthorization: header,
			})
			// "Bearer " with nothing after it reads as no header at all.
			want := apperror.CodeTokenInvalid
			if header == "Bearer " {
				want = grantauth.CodeUnauthorized
			}
			assertError(t, rec, http.StatusUnauthorized, want)
		})
	}
}

// The scheme is case-insensitive (RFC 7235), and another scheme reads as absent.
func TestTheBearerSchemeIsCaseInsensitiveAndOtherSchemesDoNotCount(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	raw := r.bearerFor(t, "acc_1")[len("Bearer "):]
	rec := r.call(t, map[string]string{
		grantauth.HeaderGrantToken:    "gt_acc",
		grantauth.HeaderAuthorization: "bEaReR " + raw,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want the lower-case scheme accepted", rec.Code)
	}

	r2 := newRig(t)
	r2.accountGrant("gt_acc")
	assertError(t, r2.call(t, map[string]string{
		grantauth.HeaderGrantToken:    "gt_acc",
		grantauth.HeaderAuthorization: "Basic " + raw,
	}), http.StatusUnauthorized, grantauth.CodeUnauthorized)
}

// A guest grant ignores Authorization entirely (§8.17). A token sent beside one
// is not an error; it is simply not what binds the grant.
func TestAGuestGrantIgnoresAnyAuthorizationHeader(t *testing.T) {
	r := newRig(t)
	r.guestGrant("gt_guest")
	rec := r.call(t, map[string]string{
		grantauth.HeaderGrantToken:    "gt_guest",
		grantauth.HeaderDeviceID:      device,
		grantauth.HeaderAuthorization: r.bearerFor(t, "acc_someone_else"),
	})
	if rec.Code != http.StatusNoContent || r.got == nil {
		t.Fatalf("status = %d; want a guest grant to pass regardless of Authorization", rec.Code)
	}
}

// Fail closed. A guard that skips the check when misconfigured restores
// exactly the gap §8.17 closes, on the path where a leaked grant is worth most.
func TestWithoutASessionVerifierAnAccountGrantIs503NotAPass(t *testing.T) {
	r := newRigWith(t, nil)
	r.accountGrant("gt_acc")
	rec := r.call(t, map[string]string{grantauth.HeaderGrantToken: "gt_acc"})
	if rec.Code != http.StatusServiceUnavailable || r.got != nil {
		t.Fatalf("status = %d, reached handler = %v; want 503 and no pass", rec.Code, r.got != nil)
	}
}

// The session only CHECKS: attribution still comes from the grant hash, which
// admission-ctl wrote. v2.3 took it from the header and a stale header meant a
// booking attributed to the wrong account.
func TestTheGrantNotTheSessionCarriesTheAccountID(t *testing.T) {
	r := newRig(t)
	r.accountGrant("gt_acc")
	rec := r.call(t, map[string]string{
		grantauth.HeaderGrantToken:    "gt_acc",
		grantauth.HeaderAuthorization: r.bearerFor(t, "acc_1"),
	})
	if rec.Code != http.StatusNoContent || r.got == nil {
		t.Fatalf("status = %d; want a pass", rec.Code)
	}
	want := grantauth.Grant{Token: "gt_acc", EventID: "evt_a", BookingID: "RB-2", QueueToken: "qt_2", AccountID: "acc_1"}
	if *r.got != want {
		t.Fatalf("grant = %+v, want %+v", *r.got, want)
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
