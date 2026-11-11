package midtrans

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

const serverKey = "SB-Mid-server-test"

// capture records what the client actually put on the wire, which is where
// every one of these details either holds or silently stops holding.
type capture struct {
	method string
	path   string
	auth   string
	accept string
	ctype  string
	body   map[string]any
}

func newStub(t *testing.T, status int, response string) (*Client, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.auth = r.Header.Get("Authorization")
		cap.accept = r.Header.Get("Accept")
		cap.ctype = r.Header.Get("Content-Type")
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &cap.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, serverKey, 5*time.Second), cap
}

func heldAt() time.Time {
	// 10:15 UTC is 17:15 WIB, which is what custom_expiry.order_time must say.
	return time.Date(2026, 3, 1, 10, 15, 0, 0, time.UTC)
}

func vaRequest() portout.ChargeRequest {
	return portout.ChargeRequest{
		OrderID:       "RB-00912:1",
		GrossAmount:   3000000,
		PaymentType:   entity.MethodBankTransfer,
		Bank:          entity.BankBCA,
		HeldAt:        heldAt(),
		ExpirySeconds: 600,
	}
}

const vaResponse = `{
  "status_code": "201",
  "status_message": "Success, Bank Transfer transaction is created",
  "transaction_id": "tx-1",
  "order_id": "RB-00912:1",
  "gross_amount": "3000000.00",
  "payment_type": "bank_transfer",
  "transaction_status": "pending",
  "va_numbers": [{"bank": "bca", "va_number": "8277081234567890"}]
}`

// Basic auth with the server key as the username and an EMPTY password. The
// trailing colon is required: without it Midtrans rejects every call as
// unauthorised.
func TestAuthHeader_IsBasicWithTrailingColon(t *testing.T) {
	c, cap := newStub(t, 201, vaResponse)

	_, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(cap.auth, "Basic "))
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(cap.auth, "Basic "))
	require.NoError(t, err)
	assert.Equal(t, serverKey+":", string(decoded))
}

func TestCharge_PostsToV2Charge(t *testing.T) {
	c, cap := newStub(t, 201, vaResponse)

	_, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, cap.method)
	assert.Equal(t, "/v2/charge", cap.path)
	assert.Equal(t, "application/json", cap.ctype)
	assert.Equal(t, "application/json", cap.accept)
}

// custom_expiry is on EVERY charge. Without it a virtual account defaults to
// 24 hours at the gateway and stays payable long after the sweep released the
// seats and sold them to someone else.
func TestCharge_AlwaysSendsCustomExpiry(t *testing.T) {
	c, cap := newStub(t, 201, vaResponse)

	_, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)

	exp, ok := cap.body["custom_expiry"].(map[string]any)
	require.True(t, ok, "custom_expiry missing from charge body")
	// Seconds, not minutes. Midtrans accepts second/minute/hour/day, and
	// minutes would truncate a window that is not a whole number of them.
	assert.Equal(t, float64(600), exp["expiry_duration"])
	assert.Equal(t, "second", exp["unit"])
}

// A window that is not a whole number of minutes must reach the gateway
// intact. In minutes this would have been sent as 7, and Midtrans would have
// refused payment with thirty seconds still on the customer's clock.
func TestCharge_SubMinuteWindowIsSentExactly(t *testing.T) {
	c, cap := newStub(t, 201, vaResponse)

	req := vaRequest()
	req.ExpirySeconds = 450 // 7m30s
	_, err := c.Charge(context.Background(), req)
	require.NoError(t, err)

	exp := cap.body["custom_expiry"].(map[string]any)
	assert.Equal(t, float64(450), exp["expiry_duration"])
	assert.Equal(t, "second", exp["unit"])
}

