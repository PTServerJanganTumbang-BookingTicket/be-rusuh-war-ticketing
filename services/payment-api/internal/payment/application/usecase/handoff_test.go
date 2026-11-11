package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// memStore is an in-memory IHandoffStore. grants maps a grant token to its booking id and remaining TTL.
type memStore struct {
	codes    map[string]string
	sessions map[string]string
	ttls     map[string]time.Duration
	grants   map[string]struct {
		booking string
		ttl     time.Duration
	}
	err error
}

func newMemStore() *memStore {
	return &memStore{codes: map[string]string{}, sessions: map[string]string{}, ttls: map[string]time.Duration{},
		grants: map[string]struct {
			booking string
			ttl     time.Duration
		}{}}
}

func (s *memStore) grant(token, booking string, ttl time.Duration) {
	s.grants[token] = struct {
		booking string
		ttl     time.Duration
	}{booking, ttl}
}

func (s *memStore) PutCode(_ context.Context, code, grant string, ttl time.Duration) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if _, ok := s.codes[code]; ok {
		return false, nil
	}
	s.codes[code], s.ttls[code] = grant, ttl
	return true, nil
}

func (s *memStore) TakeCode(_ context.Context, code string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	g, ok := s.codes[code]
	delete(s.codes, code)
	return g, ok, nil
}

func (s *memStore) GrantBooking(_ context.Context, token string) (string, time.Duration, bool, error) {
	g, ok := s.grants[token]
	return g.booking, g.ttl, ok, nil
}

func (s *memStore) PutSession(_ context.Context, session, grant string, ttl time.Duration) (bool, error) {
	s.sessions[session], s.ttls[session] = grant, ttl
	return true, nil
}

type fakeLimiter struct {
	allowed    bool
	retryAfter time.Duration
	err        error
	calls      int
}

func (l *fakeLimiter) Allow(context.Context, string) (bool, time.Duration, error) {
	l.calls++
	return l.allowed, l.retryAfter, l.err
}

type handoffRig struct {
	uc      *HandoffUsecase
	repo    *fakeRepo
	store   *memStore
	limiter *fakeLimiter
}

func newHandoffRig(t *testing.T, b entity.Booking) *handoffRig {
	t.Helper()
	r := &handoffRig{repo: &fakeRepo{}, store: newMemStore(), limiter: &fakeLimiter{allowed: true}}
	r.repo.getOwnedFn = func(_ context.Context, id, grant string) (entity.Booking, error) {
		if id != b.ID || grant != b.GrantToken {
			return entity.Booking{}, portout.ErrNotFound
		}
		return b, nil
	}
	n := 0
	r.uc = NewHandoff(r.repo, r.store, r.limiter).(*HandoffUsecase)
	r.uc.now = func() time.Time { return fixedNow }
	r.uc.random = func() (string, error) { n++; return fmt.Sprintf("secret-%02d", n), nil }
	return r
}

func dataOf(t *testing.T, err error) map[string]any {
	t.Helper()
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr))
	d, ok := appErr.Data.(map[string]any)
	require.True(t, ok, "data = %#v", appErr.Data)
	return d
}

func TestCreateHandoff_AnswersEachBookingStatusAsTheContractSays(t *testing.T) {
	cases := []struct {
		status entity.BookingStatus
		want   string // "" = a code is issued
	}{
		{entity.StatusGranted, "SELECTING"},
		{entity.StatusSelecting, "SELECTING"},
		{entity.StatusHeld, ""},
		{entity.StatusAwaitingPayment, ""},
		{entity.StatusPendingCancellation, ""},
		{entity.StatusConfirmed, "PAID"},
		{entity.StatusExpired, "EXPIRED"},
		{entity.StatusLapsed, "EXPIRED"},
		{entity.StatusAbandoned, "EXPIRED"},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			b := heldBooking(c.status)
			if c.status == entity.StatusPendingCancellation {
				past := fixedNow.Add(-10 * time.Second)
				b.PaymentExpiresAt = &past // inside the 30 s grace: still accepted
			}
			r := newHandoffRig(t, b)
			out, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
			if c.want == "" {
				require.NoError(t, err)
				assert.Equal(t, b.ID, out.BookingID)
				return
			}
			assertAppError(t, err, http.StatusConflict, apperror.CodeBookingStatusInvalid)
			assert.Equal(t, c.want, dataOf(t, err)["status"])
			assert.Empty(t, r.store.codes, "no code for a booking that cannot be paid")
		})
	}
}

