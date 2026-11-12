package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
)

type fakeHandoff struct {
	createErr  error
	exchangeFn func(dto.ExchangeRequest) (dto.ExchangeResponse, error)
	gotBooking string
	gotGrant   string
	calls      int
}

func (f *fakeHandoff) CreateHandoff(_ context.Context, bookingID, grant string) (dto.HandoffResponse, error) {
	f.calls++
	f.gotBooking, f.gotGrant = bookingID, grant
	if f.createErr != nil {
		return dto.HandoffResponse{}, f.createErr
	}
	return dto.HandoffResponse{BookingID: bookingID, HandoffCode: "hc_x",
		PaymentURL: "https://rush-my-ticket.my.id/m/orders/" + bookingID + "/pay#code=hc_x", ExpiresAt: "2026-10-25T10:16:30Z"}, nil
}

func (f *fakeHandoff) ExchangeHandoff(_ context.Context, req dto.ExchangeRequest) (dto.ExchangeResponse, error) {
	f.calls++
	if f.exchangeFn != nil {
		return f.exchangeFn(req)
	}
	return dto.ExchangeResponse{BookingID: req.BookingID, PaymentSession: "ps_x", ExpiresAt: "2026-10-25T10:34:00Z"}, nil
}

func handoffServer(uc *fakeHandoff, guard func(http.Handler) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	RegisterHandoffRoutes(mux, NewHandoff(uc), guard)
	return mux
}

type handoffEnvelope struct {
	Meta struct {
		Code      int    `json:"code"`
		ErrorCode string `json:"error_code"`
	} `json:"meta"`
	Data map[string]any `json:"data"`
}

func decodeHandoff(t *testing.T, body []byte) handoffEnvelope {
	t.Helper()
	var e handoffEnvelope
	require.NoError(t, json.Unmarshal(body, &e), string(body))
	return e
}

func TestCreateHandoffAnswers201WithTheCodeForTheGrantsBooking(t *testing.T) {
	uc := &fakeHandoff{}
	w := do(t, handoffServer(uc, testGuard(grantauth.Grant{Token: "gt_1"})), http.MethodPost, "/payments/RB-00912/handoff", "")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, "RB-00912", uc.gotBooking)
	assert.Equal(t, "gt_1", uc.gotGrant)
	e := decodeHandoff(t, w.Body.Bytes())
	assert.Equal(t, "hc_x", e.Data["handoff_code"])
	assert.Equal(t, "RB-00912", e.Data["booking_id"])
	assert.Equal(t, "https://rush-my-ticket.my.id/m/orders/RB-00912/pay#code=hc_x", e.Data["payment_url"])
}

func TestCreateHandoffKeepsTheErrorDataTheAppRoutesOn(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		key    string
	}{
		"409 status": {apperror.ErrConflict("x").WithCode(apperror.CodeBookingStatusInvalid).WithData(map[string]any{"status": "SELECTING"}), http.StatusConflict, "status"},
		"409 window": {apperror.ErrConflict("x").WithCode(apperror.CodePaymentWindowClosed).WithData(map[string]any{"payment_expires_at": "2026-10-25T10:25:00Z"}), http.StatusConflict, "payment_expires_at"},
		"429":        {apperror.ErrTooManyRequests("x").WithCode(apperror.CodeTooManyRequests).WithRetryAfter(6 * time.Second).WithData(map[string]any{"retry_after": 6}), http.StatusTooManyRequests, "retry_after"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			uc := &fakeHandoff{createErr: c.err}
			w := do(t, handoffServer(uc, testGuard(grantauth.Grant{Token: "gt_1"})), http.MethodPost, "/payments/RB-1/handoff", "")
			require.Equal(t, c.status, w.Code)
			assert.Contains(t, decodeHandoff(t, w.Body.Bytes()).Data, c.key, "data must survive the writer")
			if c.status == http.StatusTooManyRequests {
				assert.Equal(t, "6", w.Header().Get("Retry-After"))
			}
		})
	}
}

func TestCreateHandoffWithoutTheGuardIsAWiringBugNotA401(t *testing.T) {
	uc := &fakeHandoff{}
	w := do(t, handoffServer(uc, passthroughGuard()), http.MethodPost, "/payments/RB-1/handoff", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Zero(t, uc.calls)
}

func TestExchangeNeedsNoGuardAndAnswers200WithTheSession(t *testing.T) {
	uc := &fakeHandoff{}
	// A guard that refuses everything proves the exchange route is not behind it.
	refuse := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	}
	w := do(t, handoffServer(uc, refuse), http.MethodPost, "/payments/handoff/exchange",
		`{"booking_id":"RB-00912","handoff_code":"hc_abc"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "ps_x", decodeHandoff(t, w.Body.Bytes()).Data["payment_session"])
}

func TestExchangeWithABodyThatIsNotJSONIsA400(t *testing.T) {
	uc := &fakeHandoff{}
	w := do(t, handoffServer(uc, passthroughGuard()), http.MethodPost, "/payments/handoff/exchange", `not json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, apperror.CodeValidationError, decodeHandoff(t, w.Body.Bytes()).Meta.ErrorCode)
	assert.Zero(t, uc.calls)
}

func TestExchangePassesTheUseCaseErrorThrough(t *testing.T) {
	uc := &fakeHandoff{exchangeFn: func(dto.ExchangeRequest) (dto.ExchangeResponse, error) {
		return dto.ExchangeResponse{}, apperror.ErrUnauthorized("x").WithCode(apperror.CodeHandoffCodeInvalid)
	}}
	w := do(t, handoffServer(uc, passthroughGuard()), http.MethodPost, "/payments/handoff/exchange",
		`{"booking_id":"RB-1","handoff_code":"hc_used"}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, apperror.CodeHandoffCodeInvalid, decodeHandoff(t, w.Body.Bytes()).Meta.ErrorCode)
}

func TestTheHandoffRoutesDoNotShadowTheExistingPaymentRoutes(t *testing.T) {
	mux := http.NewServeMux()
	pay := &fakeUsecase{}
	RegisterRoutes(mux, NewPayment(pay), testGuard(grantauth.Grant{Token: "gt_1"}))
	RegisterHandoffRoutes(mux, NewHandoff(&fakeHandoff{}), testGuard(grantauth.Grant{Token: "gt_1"}))
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/payments/RB-1/handoff"},
		{http.MethodPost, "/payments/handoff/exchange"},
		{http.MethodGet, "/payments/RB-1/status"},
		{http.MethodPost, "/payments/webhook"},
	} {
		_, pattern := mux.Handler(httptest.NewRequest(c.method, c.path, nil))
		assert.NotEmpty(t, pattern, "%s %s has no route", c.method, c.path)
	}
}