// expiry_time is the only evidence custom_expiry was honoured at all. Parsed in
// WIB, like every other Midtrans timestamp: read as UTC it would land seven
// hours out and every charge would look like a mismatch.
func TestCharge_ParsesGatewayExpiryInWIB(t *testing.T) {
	// 17:25 WIB = 10:25 UTC, which is held_at (10:15 UTC) + the 10 minutes
	// requested. This is the matching case.
	const resp = `{
  "status_code": "201",
  "order_id": "RB-00912:1",
  "gross_amount": "3000000.00",
  "payment_type": "bank_transfer",
  "transaction_status": "pending",
  "va_numbers": [{"bank": "bca", "va_number": "8801234567890"}],
  "expiry_time": "2026-03-01 17:25:00"
}`
	c, _ := newStub(t, 201, resp)

	out, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)
	require.NotNil(t, out.GatewayExpiry, "expiry_time must be parsed, not discarded")
	assert.True(t, out.GatewayExpiry.Equal(time.Date(2026, 3, 1, 10, 25, 0, 0, time.UTC)),
		"expected 10:25 UTC (17:25 WIB), got %s", out.GatewayExpiry)
}

// Midtrans clamping or ignoring custom_expiry is the failure this exists to
// surface: the charge answers 201, we promise ten minutes, and the VA stays
// payable for the 24-hour default long after the sweep resold the seats. The
// charge must still SUCCEED - the instrument exists and the customer can pay
// it, so refusing here would discard a live payment over a reporting gap.
func TestCharge_GatewayExpiryMismatch_IsReportedNotFatal(t *testing.T) {
	// 24 hours out: custom_expiry ignored entirely.
	const resp = `{
  "status_code": "201",
  "order_id": "RB-00912:1",
  "gross_amount": "3000000.00",
  "payment_type": "bank_transfer",
  "transaction_status": "pending",
  "va_numbers": [{"bank": "bca", "va_number": "8801234567890"}],
  "expiry_time": "2026-03-02 17:15:00"
}`
	c, _ := newStub(t, 201, resp)

	out, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err, "a deadline discrepancy must not fail the charge")
	assert.Equal(t, "8801234567890", out.Instructions["va_number"])
	require.NotNil(t, out.GatewayExpiry)
	assert.True(t, out.GatewayExpiry.After(heldAt().Add(time.Hour)),
		"the mismatching value must still be reported for the operator")
}

// A response without expiry_time is not an error: several of Midtrans's own
// documented samples omit it.
func TestCharge_NoExpiryTime_IsNotAnError(t *testing.T) {
	c, _ := newStub(t, 201, vaResponse)

	out, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)
	assert.Nil(t, out.GatewayExpiry)
}

// An unparseable value is logged and dropped rather than failing the charge,
// for the same reason as a mismatch.
func TestCharge_UnparseableExpiryTime_IsNotAnError(t *testing.T) {
	const resp = `{
  "status_code": "201",
  "order_id": "RB-00912:1",
  "gross_amount": "3000000.00",
  "payment_type": "bank_transfer",
  "transaction_status": "pending",
  "expiry_time": "not-a-timestamp"
}`
	c, _ := newStub(t, 201, resp)

	out, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)
	assert.Nil(t, out.GatewayExpiry)
}

// order_time is held_at in WIB, not the request time and not UTC. The zone is
// fixed in code rather than loaded from tzdata, because a Lambda image without
// zoneinfo would fall back to UTC and shift every deadline by seven hours.
func TestCharge_OrderTimeIsHeldAtInWIB(t *testing.T) {
	c, cap := newStub(t, 201, vaResponse)

	_, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)

	exp := cap.body["custom_expiry"].(map[string]any)
	assert.Equal(t, "2026-03-01 17:15:00 +0700", exp["order_time"])
}

// gross_amount is a NUMBER on the charge request. It comes back as a decimal
// string on the notification, and that string is what the signature uses - the
// two are not interchangeable.
func TestCharge_GrossAmountIsNumericOnRequest(t *testing.T) {
	c, cap := newStub(t, 201, vaResponse)

	_, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)

	td := cap.body["transaction_details"].(map[string]any)
	assert.Equal(t, float64(3000000), td["gross_amount"])
	assert.Equal(t, "RB-00912:1", td["order_id"])
}

