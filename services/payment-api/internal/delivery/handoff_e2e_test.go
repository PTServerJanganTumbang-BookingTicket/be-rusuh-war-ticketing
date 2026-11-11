package delivery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	paymentcfg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/config"
)

// The WebView handoff end to end (ADR-007): the real mux, guards, use cases, Postgres repository and Redis adapters.
// Postgres comes from pkg/pgtest (skipped without DATABASE_URL); Redis is miniredis so the test can drop a grant.

const (
	e2ePepper = "e2e-pepper"
	e2eDevice = "e2e-device-0001"
)

type handoffE2E struct {
	t    *testing.T
	h    http.Handler
	pool *pgxpool.Pool
	mr   *miniredis.Miniredis
}

func newHandoffE2E(t *testing.T) *handoffE2E {
	t.Helper()
	pool := pgtest.New(t)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	srv, err := newMux(pool, rdb, paymentcfg.Config{
		App:      paymentcfg.App{JWTSecret: "e2e-jwt", DevicePepper: e2ePepper},
		Midtrans: paymentcfg.Midtrans{BaseURL: "http://127.0.0.1:1", Timeout: time.Second},
		Payment:  paymentcfg.Payment{TTL: 10 * time.Minute, MinRetryRemaining: time.Minute, MaxAttempts: 3},
	})
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO events (id, name, event_date, venue, image_url) VALUES ('evt_a', 'Konser A', '2026-12-25', 'GBK', 'https://cdn/a.jpg')`)
	require.NoError(t, err)
	return &handoffE2E{t: t, h: srv.Handler, pool: pool, mr: mr}
}

// held seeds a HELD booking with five minutes left to pay, and the guest grant for it (TTL 15 minutes).
func (e *handoffE2E) held(id string) {
	e.t.Helper()
	_, err := e.pool.Exec(e.t.Context(), `INSERT INTO bookings
		(id, event_id, queue_token, status, grant_token, granted_at, booking_expires_at, held_at, payment_expires_at, contact_email)
		VALUES ($1, 'evt_a', 'qt_'||$1, 'HELD', 'gt_'||$1, now() - interval '6 minutes', now() - interval '1 minute',
		        now() - interval '5 minutes', now() + interval '5 minutes', 'buyer@example.com')`, id)
	require.NoError(e.t, err)
	key := rediskeys.Grant("gt_" + id)
	e.mr.HSet(key, rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, id,
		rediskeys.FieldQueueToken, "qt_"+id, rediskeys.FieldDeviceHash, grantauth.DeviceHash([]byte(e2ePepper), e2eDevice))
	e.mr.SetTTL(key, 15*time.Minute)
}

func (e *handoffE2E) do(method, path, body string, headers map[string]string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	require.NoError(e.t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec.Code, out
}

func dataOf(body map[string]any) map[string]any { d, _ := body["data"].(map[string]any); return d }

func errorCode(body map[string]any) string {
	m, _ := body["meta"].(map[string]any)
	c, _ := m["error_code"].(string)
	return c
}

// session runs the app half and the page half: a code from the guest's grant, then the exchange.
func (e *handoffE2E) session(id string) string {
	e.t.Helper()
	code, body := e.do(http.MethodPost, "/payments/"+id+"/handoff", "",
		map[string]string{"X-Grant-Token": "gt_" + id, "X-Device-Id": e2eDevice})
	require.Equal(e.t, http.StatusCreated, code, body)
	hc := dataOf(body)["handoff_code"].(string)

	code, body = e.do(http.MethodPost, "/payments/handoff/exchange", `{"booking_id":"`+id+`","handoff_code":"`+hc+`"}`, nil)
	require.Equal(e.t, http.StatusOK, code, body)
	return dataOf(body)["payment_session"].(string)
}

func TestTheWebViewPollsPaymentStatusWithOnlyItsSession(t *testing.T) {
	e := newHandoffE2E(t)
	e.held("RB-A")
	ps := e.session("RB-A")

	code, body := e.do(http.MethodGet, "/payments/RB-A/status", "", map[string]string{"X-Payment-Session": ps})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "PENDING", dataOf(body)["status"])

	code, body = e.do(http.MethodGet, "/payments/RB-A/methods", "", map[string]string{"X-Payment-Session": ps})
	assert.Equal(t, http.StatusOK, code, body)
}

func TestASessionForBookingAOnBookingBIsNotFound(t *testing.T) {
	e := newHandoffE2E(t)
	e.held("RB-A")
	e.held("RB-B")
	ps := e.session("RB-A")
	code, body := e.do(http.MethodGet, "/payments/RB-B/status", "", map[string]string{"X-Payment-Session": ps})
	assert.Equal(t, http.StatusNotFound, code, body)
	assert.Equal(t, "NOT_FOUND", errorCode(body))
}

func TestStatusStillSaysPaidAfterTheWebhookBecauseTheGrantIsKept(t *testing.T) {
	e := newHandoffE2E(t)
	e.held("RB-A")
	ps := e.session("RB-A")
	// What the webhook's confirm leaves behind: CONFIRMED in Aurora, grant:<token> untouched.
	_, err := e.pool.Exec(t.Context(), `UPDATE bookings SET status = 'CONFIRMED', paid_at = now() WHERE id = 'RB-A'`)
	require.NoError(t, err)

	code, body := e.do(http.MethodGet, "/payments/RB-A/status", "", map[string]string{"X-Payment-Session": ps})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "PAID", dataOf(body)["status"])
}

func TestTheSessionDiesTheMomentTheSweepDropsTheGrant(t *testing.T) {
	e := newHandoffE2E(t)
	e.held("RB-A")
	ps := e.session("RB-A")
	e.mr.Del(rediskeys.Grant("gt_RB-A"))

	code, body := e.do(http.MethodGet, "/payments/RB-A/status", "", map[string]string{"X-Payment-Session": ps})
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, grantauth.CodePaymentSessionInvalid, errorCode(body))
}

func TestASessionCannotMintAnotherHandoffCode(t *testing.T) {
	e := newHandoffE2E(t)
	e.held("RB-A")
	ps := e.session("RB-A")
	code, body := e.do(http.MethodPost, "/payments/RB-A/handoff", "", map[string]string{"X-Payment-Session": ps})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "VALIDATION_ERROR", errorCode(body))
}

func TestAGuestHandoffNeedsTheDeviceTheGrantWasGivenTo(t *testing.T) {
	e := newHandoffE2E(t)
	e.held("RB-A")
	code, body := e.do(http.MethodPost, "/payments/RB-A/handoff", "",
		map[string]string{"X-Grant-Token": "gt_RB-A", "X-Device-Id": "another-device-0001"})
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, grantauth.CodeDeviceMismatch, errorCode(body))
}

func TestTheSessionLivesNoLongerThanTheGrant(t *testing.T) {
	e := newHandoffE2E(t)
	e.held("RB-A")
	e.mr.SetTTL(rediskeys.Grant("gt_RB-A"), 90*time.Second)
	ps := e.session("RB-A")
	assert.Equal(t, 90*time.Second, e.mr.TTL(rediskeys.PaymentSession(ps)))
}
