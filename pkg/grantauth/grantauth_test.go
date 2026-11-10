package grantauth

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
)

const testPepper = "test-device-pepper"

type fakeStore struct {
	fields map[string]string
	err    error
	calls  int
	gotTok string
}

func (s *fakeStore) Grant(_ context.Context, token string) (map[string]string, error) {
	s.calls++
	s.gotTok = token
	if s.err != nil {
		return nil, s.err
	}
	return s.fields, nil
}

func accountGrant() map[string]string {
	return map[string]string{
		FieldEventID:    "EV-1",
		FieldBookingID:  "RB-00912",
		FieldQueueToken: "qt-1",
		FieldAccountID:  "ACC-1",
	}
}

func guestGrant(deviceID string) map[string]string {
	return map[string]string{
		FieldEventID:    "EV-1",
		FieldBookingID:  "RB-00912",
		FieldQueueToken: "qt-1",
		FieldDeviceHash: DeviceHash(testPepper, deviceID),
	}
}

func newVerifier(t *testing.T, store Store) *Verifier {
	t.Helper()
	v, err := NewVerifier(store, testPepper)
	require.NoError(t, err)
	return v
}

func assertAppError(t *testing.T, err error, status int, code string) {
	t.Helper()
	require.Error(t, err)
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr), "expected *apperror.AppError, got %T", err)
	assert.Equal(t, status, appErr.HTTPStatus)
	assert.Equal(t, code, appErr.ErrorCode)
}

// ── NewVerifier ───────────────────────────────────────────────────────────────

// Without the pepper the device check cannot run, so every guest would be
// rejected while the service reported itself healthy. Refusing to build is the
// only safe behaviour.
func TestNewVerifier_RequiresPepper(t *testing.T) {
	_, err := NewVerifier(&fakeStore{}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEVICE_PEPPER")
}

func TestNewVerifier_RequiresStore(t *testing.T) {
	_, err := NewVerifier(nil, testPepper)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store")
}

// ── DeviceHash ────────────────────────────────────────────────────────────────

// The raw device id is never stored: it links one device's visits, so a Redis
// dump must not yield a cross-event correlator.
func TestDeviceHash_IsHexHMACSHA256(t *testing.T) {
	got := DeviceHash(testPepper, "device-1")
	assert.Len(t, got, 64)
	_, err := hex.DecodeString(got)
	require.NoError(t, err)
	assert.Equal(t, strings.ToLower(got), got)
	assert.NotContains(t, got, "device-1")
}

func TestDeviceHash_IsStableAndKeyed(t *testing.T) {
	assert.Equal(t, DeviceHash(testPepper, "device-1"), DeviceHash(testPepper, "device-1"))
	assert.NotEqual(t, DeviceHash(testPepper, "device-1"), DeviceHash(testPepper, "device-2"))
	// Keyed by the pepper, which is what makes a rotation invalidate every
	// live binding at once - and why it may only happen between sales.
	assert.NotEqual(t, DeviceHash(testPepper, "device-1"), DeviceHash("other-pepper", "device-1"))
}

// ── Verify: token ─────────────────────────────────────────────────────────────

// A missing header is a distinct code from an invalid one: the client's fix is
// different, and the store is not consulted at all.
func TestVerify_MissingHeader_IsGrantTokenMissing(t *testing.T) {
	store := &fakeStore{}

	_, err := newVerifier(t, store).Verify(context.Background(), "", "device-1")
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeGrantTokenMissing)
	assert.Equal(t, 0, store.calls)
}

// Unknown, expired and revoked are one answer: telling them apart maps which
// grants are live.
func TestVerify_UnknownGrant_IsGrantTokenInvalid(t *testing.T) {
	store := &fakeStore{err: ErrNotFound}

	_, err := newVerifier(t, store).Verify(context.Background(), "tok-1", "device-1")
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeGrantTokenInvalid)
}

// The token is resolved BEFORE the device is considered. A device check
// against an unknown grant would answer DEVICE_MISMATCH and so confirm that
// some other grant exists for that device.
func TestVerify_UnknownGrantNeverReportsDeviceMismatch(t *testing.T) {
	store := &fakeStore{err: ErrNotFound}

	// No device id at all, and a wrong one: both must read as an invalid
	// grant, never as a device problem.
	for _, device := range []string{"", "someone-elses-device"} {
		_, err := newVerifier(t, store).Verify(context.Background(), "tok-1", device)
		assertAppError(t, err, http.StatusUnauthorized, apperror.CodeGrantTokenInvalid)
	}
}

