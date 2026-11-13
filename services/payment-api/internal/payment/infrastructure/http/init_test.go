package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
)

// ── test doubles ──────────────────────────────────────────────────────────────

type fakeUsecase struct {
	methodsFn func(ctx context.Context, bookingID, grant string) (dto.MethodsResponse, error)
	initFn    func(ctx context.Context, bookingID, grant string, req dto.InitiateRequest) (dto.InitiateResponse, error)
	retryFn   func(ctx context.Context, bookingID, grant string) (dto.RetryResponse, error)
	statusFn  func(ctx context.Context, bookingID, grant string) (dto.StatusResponse, error)
	notifyFn  func(ctx context.Context, n dto.Notification) error

	gotBookingID string
	gotGrant     string
	gotRequest   dto.InitiateRequest
	gotNotify    dto.Notification
	notifyCalls  int
}

func (u *fakeUsecase) GetMethods(ctx context.Context, bookingID, grant string) (dto.MethodsResponse, error) {
	u.gotBookingID, u.gotGrant = bookingID, grant
	if u.methodsFn != nil {
		return u.methodsFn(ctx, bookingID, grant)
	}
	return dto.MethodsResponse{}, nil
}

func (u *fakeUsecase) Initiate(ctx context.Context, bookingID, grant string, req dto.InitiateRequest) (dto.InitiateResponse, error) {
	u.gotBookingID, u.gotGrant, u.gotRequest = bookingID, grant, req
	if u.initFn != nil {
		return u.initFn(ctx, bookingID, grant, req)
	}
	return dto.InitiateResponse{}, nil
}

func (u *fakeUsecase) Retry(ctx context.Context, bookingID, grant string) (dto.RetryResponse, error) {
	u.gotBookingID, u.gotGrant = bookingID, grant
	if u.retryFn != nil {
		return u.retryFn(ctx, bookingID, grant)
	}
	return dto.RetryResponse{}, nil
}

func (u *fakeUsecase) GetStatus(ctx context.Context, bookingID, grant string) (dto.StatusResponse, error) {
	u.gotBookingID, u.gotGrant = bookingID, grant
	if u.statusFn != nil {
		return u.statusFn(ctx, bookingID, grant)
	}
	return dto.StatusResponse{Status: dto.StatusPending}, nil
}

func (u *fakeUsecase) HandleNotification(ctx context.Context, n dto.Notification) error {
	u.notifyCalls++
	u.gotNotify = n
	if u.notifyFn != nil {
		return u.notifyFn(ctx, n)
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// testGuard stands in for pkg/grantauth's middleware: it puts a fixed grant on
// the context, as the real guard does after verifying the token and the device.
func testGuard(g grantauth.Grant) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(grantauth.WithGrant(r.Context(), g)))
		})
	}
}

// passthroughGuard mounts the routes with NO grant on the context, which is
// the wiring bug the handlers answer 500 for.
func passthroughGuard() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func newServer(t *testing.T, uc *fakeUsecase, guard func(http.Handler) http.Handler) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	// Empty allowlist disables IP check in tests.
	RegisterRoutes(mux, NewPayment(uc, nil), guard)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

type envelope struct {
	Meta struct {
		Code       int    `json:"code"`
		Message    string `json:"message"`
		ErrorCode  string `json:"error_code"`
		ServerTime string `json:"server_time"`
	} `json:"meta"`
	Data json.RawMessage `json:"data"`
}

func decode(t *testing.T, w *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
	return e
}

var testGrant = grantauth.Grant{
	Token:     "grant-abc",
	EventID:   "EV-1",
	BookingID: "RB-00912",
	AccountID: "ACC-1",
}
