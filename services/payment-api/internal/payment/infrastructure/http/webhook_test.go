package http

import (
	"context"
	"errors"
	"net/http"
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
  "order_id": "RB-00912:1",
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
	assert.Equal(t, "RB-00912:1", n.OrderID)
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