func TestCharge_BankTransfer_CarriesBank(t *testing.T) {
	c, cap := newStub(t, 201, vaResponse)

	res, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)

	bt := cap.body["bank_transfer"].(map[string]any)
	assert.Equal(t, entity.BankBCA, bt["bank"])
	assert.Equal(t, "bca", res.Instructions["bank"])
	assert.Equal(t, "8277081234567890", res.Instructions["va_number"])
	assert.Equal(t, "pending", res.TransactionStatus)
	assert.Equal(t, "201", res.StatusCode)
	assert.NotEmpty(t, res.Raw)
}

// GoPay carries no bank_transfer block at all. Sending an empty one would make
// Midtrans reject the charge.
func TestCharge_GoPay_OmitsBankTransfer(t *testing.T) {
	const gopayResponse = `{
      "status_code": "201",
      "transaction_status": "pending",
      "payment_type": "gopay",
      "actions": [
        {"name": "generate-qr-code", "url": "https://api.sandbox.midtrans.com/v2/gopay/qr/1"},
        {"name": "deeplink-redirect", "url": "https://simulator.sandbox.midtrans.com/gopay/ui/1"},
        {"name": "get-status", "url": "https://api.sandbox.midtrans.com/v2/1/status"}
      ]
    }`
	c, cap := newStub(t, 201, gopayResponse)

	res, err := c.Charge(context.Background(), portout.ChargeRequest{
		OrderID: "RB-00912:1", GrossAmount: 3000000,
		PaymentType: entity.MethodGoPay, HeldAt: heldAt(), ExpirySeconds: 600,
	})
	require.NoError(t, err)

	_, hasBank := cap.body["bank_transfer"]
	assert.False(t, hasBank)
	assert.Equal(t, "https://api.sandbox.midtrans.com/v2/gopay/qr/1", res.Instructions["qr_url"])
	assert.Equal(t, "https://simulator.sandbox.midtrans.com/gopay/ui/1", res.Instructions["deeplink_url"])
	// get-status is Midtrans's own polling URL and is not an instruction for
	// the customer, so it must not leak into the response.
	assert.NotContains(t, res.Instructions, "get-status")
	assert.Len(t, res.Instructions, 2)
}

// A 402 with a status_code in the body is a business outcome, not a transport
// failure: the gateway answered, and it said no. Treating it as an error would
// make the use case report 503 and the breaker count a failure for a perfectly
// healthy gateway.
func TestCharge_DeclinedWithStatusCode_IsAnAnswerNotAnError(t *testing.T) {
	const declined = `{
      "status_code": "402",
      "status_message": "Transaction is declined",
      "transaction_status": "deny",
      "order_id": "RB-00912:1"
    }`
	c, _ := newStub(t, 402, declined)

	res, err := c.Charge(context.Background(), vaRequest())
	require.NoError(t, err)
	assert.Equal(t, "deny", res.TransactionStatus)
	assert.Equal(t, "402", res.StatusCode)
	assert.True(t, res.Declined())
}

// A 4xx with no status_code is not a Midtrans business response - it is a
// gateway, a proxy or a misconfiguration, and the caller must not mistake it
// for a decline.
func TestCharge_4xxWithoutStatusCode_IsAnError(t *testing.T) {
	c, _ := newStub(t, 403, `<html>Forbidden</html>`)

	_, err := c.Charge(context.Background(), vaRequest())
	require.Error(t, err)
}

// A 5xx is always an error: there is no business meaning to read out of it,
// and the breaker needs to see the failure.
func TestCharge_5xx_IsAnError(t *testing.T) {
	c, _ := newStub(t, 503, `{"status_code":"503"}`)

	_, err := c.Charge(context.Background(), vaRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream 503")
}

// A timeout must surface as an error WITHOUT any result: the instrument may
// well exist at the gateway, so the caller's rule is "do not re-send".
func TestCharge_Timeout_IsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, serverKey, 20*time.Millisecond)

	res, err := c.Charge(context.Background(), vaRequest())
	require.Error(t, err)
	assert.Empty(t, res.Instructions)
	assert.Empty(t, res.TransactionStatus)
}

