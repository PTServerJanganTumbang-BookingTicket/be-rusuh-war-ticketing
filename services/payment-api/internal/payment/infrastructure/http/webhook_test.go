package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
)

const notificationBody = `{
  "transaction_time": "2026-03-01 10:07:12",
  "transaction_status": "settlement",
  "transaction_id": "tx-1",
  "status_message": "midtrans payment notification",
  "status_code": "200",
  "signature_key": "deadbeef",
  "settlement_time": "2026-03-01 10:07:30",
  "payment_type": "bank_transfer",
  "order_id": "RB-00912_1",
  "merchant_id": "M-1",
  "gross_amount": "3000000.00",
  "fraud_status": "accept",
  "currency": "IDR",
  "va_numbers": [{"bank": "bca", "va_number": "8277081234567890"}]
}`

// The webhook is mounted WITHOUT the grant guard, because Midtrans holds no
// grant. If it were guarded, every real notification would be rejected and no
// booking would ever be confirmed.
func TestWebhook_NeedsNoGrant(t *testing.T) {
	uc := &fakeUsecase{}
	mux := newServer(t, uc, passthroughGuard())

	w := do(t, mux, http.MethodPost, "/payments/webhook", notificationBody)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, uc.notifyCalls)
}

func TestWebhook_DecodesEveryFieldAsString(t *testing.T) {
	uc := &fakeUsecase{}
	mux := newServer(t, uc, passthroughGuard())

	require.Equal(t, http.StatusOK, do(t, mux, http.MethodPost, "/payments/webhook", notificationBody).Code)

	n := uc.gotNotify
	assert.Equal(t, "RB-00912_1", n.OrderID)
	assert.Equal(t, "settlement", n.TransactionStatus)
	assert.Equal(t, "200", n.StatusCode)
	assert.Equal(t, "accept", n.FraudStatus)
	assert.Equal(t, "deadbeef", n.SignatureKey)
	// gross_amount must survive decoding byte for byte: it goes into the
	// signature exactly as received, and "3000000" would never match.
	assert.Equal(t, "3000000.00", n.GrossAmount)
	require.Len(t, n.VANumbers, 1)
	assert.Equal(t, "8277081234567890", n.VANumbers[0].VANumber)
}

// Every outcome is a 200. Midtrans retries any non-2xx, so a 409 for an
// already-applied notification would turn one event into a retry storm during
// the busiest minutes, and a 401 for a bad signature would hand a prober a
// free oracle.
func TestWebhook_AlwaysAnswers200(t *testing.T) {
	cases := map[string]error{
		"applied":            nil,
		"signature mismatch": errors.New("payment: signature mismatch"),
		"duplicate":          errors.New("payment: duplicate gateway event"),
		"unknown booking":    errors.New("payment: booking not found"),
		"database down":      errors.New("postgres: connection reset"),
	}
	for name, ucErr := range cases {
		t.Run(name, func(t *testing.T) {
			uc := &fakeUsecase{notifyFn: func(context.Context, dto.Notification) error { return ucErr }}
			mux := newServer(t, uc, passthroughGuard())

			w := do(t, mux, http.MethodPost, "/payments/webhook", notificationBody)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, http.StatusOK, decode(t, w).Meta.Code)
		})
	}
}

// Even a body that cannot be parsed gets a 200: retrying it will not make it
// parseable, and the use case is never called with a half-decoded payload.
func TestWebhook_UnparseableBody_Is200AndNotProcessed(t *testing.T) {
	uc := &fakeUsecase{}
	mux := newServer(t, uc, passthroughGuard())

	w := do(t, mux, http.MethodPost, "/payments/webhook", `{"order_id":`)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, uc.notifyCalls)
}

