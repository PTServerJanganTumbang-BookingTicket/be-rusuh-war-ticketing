package http

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
)

func TestGetMethods_OK(t *testing.T) {
	uc := &fakeUsecase{methodsFn: func(context.Context, string, string) (dto.MethodsResponse, error) {
		return dto.MethodsResponse{Methods: []dto.Method{{ID: entity.MethodGoPay, Enabled: true}}}, nil
	}}
	mux := newServer(t, uc, testGuard(testGrant))

	w := do(t, mux, http.MethodGet, "/payments/RB-00912/methods", "")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	e := decode(t, w)
	assert.Equal(t, http.StatusOK, e.Meta.Code)
	assert.NotEmpty(t, e.Meta.ServerTime)
	assert.Contains(t, string(e.Data), entity.MethodGoPay)
}

// The booking id comes from the path and the grant token from the guarded
// context - never the other way round, and never from a header the client
// controls.
func TestHandlers_PassPathIDAndContextGrant(t *testing.T) {
	cases := map[string]struct {
		method, target string
		body           string
	}{
		"methods":  {http.MethodGet, "/payments/RB-00912/methods", ""},
		"initiate": {http.MethodPost, "/payments/RB-00912", `{"method":"gopay"}`},
		"retry":    {http.MethodPost, "/payments/RB-00912/retry", ""},
		"status":   {http.MethodGet, "/payments/RB-00912/status", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			uc := &fakeUsecase{}
			mux := newServer(t, uc, testGuard(testGrant))

			w := do(t, mux, c.method, c.target, c.body)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "RB-00912", uc.gotBookingID)
			assert.Equal(t, "grant-abc", uc.gotGrant)
		})
	}
}

// A route mounted without the guard is a wiring bug, not a client error. A 401
// here would send the client off chasing headers it had sent correctly.
func TestHandlers_WithoutGuard_Are500(t *testing.T) {
	cases := map[string]struct {
		method, target, body string
	}{
		"methods":  {http.MethodGet, "/payments/RB-00912/methods", ""},
		"initiate": {http.MethodPost, "/payments/RB-00912", `{"method":"gopay"}`},
		"retry":    {http.MethodPost, "/payments/RB-00912/retry", ""},
		"status":   {http.MethodGet, "/payments/RB-00912/status", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			uc := &fakeUsecase{}
			mux := newServer(t, uc, passthroughGuard())

			w := do(t, mux, c.method, c.target, c.body)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			// The use case must not have been reached with an empty grant,
			// which would have looked like a legitimate lookup.
			assert.Empty(t, uc.gotGrant)
		})
	}
}

func TestInitiate_DecodesBody(t *testing.T) {
	uc := &fakeUsecase{}
	mux := newServer(t, uc, testGuard(testGrant))

	w := do(t, mux, http.MethodPost, "/payments/RB-00912", `{"method":"bank_transfer","bank":"bca"}`)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, entity.MethodBankTransfer, uc.gotRequest.Method)
	assert.Equal(t, entity.BankBCA, uc.gotRequest.Bank)
}

func TestInitiate_MalformedBody_Is400(t *testing.T) {
	uc := &fakeUsecase{}
	mux := newServer(t, uc, testGuard(testGrant))

	w := do(t, mux, http.MethodPost, "/payments/RB-00912", `{"method":`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, apperror.CodeValidationError, decode(t, w).Meta.ErrorCode)
	assert.Empty(t, uc.gotRequest.Method)
}

