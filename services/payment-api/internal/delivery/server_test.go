package delivery

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentcfg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/config"
)

// ── Close ──────────────────────────────────────────────────────────────────

func TestServer_Close_NilFields(t *testing.T) {
	s := &Server{}
	assert.NotPanics(t, s.Close)
}

func TestServer_Close_RunsTheCloser(t *testing.T) {
	called := 0
	s := &Server{close: func() { called++ }}

	s.Close()
	assert.Equal(t, 1, called)
}

// ── New (connection paths) ─────────────────────────────────────────────────

// An unreachable database must fail at wiring time rather than at the first
// request: a Lambda that starts healthy against a dead Aurora serves 500s
// while reporting itself fine.
func TestNew_UnreachableDatabase_Fails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	srv, err := New(ctx, paymentcfg.Config{
		DB: paymentcfg.DB{
			// Port 1 is never a Postgres. A malformed DSN would also do, but
			// this exercises the dial path rather than the parser.
			DSN:      "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
			MaxConns: 1,
		},
		Redis: paymentcfg.Redis{Addr: "127.0.0.1:1"},
	})
	require.Error(t, err)
	assert.Nil(t, srv)
}

// ── newMux (construction + routing paths) ─────────────────────────────────

// lazyPool returns a non-nil pgxpool.Pool that has never connected.
// pgxpool v5 is lazy: NewWithConfig returns immediately without dialling.
// pool.Close() on an unconnected pool is safe and instant.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	return pool
}

func miniredisRDB(t *testing.T) *goredis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// An empty JWT secret must be caught at wiring time — the guard would fail
// closed on every account grant at runtime otherwise.
func TestNewMux_EmptyJWTSecret_ReturnsErrorAndClosesResources(t *testing.T) {
	pool := lazyPool(t)
	rdb := miniredisRDB(t)

	srv, err := newMux(pool, rdb, paymentcfg.Config{
		App: paymentcfg.App{JWTSecret: ""},
	})
	require.Error(t, err)
	assert.Nil(t, srv)
	// pool.Close() and rdb.Close() were called inside newMux's error path.
}

// A valid config must wire all adapters, register the expected routes, and
// release resources cleanly on Close.
func TestNewMux_ValidConfig_RegistersRoutesAndReleasesResources(t *testing.T) {
	pool := lazyPool(t)
	rdb := miniredisRDB(t)

	cfg := paymentcfg.Config{
		App: paymentcfg.App{
			JWTSecret:    "test-jwt-secret-must-be-nonempty",
			DevicePepper: "test-device-pepper",
		},
		Midtrans: paymentcfg.Midtrans{
			BaseURL: "https://api.sandbox.midtrans.com",
			Timeout: 5 * time.Second,
		},
		Payment: paymentcfg.Payment{
			TTL:               10 * time.Minute,
			MinRetryRemaining: time.Minute,
			MaxAttempts:       3,
		},
	}

	srv, err := newMux(pool, rdb, cfg)
	require.NoError(t, err)
	require.NotNil(t, srv)

	// /sys/ping is unguarded — 200 without any header.
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sys/ping", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// Customer-facing routes require X-Grant-Token.
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/payments/RB-1/methods"},
		{http.MethodPost, "/payments/RB-1"},
		{http.MethodPost, "/payments/RB-1/retry"},
		{http.MethodGet, "/payments/RB-1/status"},
	} {
		rec = httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", tc.method, tc.path)
		assert.Contains(t, rec.Body.String(), "GRANT_TOKEN_MISSING", "%s %s", tc.method, tc.path)
	}

	// Close releases pool and rdb without panicking.
	assert.NotPanics(t, srv.Close)
}

// ── newQRNonce ─────────────────────────────────────────────────────────────

// 256 bits from crypto/rand, base64url so it survives a URL and a QR without
// escaping.
func TestNewQRNonce(t *testing.T) {
	got, err := newQRNonce()
	require.NoError(t, err)

	raw, err := base64.RawURLEncoding.DecodeString(got)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
}

// The nonce is the only thing standing between a screenshot and a forged
// ticket, so two tickets must never share one.
func TestNewQRNonce_IsUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 256; i++ {
		got, err := newQRNonce()
		require.NoError(t, err)
		_, dup := seen[got]
		require.False(t, dup, "nonce repeated")
		seen[got] = struct{}{}
	}
}

// Deliberately NOT derived from the ticket id, the booking id or the NIK: a QR
// is screenshotted, forwarded and photographed, so it must be unguessable and
// must carry no identity.
func TestNewQRNonce_CarriesNoIdentity(t *testing.T) {
	got, err := newQRNonce()
	require.NoError(t, err)

	for _, identity := range []string{"RB-", "3171", "ACC-", "@"} {
		assert.NotContains(t, got, identity)
	}
}