func TestCancel_PostsToCancelNotExpire(t *testing.T) {
	c, cap := newStub(t, 200, `{"status_code":"200","transaction_status":"cancel"}`)

	require.NoError(t, c.Cancel(context.Background(), "RB-00912:1"))
	assert.Equal(t, http.MethodPost, cap.method)
	assert.Equal(t, "/v2/RB-00912:1/cancel", cap.path)
}

// Midtrans refusing to cancel a transaction that is already paid is a real
// outcome: §5.3 answers 409 PAYMENT_ALREADY_SETTLED and changes nothing, and
// the webhook confirms the booking normally.
func TestCancel_AlreadyPaid_ReportsAlreadySettled(t *testing.T) {
	for _, status := range []string{"settlement", "capture"} {
		t.Run(status, func(t *testing.T) {
			c, _ := newStub(t, 412, `{"status_code":"412","transaction_status":"`+status+`"}`)
			err := c.Cancel(context.Background(), "RB-00912:1")
			assert.ErrorIs(t, err, portout.ErrAlreadySettled)
		})
	}
}

func TestExpire_PostsToExpire(t *testing.T) {
	c, cap := newStub(t, 200, `{"status_code":"200","transaction_status":"expire"}`)

	require.NoError(t, c.Expire(context.Background(), "RB-00912:1"))
	assert.Equal(t, http.MethodPost, cap.method)
	assert.Equal(t, "/v2/RB-00912:1/expire", cap.path)
}

// Status is the only way the reconciler learns the fate of a charge that timed
// out, so it is a GET and carries no body.
func TestStatus_IsAReadOnlyGet(t *testing.T) {
	c, cap := newStub(t, 200, `{
      "status_code": "200",
      "transaction_status": "settlement",
      "fraud_status": "accept",
      "gross_amount": "3000000.00"
    }`)

	res, err := c.Status(context.Background(), "RB-00912:1")
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, cap.method)
	assert.Equal(t, "/v2/RB-00912:1/status", cap.path)
	assert.Empty(t, cap.body)
	assert.Equal(t, "settlement", res.TransactionStatus)
	assert.Equal(t, "accept", res.FraudStatus)
	// Kept as the string Midtrans sent, because it is what a signature would
	// be recomputed from.
	assert.Equal(t, "3000000.00", res.GrossAmount)
	assert.NotEmpty(t, res.Raw)
}

func TestStatus_UndecodableBody_IsAnError(t *testing.T) {
	c, _ := newStub(t, 200, `not json`)

	_, err := c.Status(context.Background(), "RB-00912:1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode status")
}

func TestCarriesStatusCode(t *testing.T) {
	assert.True(t, carriesStatusCode([]byte(`{"status_code":"402"}`)))
	assert.False(t, carriesStatusCode([]byte(`{"status_code":""}`)))
	// A non-numeric status_code is not Midtrans's: its codes are always
	// numeric strings, so anything else is some other service answering.
	assert.False(t, carriesStatusCode([]byte(`{"status_code":"forbidden"}`)))
	assert.False(t, carriesStatusCode([]byte(`<html></html>`)))
	assert.False(t, carriesStatusCode([]byte(`{}`)))
}

func TestChargeResult_Declined(t *testing.T) {
	for _, s := range []string{"deny", "cancel", "failure"} {
		assert.True(t, portout.ChargeResult{TransactionStatus: s}.Declined(), s)
	}
	for _, s := range []string{"pending", "settlement", "capture", "expire", ""} {
		assert.False(t, portout.ChargeResult{TransactionStatus: s}.Declined(), s)
	}
}