// A broken Redis is NOT an invalid grant. Answering 401 would log customers
// out of a paid-for seat during an infrastructure blip.
func TestVerify_StoreFailure_Is503NotUnauthorized(t *testing.T) {
	store := &fakeStore{err: errors.New("dial tcp: connection refused")}

	_, err := newVerifier(t, store).Verify(context.Background(), "tok-1", "device-1")
	require.Error(t, err)

	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPStatus)
	assert.NotEqual(t, apperror.CodeGrantTokenInvalid, appErr.ErrorCode)
	// The driver's message must not reach the client.
	assert.NotContains(t, appErr.Message, "connection refused")
}

// booking_id is what the payment and booking endpoints bind on. A hash without
// it was written by a producer that predates ADR-006 §8.13 - failing closed
// beats letting the request match a NULL column.
func TestVerify_GrantWithoutBookingID_IsInvalid(t *testing.T) {
	fields := accountGrant()
	delete(fields, FieldBookingID)
	store := &fakeStore{fields: fields}

	_, err := newVerifier(t, store).Verify(context.Background(), "tok-1", "")
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeGrantTokenInvalid)
}

// An empty map would be indistinguishable from a grant with no fields, which
// is exactly why the store contract says to return ErrNotFound. Belt and
// braces: an empty map still fails closed here.
func TestVerify_EmptyHash_IsInvalid(t *testing.T) {
	store := &fakeStore{fields: map[string]string{}}

	_, err := newVerifier(t, store).Verify(context.Background(), "tok-1", "device-1")
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeGrantTokenInvalid)
}

// ── Verify: account grants ────────────────────────────────────────────────────

// Account-bound grants are valid from ANY device: the account is what binds
// them, and on §4/§5 no Authorization header is sent to compare against.
func TestVerify_AccountGrant_IsValidFromAnyDevice(t *testing.T) {
	store := &fakeStore{fields: accountGrant()}
	v := newVerifier(t, store)

	for _, device := range []string{"", "device-1", "a-completely-different-device"} {
		g, err := v.Verify(context.Background(), "tok-1", device)
		require.NoError(t, err, device)
		assert.Equal(t, "tok-1", g.Token)
		assert.Equal(t, "RB-00912", g.BookingID)
		assert.Equal(t, "EV-1", g.EventID)
		assert.Equal(t, "qt-1", g.QueueToken)
		assert.Equal(t, "ACC-1", g.AccountID)
		assert.False(t, g.IsGuest())
	}
}

// The raw token travels onward: repositories match it against
// bookings.grant_token, so it is the ownership key and not just a credential.
func TestVerify_CarriesTheRawTokenOnward(t *testing.T) {
	store := &fakeStore{fields: accountGrant()}

	g, err := newVerifier(t, store).Verify(context.Background(), "tok-abc", "")
	require.NoError(t, err)
	assert.Equal(t, "tok-abc", g.Token)
	assert.Equal(t, "tok-abc", store.gotTok)
}

// ── Verify: guest grants ──────────────────────────────────────────────────────

func TestVerify_GuestGrant_MatchingDevice_IsValid(t *testing.T) {
	store := &fakeStore{fields: guestGrant("device-1")}

	g, err := newVerifier(t, store).Verify(context.Background(), "tok-1", "device-1")
	require.NoError(t, err)
	assert.True(t, g.IsGuest())
	assert.Empty(t, g.AccountID)
	assert.Equal(t, DeviceHash(testPepper, "device-1"), g.DeviceHash)
}

func TestVerify_GuestGrant_WrongDevice_IsDeviceMismatch(t *testing.T) {
	store := &fakeStore{fields: guestGrant("device-1")}

	_, err := newVerifier(t, store).Verify(context.Background(), "tok-1", "device-2")
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeDeviceMismatch)
}

// A guest with no X-Device-Id cannot be checked at all. That is a 400 with its
// own code rather than a 401: the client omitted a required header, which is a
// different fix from "your grant is wrong".
func TestVerify_GuestGrant_NoDeviceID_IsDeviceIDRequired(t *testing.T) {
	store := &fakeStore{fields: guestGrant("device-1")}

	_, err := newVerifier(t, store).Verify(context.Background(), "tok-1", "")
	assertAppError(t, err, http.StatusBadRequest, apperror.CodeDeviceIDRequired)
}