// The acknowledgement carries no detail about what happened. An outsider
// cannot tell an accepted notification from an ignored one, which is the
// point.
func TestWebhook_ResponseRevealsNothing(t *testing.T) {
	uc := &fakeUsecase{notifyFn: func(context.Context, dto.Notification) error {
		return errors.New("payment: signature mismatch")
	}}
	mux := newServer(t, uc, passthroughGuard())

	bad := do(t, mux, http.MethodPost, "/payments/webhook", notificationBody)

	uc2 := &fakeUsecase{}
	good := do(t, newServer(t, uc2, passthroughGuard()), http.MethodPost, "/payments/webhook", notificationBody)

	assert.Equal(t, good.Code, bad.Code)
	assert.Equal(t, decode(t, good).Meta.Message, decode(t, bad).Meta.Message)
	assert.NotContains(t, decode(t, bad).Meta.Message, "signature")
}

// POST /payments/webhook must not be routed as POST /payments/{booking_id}
// with booking_id="webhook". Go's ServeMux prefers the literal segment, the
// same way API Gateway does.
func TestWebhook_LiteralRouteBeatsPathVariable(t *testing.T) {
	uc := &fakeUsecase{}
	mux := newServer(t, uc, testGuard(testGrant))

	w := do(t, mux, http.MethodPost, "/payments/webhook", notificationBody)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, uc.notifyCalls)
	// The initiate handler was not reached, so no booking lookup happened for
	// an id of "webhook".
	assert.Empty(t, uc.gotBookingID)
}

// ── CIDR allowlist tests ──────────────────────────────────────────────────────
// Test IPs below are from RFC 5737 documentation ranges (192.0.2.0/24,
// 198.51.100.0/24, 203.0.113.0/24). Do NOT use these in production allowlists;
// they are reserved for documentation examples.

// mustParsePrefix parses a CIDR string or panics. For tests only.
func mustParsePrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		// Try as single IP
		addr, err2 := netip.ParseAddr(s)
		if err2 != nil {
			panic("invalid prefix: " + s)
		}
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		return netip.PrefixFrom(addr, bits)
	}
	return p
}

// newServerWithAllowlist creates a test server with a specific IP allowlist.
func newServerWithAllowlist(t *testing.T, uc *fakeUsecase, allowlist []netip.Prefix) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewPayment(uc, allowlist), passthroughGuard())
	return mux
}

// doFromIP makes a request with a specific RemoteAddr.
func doFromIP(t *testing.T, mux *http.ServeMux, method, target, body, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestWebhook_CIDRAllowlist_ExactIPMatch(t *testing.T) {
	uc := &fakeUsecase{}
	allowlist := []netip.Prefix{mustParsePrefix("192.0.2.1/32")}
	mux := newServerWithAllowlist(t, uc, allowlist)

	w := doFromIP(t, mux, http.MethodPost, "/payments/webhook", notificationBody, "192.0.2.1:54321")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, uc.notifyCalls, "should process webhook from allowlisted IP")
}

func TestWebhook_CIDRAllowlist_BlocksNonAllowlistedIP(t *testing.T) {
	uc := &fakeUsecase{}
	allowlist := []netip.Prefix{mustParsePrefix("192.0.2.1/32")}
	mux := newServerWithAllowlist(t, uc, allowlist)

	w := doFromIP(t, mux, http.MethodPost, "/payments/webhook", notificationBody, "203.0.113.1:54321")

	// Still returns 200 (we don't reveal the block)
	assert.Equal(t, http.StatusOK, w.Code)
	// But the use case was NOT called
	assert.Equal(t, 0, uc.notifyCalls, "should NOT process webhook from non-allowlisted IP")
}

// This is the critical test case from the task list: CIDR range must allow
// IPs within the range, not just exact matches.
func TestWebhook_CIDRAllowlist_RangeContainsIP(t *testing.T) {
	uc := &fakeUsecase{}
	// A /24 range: 198.51.100.0 - 198.51.100.255 (RFC 5737 TEST-NET-2)
	allowlist := []netip.Prefix{mustParsePrefix("198.51.100.0/24")}
	mux := newServerWithAllowlist(t, uc, allowlist)

	// IP 198.51.100.5 is WITHIN the 198.51.100.0/24 range
	w := doFromIP(t, mux, http.MethodPost, "/payments/webhook", notificationBody, "198.51.100.5:54321")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, uc.notifyCalls, "198.51.100.5 should be allowed by 198.51.100.0/24")
}

