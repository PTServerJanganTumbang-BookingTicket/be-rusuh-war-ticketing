package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	mock_inbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/_mock/user/port/inbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// The envelope every endpoint in this service owes its caller.
//
// The Apidog contract declares meta.server_time on EVERY user and account
// response — success and error alike — and BR-10 is why: a client synchronises
// its own clock from whatever reply it is already making, and the payment
// countdown is drawn from that. An endpoint that omits it is not a cosmetic
// gap; it is a client that cannot correct for drift.
//
// This file exists because the service got it right by accident rather than by
// construction: every handler writes through pkg/apinizer, which stamps the
// field in meta(). Nothing pinned that. A handler added later that writes its
// own JSON — or a refactor that bypasses WriteEnvelope — would drop the field
// on one route and nothing would notice, because the field is in a shared
// helper that no per-endpoint test looks at.
//
// So this walks every registered route instead of trusting the helper.

// routeCase is one route, with whatever the handler needs to reach its 2xx.
type routeCase struct {
	name   string
	method string
	path   string
	body   string
	auth   bool
	// cookie sets magic_token, which is how §1.3 receives its token.
	cookie string
	// expect sets up the usecase call the handler makes on the happy path.
	expect func(uc *mock_inbound.MockIUserUsecase)
	status int
	// noBody marks the one response Apidog declares with no envelope at all.
	noBody bool
}

func allRoutes() []routeCase {
	return []routeCase{
		{
			name: "request magic link", method: http.MethodPost, path: "/auth/magic-link",
			body: `{"email":"a@example.com"}`, status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().RequestMagicLink(gomock.Any(), gomock.Any()).Return(nil)
			},
		},
		{
			name: "resend magic link", method: http.MethodPost, path: "/auth/magic-link/resend",
			body: `{"email":"a@example.com"}`, status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().RequestMagicLink(gomock.Any(), gomock.Any()).Return(nil)
			},
		},
		{
			// §1.3 reads the magic_token COOKIE: the frontend receives the
			// login token and sets it before calling this.
			name: "verify magic link", method: http.MethodGet, path: "/auth/verify",
			cookie: "tok-123", status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().VerifyMagicLink(gomock.Any(), "tok-123").
					Return(dto.VerifyMagicLinkResponse{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 3600}, nil)
			},
		},
		{
			// Cookie, like §1.3: the emailed link lands on the frontend, which
			// sets magic_token before calling here.
			name: "verify email", method: http.MethodGet, path: "/auth/verify-email",
			cookie: "tok-123",
			status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().VerifyEmail(gomock.Any(), "tok-123").Return(nil)
			},
		},
		{
			name: "register", method: http.MethodPost, path: "/auth/register",
			body:   `{"email":"a@example.com","nik":"3171000000000022","consent":{"accepted":true}}`,
			status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().Register(gomock.Any(), gomock.Any()).
					Return(dto.RegisterResponse{UserID: "USR-1"}, nil)
			},
		},
		{
			name: "refresh session", method: http.MethodPost, path: "/auth/refresh",
			body: `{"refresh_token":"rt-1"}`, status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().RefreshSession(gomock.Any(), "rt-1").
					Return(dto.RefreshSessionResponse{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 3600}, nil)
			},
		},
		{
			name: "get my account", method: http.MethodGet, path: "/accounts/me",
			auth: true, status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().GetMyAccount(gomock.Any(), "USR-1").
					Return(dto.AccountProfileResponse{Email: "a@example.com"}, nil)
			},
		},
		{
			name: "get saved niks", method: http.MethodGet, path: "/accounts/me/saved-niks",
			auth: true, status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().GetSavedNIKs(gomock.Any(), "USR-1").
					Return(dto.SavedNIKsResponse{MaxEntries: 5}, nil)
			},
		},
		{
			name: "add saved nik", method: http.MethodPost, path: "/accounts/me/saved-niks",
			body: `{"nik":"3171000000000033","label":"Budi"}`, auth: true, status: http.StatusCreated,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().AddSavedNIK(gomock.Any(), "USR-1", gomock.Any()).
					Return(dto.SavedNIKItem{ID: "snk-1"}, nil)
			},
		},
		{
			// Apidog declares 204 with no schema, so there is no envelope to
			// carry a clock. Pinned here so the exception stays deliberate:
			// if this ever becomes a 200, it owes a server_time like the rest.
			name: "delete saved nik", method: http.MethodDelete, path: "/accounts/me/saved-niks/snk-1",
			auth: true, status: http.StatusNoContent, noBody: true,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().DeleteSavedNIK(gomock.Any(), "USR-1", "snk-1").Return(nil)
			},
		},
		{
			name: "my tickets", method: http.MethodGet, path: "/users/me/tickets",
			auth: true, status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().GetMyTickets(gomock.Any(), "USR-1", gomock.Any()).
					Return(dto.MyTicketsResponse{Limit: 10}, nil)
			},
		},
		{
			name: "e-ticket", method: http.MethodGet, path: "/tickets/TKT-1/eticket",
			auth: true, status: http.StatusOK,
			expect: func(uc *mock_inbound.MockIUserUsecase) {
				uc.EXPECT().GetETicket(gomock.Any(), "USR-1", "TKT-1").
					Return(dto.ETicketResponse{TicketID: "TKT-1"}, nil)
			},
		},
	}
}

