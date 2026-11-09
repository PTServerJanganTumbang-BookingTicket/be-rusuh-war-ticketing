package delivery_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/delivery"
)

func handler(t *testing.T) http.Handler {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return delivery.NewHandler(config.Config{}, delivery.Deps{Redis: rdb})
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
	cfg := config.Config{DB: config.DB{URL: dsn, MaxConns: 1}, Redis: config.Redis{Addr: addr, Cluster: true}}
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