// 200, not 201. A replayed initiate returns the same payment and creates
// nothing, and the client cannot tell the two apart - so neither should the
// status code.
func TestInitiate_SuccessIs200NotCreated(t *testing.T) {
	uc := &fakeUsecase{initFn: func(context.Context, string, string, dto.InitiateRequest) (dto.InitiateResponse, error) {
		return dto.InitiateResponse{PaymentID: "RB-00912:1", Status: dto.StatusPending}, nil
	}}
	mux := newServer(t, uc, testGuard(testGrant))

	w := do(t, mux, http.MethodPost, "/payments/RB-00912", `{"method":"gopay"}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, string(decode(t, w).Data), "RB-00912:1")
}

// Every use-case error reaches the client as its own status AND its own
// Appendix A code: clients switch on the code, so a right status with a
// missing code is still a broken response.
func TestHandlers_ErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", apperror.ErrNotFound("x").WithCode(apperror.CodeNotFound), 404, apperror.CodeNotFound},
		{"wrong status", apperror.ErrConflict("x").WithCode(apperror.CodeBookingStatusInvalid), 409, apperror.CodeBookingStatusInvalid},
		{"already settled", apperror.ErrConflict("x").WithCode(apperror.CodePaymentAlreadySettled), 409, apperror.CodePaymentAlreadySettled},
		{"window closed", apperror.ErrConflict("x").WithCode(apperror.CodePaymentWindowClosed), 409, apperror.CodePaymentWindowClosed},
		{"too many attempts", apperror.ErrTooManyRequests("x").WithCode(apperror.CodeMaxAttemptsExceeded), 429, apperror.CodeMaxAttemptsExceeded},
		{"gateway down", apperror.ErrServiceUnavailable("x").WithCode(apperror.CodePaymentGatewayUnavailable), 503, apperror.CodePaymentGatewayUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeUsecase{statusFn: func(context.Context, string, string) (dto.StatusResponse, error) {
				return dto.StatusResponse{}, c.err
			}}
			mux := newServer(t, uc, testGuard(testGrant))

			w := do(t, mux, http.MethodGet, "/payments/RB-00912/status", "")
			assert.Equal(t, c.status, w.Code)
			assert.Equal(t, c.code, decode(t, w).Meta.ErrorCode)
		})
	}
}

// An error that is not an AppError is a bug, not a contract response. It
// becomes a bare 500 with no error_code and no detail: leaking a database
// message to a customer tells an attacker more than it tells the customer.
func TestHandlers_UnknownError_Is500WithoutDetail(t *testing.T) {
	uc := &fakeUsecase{statusFn: func(context.Context, string, string) (dto.StatusResponse, error) {
		return dto.StatusResponse{}, errors.New("pq: relation \"bookings\" does not exist")
	}}
	mux := newServer(t, uc, testGuard(testGrant))

	w := do(t, mux, http.MethodGet, "/payments/RB-00912/status", "")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	e := decode(t, w)
	assert.Empty(t, e.Meta.ErrorCode)
	assert.NotContains(t, e.Meta.Message, "bookings")
	assert.NotContains(t, e.Meta.Message, "pq:")
}

func TestStatusMessage(t *testing.T) {
	assert.Equal(t, "Payment successful", statusMessage(dto.StatusPaid))
	assert.Equal(t, "Payment expired", statusMessage(dto.StatusExpired))
	assert.Equal(t, "Payment failed", statusMessage(dto.StatusFailed))
	assert.Equal(t, "Payment pending", statusMessage(dto.StatusPending))
	// Anything unrecognised reads as pending rather than as an empty message.
	assert.Equal(t, "Payment pending", statusMessage("WHATEVER"))
}

func TestGetStatus_MessageTracksStatus(t *testing.T) {
	for status, want := range map[string]string{
		dto.StatusPaid:    "Payment successful",
		dto.StatusExpired: "Payment expired",
		dto.StatusFailed:  "Payment failed",
		dto.StatusPending: "Payment pending",
	} {
		uc := &fakeUsecase{statusFn: func(context.Context, string, string) (dto.StatusResponse, error) {
			return dto.StatusResponse{Status: status}, nil
		}}
		mux := newServer(t, uc, testGuard(testGrant))

		w := do(t, mux, http.MethodGet, "/payments/RB-00912/status", "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, want, decode(t, w).Meta.Message, status)
	}
}