// Rotating the pepper invalidates every live binding at once, which is why it
// may only happen between sales.
func TestVerify_GuestGrant_RotatedPepper_FailsEveryBinding(t *testing.T) {
	store := &fakeStore{fields: guestGrant("device-1")}
	v, err := NewVerifier(store, "a-rotated-pepper")
	require.NoError(t, err)

	_, err = v.Verify(context.Background(), "tok-1", "device-1")
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeDeviceMismatch)
}

// A grant that is neither account- nor device-bound cannot be trusted as a
// guest grant: with no stored hash to compare, the comparison must not pass.
func TestVerify_GuestGrantWithNoDeviceHash_IsMismatch(t *testing.T) {
	fields := guestGrant("device-1")
	delete(fields, FieldDeviceHash)
	store := &fakeStore{fields: fields}

	_, err := newVerifier(t, store).Verify(context.Background(), "tok-1", "device-1")
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeDeviceMismatch)
}

// ── context ───────────────────────────────────────────────────────────────────

func TestContext_RoundTrip(t *testing.T) {
	g := Grant{Token: "tok-1", BookingID: "RB-00912"}

	got, ok := FromContext(NewContext(context.Background(), g))
	require.True(t, ok)
	assert.Equal(t, g, got)
}

// A handler reached without the guard must be able to tell: it is a wiring
// bug, and answering 401 would send the client off chasing its own headers.
func TestFromContext_Absent(t *testing.T) {
	_, ok := FromContext(context.Background())
	assert.False(t, ok)
}

// ── Middleware ────────────────────────────────────────────────────────────────

func TestMiddleware_PassesTheGrantToTheHandler(t *testing.T) {
	store := &fakeStore{fields: accountGrant()}
	var seen Grant
	var ok bool

	h := newVerifier(t, store).Middleware(func(w http.ResponseWriter, _ *http.Request, _ error) {
		t.Fatal("onError must not be called for a valid grant")
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, ok = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/payments/RB-00912/status", nil)
	r.Header.Set(HeaderGrantToken, "tok-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, ok)
	assert.Equal(t, "tok-1", seen.Token)
	assert.Equal(t, "RB-00912", seen.BookingID)
}

// The guard reads both headers itself, so handlers never touch them.
func TestMiddleware_ReadsBothHeaders(t *testing.T) {
	store := &fakeStore{fields: guestGrant("device-1")}
	reached := false

	h := newVerifier(t, store).Middleware(func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Fatalf("unexpected error: %v", err)
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	r := httptest.NewRequest(http.MethodPost, "/payments/RB-00912", nil)
	r.Header.Set(HeaderGrantToken, "tok-1")
	r.Header.Set(HeaderDeviceID, "device-1")
	h.ServeHTTP(httptest.NewRecorder(), r)

	assert.True(t, reached)
}

// A rejected request never reaches the handler, and the error is handed to the
// service's own renderer so this package stays free of a response envelope.
func TestMiddleware_RejectedRequestStopsThere(t *testing.T) {
	store := &fakeStore{err: ErrNotFound}
	var gotErr error

	h := newVerifier(t, store).Middleware(func(w http.ResponseWriter, _ *http.Request, err error) {
		gotErr = err
		w.WriteHeader(http.StatusUnauthorized)
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run for a rejected grant")
	}))

	r := httptest.NewRequest(http.MethodGet, "/payments/RB-00912/status", nil)
	r.Header.Set(HeaderGrantToken, "tok-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertAppError(t, gotErr, http.StatusUnauthorized, apperror.CodeGrantTokenInvalid)
}

// ── field names ───────────────────────────────────────────────────────────────

// The hash fields are fixed by ADR-006 §8.13(a). There is deliberately no
// "token" field - that is the key's own name - and no "rebinds" field, because
// §8.9 removed rebinding entirely.
func TestHashFieldNamesAreFixed(t *testing.T) {
	assert.Equal(t, "event_id", FieldEventID)
	assert.Equal(t, "booking_id", FieldBookingID)
	assert.Equal(t, "queue_token", FieldQueueToken)
	assert.Equal(t, "account_id", FieldAccountID)
	assert.Equal(t, "device_hash", FieldDeviceHash)
	assert.Equal(t, "X-Grant-Token", HeaderGrantToken)
	assert.Equal(t, "X-Device-Id", HeaderDeviceID)
}