func TestCreateHandoff_AHeldBookingPastItsDeadlineIsAClosedWindow(t *testing.T) {
	for name, offset := range map[string]time.Duration{"at the deadline": 0, "one second after": time.Second} {
		t.Run(name, func(t *testing.T) {
			b := heldBooking(entity.StatusHeld)
			deadline := fixedNow.Add(-offset)
			b.PaymentExpiresAt = &deadline
			r := newHandoffRig(t, b)
			_, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
			assertAppError(t, err, http.StatusConflict, apperror.CodePaymentWindowClosed)
			assert.Equal(t, deadline.Format(time.RFC3339), dataOf(t, err)["payment_expires_at"])
		})
	}
	b := heldBooking(entity.StatusHeld)
	deadline := fixedNow.Add(time.Second)
	b.PaymentExpiresAt = &deadline
	_, err := newHandoffRig(t, b).uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	require.NoError(t, err, "one second before the deadline is still payable")
}

func TestCreateHandoff_StoresTheGrantTokenUnderAPrefixedCodeForSixtySeconds(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	out, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	require.NoError(t, err)
	assert.Equal(t, "hc_secret-01", out.HandoffCode)
	assert.Equal(t, b.GrantToken, r.store.codes[out.HandoffCode])
	assert.Equal(t, 60*time.Second, r.store.ttls[out.HandoffCode])
	assert.Equal(t, fixedNow.Add(60*time.Second).Format(time.RFC3339), out.ExpiresAt)
}

func TestCreateHandoff_TwoCallsGiveTwoCodesThatBothWork(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	first, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	require.NoError(t, err)
	second, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	require.NoError(t, err)
	assert.NotEqual(t, first.HandoffCode, second.HandoffCode)
	assert.Len(t, r.store.codes, 2)
}

func TestCreateHandoff_AnotherGrantsBookingIsNotFound(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	_, err := r.uc.CreateHandoff(context.Background(), b.ID, "someone-elses-grant")
	assertAppError(t, err, http.StatusNotFound, apperror.CodeNotFound)
}

func TestCreateHandoff_OverTheLimitIs429WithRetryAfter(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	r.limiter.allowed, r.limiter.retryAfter = false, 5500*time.Millisecond
	_, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	assertAppError(t, err, http.StatusTooManyRequests, apperror.CodeTooManyRequests)
	assert.Equal(t, 6, dataOf(t, err)["retry_after"], "rounded up to whole seconds")
	assert.Empty(t, r.store.codes)
}

func TestCreateHandoff_ABrokenLimiterLetsTheRequestThrough(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	r.limiter.err = errors.New("NOSCRIPT")
	_, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	require.NoError(t, err)
}

func TestCreateHandoff_RedisFailingOnTheCodeWriteIs503(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	r.store.err = errors.New("connection refused")
	_, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	assertAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
}

func TestCreateHandoff_RealCodesAreHcAnd256Bits(t *testing.T) {
	secret, err := random256()
	require.NoError(t, err)
	assert.Len(t, secret, 43, "32 bytes base64url without padding")
	assert.False(t, strings.ContainsAny(secret, "+/="), "URL-safe, no padding")
}

// ── exchange ─────────────────────────────────────────────────────────────────

func codeFor(t *testing.T, r *handoffRig, b entity.Booking) string {
	t.Helper()
	out, err := r.uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	require.NoError(t, err)
	return out.HandoffCode
}

func TestExchangeHandoff_GivesASessionThatLivesExactlyAsLongAsTheGrant(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	r.store.grant(b.GrantToken, b.ID, 700*time.Second)
	code := codeFor(t, r, b)

	out, err := r.uc.ExchangeHandoff(context.Background(), dto.ExchangeRequest{BookingID: b.ID, HandoffCode: code})
	require.NoError(t, err)
	assert.Equal(t, b.ID, out.BookingID)
	assert.True(t, strings.HasPrefix(out.PaymentSession, "ps_"))
	assert.Equal(t, b.GrantToken, r.store.sessions[out.PaymentSession])
	assert.Equal(t, 700*time.Second, r.store.ttls[out.PaymentSession])
	assert.Equal(t, fixedNow.Add(700*time.Second).Format(time.RFC3339), out.ExpiresAt)
}

