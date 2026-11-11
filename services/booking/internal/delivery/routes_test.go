package delivery_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/delivery"
)

const sessionSecret = "route-test-session-secret"

// rig is the real handler (real grantauth middleware) over miniredis, with an account grant for acc_1.
type rig struct {
	h  http.Handler
	mr *miniredis.Miniredis
}

func newRig(t *testing.T) *rig {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cfg := config.Config{App: config.App{Env: "local"}, Grant: config.Grant{JWTSecret: sessionSecret}}
	h, err := delivery.NewHandler(cfg, delivery.Deps{Redis: rdb})
	if err != nil {
		t.Fatal(err)
	}
	mr.HSet(rediskeys.Grant("gt_acc"), rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, "RB-1",
		rediskeys.FieldQueueToken, "qt_1", rediskeys.FieldAccountID, "acc_1")
	return &rig{h: h, mr: mr}
}

func handler(t *testing.T) http.Handler { return newRig(t).h }

func session(t *testing.T, secret, account string) string {
	t.Helper()
	i, err := token.NewIssuer(secret, "rusuh-user", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := i.Issue(account, account+"@example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("{}")))
	return rec
}

func TestTheHealthCheckNeedsNoGrant(t *testing.T) {
	if rec := serve(handler(t), http.MethodGet, "/sys/ping"); rec.Code != http.StatusOK {
		t.Fatalf("GET /sys/ping = %d, want 200 without any header", rec.Code)
	}
}

// The scaffold's POST /bookings writes to memory with the wrong response shape. It stays unmounted until the real
// one (build step B4), so nobody mistakes it for a working endpoint.
func TestCreateBookingIsNotMountedUntilItIsBuilt(t *testing.T) {
	if rec := serve(handler(t), http.MethodPost, "/bookings"); rec.Code != http.StatusNotFound {
		t.Fatalf("POST /bookings = %d, want 404", rec.Code)
	}
}

func TestEveryResponseIsMarkedNoStore(t *testing.T) {
	for _, path := range []string{"/sys/ping", "/bookings"} {
		if got := serve(handler(t), http.MethodGet, path).Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s Cache-Control = %q, want no-store", path, got)
		}
	}
}

func TestNewConnectsToTheConfiguredRedisAndDatabase(t *testing.T) {
	addr, dsn := os.Getenv("REDIS_ADDR"), os.Getenv("DATABASE_URL")
	if addr == "" || dsn == "" || testing.Short() {
		t.Skip("set REDIS_ADDR and DATABASE_URL (make verify does)")
	}
	cfg := config.Config{App: config.App{Env: "local"}, DB: config.DB{URL: dsn, MaxConns: 1}, Redis: config.Redis{Addr: addr, Cluster: true}}
	h, deps, err := delivery.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer deps.Close()
	if err := deps.DB.Ping(context.Background()); err != nil {
		t.Fatalf("database pool: %v", err)
	}
	if rec := serve(h, http.MethodGet, "/sys/ping"); rec.Code != http.StatusOK {
		t.Fatalf("GET /sys/ping = %d", rec.Code)
	}
}

func TestNewFailsWhenRedisIsUnreachable(t *testing.T) {
	cfg := config.Config{DB: config.DB{URL: "postgres://unused", MaxConns: 1}, Redis: config.Redis{Addr: "127.0.0.1:1"}}
	if _, _, err := delivery.New(context.Background(), cfg); err == nil {
		t.Fatal("New must fail when Redis does not answer")
	}
}

func TestTheTicketPageAnswers401WithoutAGrant(t *testing.T) {
	rec := serve(handler(t), http.MethodGet, "/events/evt_a/tickets")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "GRANT_TOKEN_MISSING") {
		t.Fatalf("GET /events/evt_a/tickets = %d %s, want 401 GRANT_TOKEN_MISSING", rec.Code, rec.Body.String())
	}
}

func TestTheBookingDetailAnswers401WithoutAGrant(t *testing.T) {
	rec := serve(handler(t), http.MethodGet, "/bookings/RB-1")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "GRANT_TOKEN_MISSING") {
		t.Fatalf("GET /bookings/RB-1 = %d %s, want 401 GRANT_TOKEN_MISSING", rec.Code, rec.Body.String())
	}
}

func TestTheHandlerRefusesAMissingETicketKeyOutsideLocal(t *testing.T) {
	if _, err := delivery.NewHandler(config.Config{App: config.App{Env: "production"}}, delivery.Deps{}); err == nil {
		t.Fatal("without ETICKET_SIGNING_KEY outside local the handler must not start")
	}
}

func TestNewReportsAMissingETicketKeyBeforeOpeningAnyConnection(t *testing.T) {
	cfg := config.Config{App: config.App{Env: "production"}, Redis: config.Redis{Addr: "127.0.0.1:1"}}
	_, _, err := delivery.New(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "ETICKET_SIGNING_KEY") {
		t.Fatalf("err = %v, want the key reported (not a connection error)", err)
	}
}

// Contract §4: an account grant needs the session of its account on every grant route (ADR-006 §8.17).
func TestEveryGrantRouteChecksTheSessionOfAnAccountGrant(t *testing.T) {
	cases := []struct {
		name          string
		authorization string
		status        int
		code          string
	}{
		{"no Authorization header", "", http.StatusUnauthorized, "UNAUTHORIZED"},
		{"another scheme", "Basic abc", http.StatusUnauthorized, "TOKEN_INVALID"},
		{"a token signed by another secret", "Bearer " + session(t, "not-the-secret", "acc_1"), http.StatusUnauthorized, "TOKEN_INVALID"},
		{"the session of another account", "Bearer " + session(t, sessionSecret, "acc_2"), http.StatusUnauthorized, "UNAUTHORIZED"},
	}
	for _, path := range []string{"/events/evt_a/tickets", "/bookings/RB-1"} {
		for _, tc := range cases {
			t.Run(path+" "+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("X-Grant-Token", "gt_acc")
				if tc.authorization != "" {
					req.Header.Set("Authorization", tc.authorization)
				}
				rec := httptest.NewRecorder()
				newRig(t).h.ServeHTTP(rec, req)
				if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"error_code":"`+tc.code+`"`) {
					t.Fatalf("%s = %d %s, want %d %s", path, rec.Code, rec.Body.String(), tc.status, tc.code)
				}
			})
		}
	}
}

func TestAnExpiredSessionAnswersTokenExpiredOnEveryGrantRoute(t *testing.T) {
	i, err := token.NewIssuer(sessionSecret, "rusuh-user", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := i.Issue("acc_1", "a@example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	for _, path := range []string{"/events/evt_a/tickets", "/bookings/RB-1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Grant-Token", "gt_acc")
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		newRig(t).h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error_code":"TOKEN_EXPIRED"`) {
			t.Fatalf("%s = %d %s, want 401 TOKEN_EXPIRED", path, rec.Code, rec.Body.String())
		}
	}
}

func TestTheHandlerRefusesAnEmptyJWTSecret(t *testing.T) {
	cfg := config.Config{App: config.App{Env: "local"}}
	if _, err := delivery.NewHandler(cfg, delivery.Deps{}); err == nil {
		t.Fatal("without JWT_SECRET the handler must not start: account grants could not be checked")
	}
}