// Every 2xx carries meta.code, meta.message and meta.server_time, and the
// server_time is a real RFC3339 instant rather than a placeholder.
func TestEveryRoute_SuccessEnvelopeCarriesServerTime(t *testing.T) {
	for _, tc := range allRoutes() {
		t.Run(tc.name, func(t *testing.T) {
			mux, uc := newTestServer(t)
			if tc.auth {
				expectAuth(uc, "USR-1")
			}
			tc.expect(uc)

			var rec = authedOrPlain(t, mux, tc)
			require.Equal(t, tc.status, rec.Code)

			if tc.noBody {
				assert.Empty(t, rec.Body.String(), "204 carries no envelope, by contract")
				return
			}

			meta := metaOf(t, rec)
			assert.Equal(t, float64(tc.status), meta["code"], "meta.code mirrors the status")
			assert.NotEmpty(t, meta["message"])

			raw, ok := meta["server_time"].(string)
			require.True(t, ok, "meta.server_time is missing — Apidog declares it on every response (BR-10)")
			ts, err := time.Parse(time.RFC3339, raw)
			require.NoError(t, err, "server_time must be RFC3339, got %q", raw)
			assert.WithinDuration(t, time.Now(), ts, time.Minute)
		})
	}
}

// And every ERROR envelope too. A client whose request failed still needs the
// clock — a 401 on a payment poll is exactly when it is about to refresh and
// retry, and Apidog declares server_time on all of these as well.
func TestEveryRoute_ErrorEnvelopeCarriesServerTime(t *testing.T) {
	for _, tc := range allRoutes() {
		t.Run(tc.name, func(t *testing.T) {
			mux, uc := newTestServer(t)
			if tc.auth {
				expectAuth(uc, "USR-1")
			}
			// Replace the happy path with a refusal from the use case. The
			// handler writes it through the same error writer every endpoint
			// shares, so this covers WriteErrorEnvelope on each route.
			failAll(uc)

			rec := authedOrPlain(t, mux, tc)
			require.GreaterOrEqual(t, rec.Code, 400, "the use case refused")

			meta := metaOf(t, rec)
			assert.Equal(t, float64(rec.Code), meta["code"])
			assert.NotEmpty(t, meta["error_code"], "an error envelope names its code")
			raw, ok := meta["server_time"].(string)
			require.True(t, ok, "meta.server_time is missing from the error envelope")
			_, err := time.Parse(time.RFC3339, raw)
			require.NoError(t, err)
		})
	}
}

// The clock is injectable, which is what lets a deployment correct drift
// without redeploying every service — and what lets this assert an exact value
// rather than a window.
func TestEnvelope_ServerTimeComesFromTheInjectedClock(t *testing.T) {
	fixed := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)
	restore := apinizer.SetClock(func() time.Time { return fixed })
	defer restore()

	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-1")
	uc.EXPECT().GetMyAccount(gomock.Any(), "USR-1").
		Return(dto.AccountProfileResponse{Email: "a@example.com"}, nil)

	rec := authed(t, mux, http.MethodGet, "/accounts/me", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, fixed.Format(time.RFC3339), metaOf(t, rec)["server_time"])
}

// ── helpers ─────────────────────────────────────────────────────────────────

func authedOrPlain(t *testing.T, mux *http.ServeMux, tc routeCase) *httptest.ResponseRecorder {
	t.Helper()
	if tc.auth {
		return authed(t, mux, tc.method, tc.path, tc.body)
	}
	var req *http.Request
	if tc.body == "" {
		req = httptest.NewRequest(tc.method, tc.path, nil)
	} else {
		req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
	}
	if tc.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "magic_token", Value: tc.cookie})
	}
	return serve(mux, req)
}

// failAll makes every inbound method refuse, so one setup covers each route's
// error path without a per-route branch.
func failAll(uc *mock_inbound.MockIUserUsecase) {
	err := apperror.ErrBadRequest("refused by the use case").WithCode(apperror.CodeValidationError)
	uc.EXPECT().RequestMagicLink(gomock.Any(), gomock.Any()).Return(err).AnyTimes()
	uc.EXPECT().VerifyMagicLink(gomock.Any(), gomock.Any()).Return(dto.VerifyMagicLinkResponse{}, err).AnyTimes()
	uc.EXPECT().VerifyEmail(gomock.Any(), gomock.Any()).Return(err).AnyTimes()
	uc.EXPECT().Register(gomock.Any(), gomock.Any()).Return(dto.RegisterResponse{}, err).AnyTimes()
	uc.EXPECT().RefreshSession(gomock.Any(), gomock.Any()).Return(dto.RefreshSessionResponse{}, err).AnyTimes()
	uc.EXPECT().GetMyAccount(gomock.Any(), gomock.Any()).Return(dto.AccountProfileResponse{}, err).AnyTimes()
	uc.EXPECT().GetSavedNIKs(gomock.Any(), gomock.Any()).Return(dto.SavedNIKsResponse{}, err).AnyTimes()
	uc.EXPECT().AddSavedNIK(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.SavedNIKItem{}, err).AnyTimes()
	uc.EXPECT().DeleteSavedNIK(gomock.Any(), gomock.Any(), gomock.Any()).Return(err).AnyTimes()
	uc.EXPECT().GetMyTickets(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.MyTicketsResponse{}, err).AnyTimes()
	uc.EXPECT().GetETicket(gomock.Any(), gomock.Any(), gomock.Any()).Return(dto.ETicketResponse{}, err).AnyTimes()
}