func TestWebhook_CIDRAllowlist_RangeEdgeCases(t *testing.T) {
	// A /24 range: 198.51.100.0 - 198.51.100.255 (RFC 5737 TEST-NET-2)
	allowlist := []netip.Prefix{mustParsePrefix("198.51.100.0/24")}

	cases := []struct {
		name    string
		ip      string
		allowed bool
	}{
		{"first IP in range", "198.51.100.0", true},
		{"last IP in range", "198.51.100.255", true},
		{"middle of range", "198.51.100.128", true},
		{"just before range", "198.51.99.255", false},
		{"just after range", "198.51.101.0", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeUsecase{}
			mux := newServerWithAllowlist(t, uc, allowlist)

			w := doFromIP(t, mux, http.MethodPost, "/payments/webhook", notificationBody, tc.ip+":54321")

			assert.Equal(t, http.StatusOK, w.Code)
			if tc.allowed {
				assert.Equal(t, 1, uc.notifyCalls, "IP %s should be allowed", tc.ip)
			} else {
				assert.Equal(t, 0, uc.notifyCalls, "IP %s should be blocked", tc.ip)
			}
		})
	}
}

func TestWebhook_CIDRAllowlist_MultipleRanges(t *testing.T) {
	// Multiple ranges using RFC 5737 documentation IPs
	allowlist := []netip.Prefix{
		mustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
		mustParsePrefix("198.51.100.5/32"), // single IP in TEST-NET-2
		mustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	}

	cases := []struct {
		name    string
		ip      string
		allowed bool
	}{
		{"in first range", "192.0.2.50", true},
		{"exact match second", "198.51.100.5", true},
		{"in third range", "203.0.113.200", true},
		{"not in any range", "10.0.0.1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeUsecase{}
			mux := newServerWithAllowlist(t, uc, allowlist)

			w := doFromIP(t, mux, http.MethodPost, "/payments/webhook", notificationBody, tc.ip+":54321")

			assert.Equal(t, http.StatusOK, w.Code)
			if tc.allowed {
				assert.Equal(t, 1, uc.notifyCalls, "IP %s should be allowed", tc.ip)
			} else {
				assert.Equal(t, 0, uc.notifyCalls, "IP %s should be blocked", tc.ip)
			}
		})
	}
}

func TestWebhook_CIDRAllowlist_EmptyAllowlistAllowsAll(t *testing.T) {
	uc := &fakeUsecase{}
	// Empty allowlist (local dev mode)
	mux := newServerWithAllowlist(t, uc, nil)

	w := doFromIP(t, mux, http.MethodPost, "/payments/webhook", notificationBody, "203.0.113.1:54321")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, uc.notifyCalls, "empty allowlist should allow all IPs")
}

func TestWebhook_CIDRAllowlist_InvalidIPRejected(t *testing.T) {
	uc := &fakeUsecase{}
	allowlist := []netip.Prefix{mustParsePrefix("198.51.100.0/24")}
	mux := newServerWithAllowlist(t, uc, allowlist)

	// Invalid IP format
	w := doFromIP(t, mux, http.MethodPost, "/payments/webhook", notificationBody, "not-an-ip:54321")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, uc.notifyCalls, "invalid IP should be blocked")
}

// F9: IPv4-mapped IPv6 addresses (::ffff:1.2.3.4) must match IPv4 allowlists.
// API Gateway normally supplies a plain dotted quad, but the RemoteAddr fallback
// can produce a mapped form — and a silent rejection is a legitimate caller blocked.
func TestWebhook_CIDRAllowlist_IPv4MappedIPv6(t *testing.T) {
	uc := &fakeUsecase{}
	// Allowlist has IPv4 CIDR (RFC 5737 TEST-NET-2)
	allowlist := []netip.Prefix{mustParsePrefix("198.51.100.0/24")}
	mux := newServerWithAllowlist(t, uc, allowlist)

	// IPv4-mapped IPv6 representation of 198.51.100.5
	w := doFromIP(t, mux, http.MethodPost, "/payments/webhook", notificationBody, "[::ffff:198.51.100.5]:54321")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, uc.notifyCalls, "::ffff:198.51.100.5 should match 198.51.100.0/24 after Unmap()")
}