func TestExchangeHandoff_ACodeWorksOnlyOnce(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	r.store.grant(b.GrantToken, b.ID, time.Minute)
	code := codeFor(t, r, b)
	req := dto.ExchangeRequest{BookingID: b.ID, HandoffCode: code}
	_, err := r.uc.ExchangeHandoff(context.Background(), req)
	require.NoError(t, err)
	_, err = r.uc.ExchangeHandoff(context.Background(), req)
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeHandoffCodeInvalid)
}

func TestExchangeHandoff_ACodeForBookingAUsedForBookingBIsRefusedAndSpent(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	r.store.grant(b.GrantToken, b.ID, time.Minute)
	code := codeFor(t, r, b)
	_, err := r.uc.ExchangeHandoff(context.Background(), dto.ExchangeRequest{BookingID: "RB-OTHER", HandoffCode: code})
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeHandoffCodeInvalid)
	_, err = r.uc.ExchangeHandoff(context.Background(), dto.ExchangeRequest{BookingID: b.ID, HandoffCode: code})
	assertAppError(t, err, http.StatusUnauthorized, apperror.CodeHandoffCodeInvalid)
	assert.Empty(t, r.store.sessions)
}

func TestExchangeHandoff_AGrantDeletedAfterTheCodeWasIssuedIsGrantTokenInvalid(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	code := codeFor(t, r, b) // no grant in the store: it was dropped by the expiry sweep
	_, err := r.uc.ExchangeHandoff(context.Background(), dto.ExchangeRequest{BookingID: b.ID, HandoffCode: code})
	assertAppError(t, err, http.StatusUnauthorized, "GRANT_TOKEN_INVALID")
	assert.Empty(t, r.store.sessions)
}

func TestExchangeHandoff_AGrantWithoutATTLNeverGetsASession(t *testing.T) {
	b := heldBooking(entity.StatusHeld)
	r := newHandoffRig(t, b)
	r.store.grant(b.GrantToken, b.ID, -1)
	code := codeFor(t, r, b)
	_, err := r.uc.ExchangeHandoff(context.Background(), dto.ExchangeRequest{BookingID: b.ID, HandoffCode: code})
	assertAppError(t, err, http.StatusUnauthorized, "GRANT_TOKEN_INVALID")
	assert.Empty(t, r.store.sessions)
}

func TestExchangeHandoff_AMalformedBodyIsA400WithoutTouchingRedis(t *testing.T) {
	r := newHandoffRig(t, heldBooking(entity.StatusHeld))
	r.store.err = errors.New("must not be called")
	for name, req := range map[string]dto.ExchangeRequest{
		"no booking":       {HandoffCode: "hc_x"},
		"no code":          {BookingID: "RB-1"},
		"wrong prefix":     {BookingID: "RB-1", HandoffCode: "ps_x"},
		"code too long":    {BookingID: "RB-1", HandoffCode: "hc_" + strings.Repeat("a", 62)},
		"booking too long": {BookingID: strings.Repeat("b", 65), HandoffCode: "hc_x"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := r.uc.ExchangeHandoff(context.Background(), req)
			assertAppError(t, err, http.StatusBadRequest, apperror.CodeValidationError)
		})
	}
}

func TestExchangeHandoff_RedisFailingOnTheCodeReadIs503(t *testing.T) {
	r := newHandoffRig(t, heldBooking(entity.StatusHeld))
	r.store.err = errors.New("connection refused")
	_, err := r.uc.ExchangeHandoff(context.Background(), dto.ExchangeRequest{BookingID: "RB-1", HandoffCode: "hc_x"})
	assertAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
}

func TestCreateHandoff_AnUnmappedStatusIsAServerErrorNeverARawValue(t *testing.T) {
	b := heldBooking(entity.BookingStatus("SOMETHING_NEW"))
	_, err := newHandoffRig(t, b).uc.CreateHandoff(context.Background(), b.ID, b.GrantToken)
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusInternalServerError, appErr.HTTPStatus)
	assert.Nil(t, appErr.Data, "no data.status outside the four §4.4 values")
}
