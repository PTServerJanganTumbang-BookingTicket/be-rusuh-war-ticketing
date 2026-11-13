package delivery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
)

func testConfig(redisAddr string) config.Config {
	return config.Config{
		Env: "local", DatabaseURL: "postgres://u:p@127.0.0.1:1/db", JWTSecret: "s",
		RedisAddr: redisAddr, Captcha: config.CaptchaTurnstile, Join: usecase.DefaultConfig(),
	}
}

func TestNew_wiresRoutesAndCloses(t *testing.T) {
	mr := miniredis.RunT(t)
	srv, err := New(context.Background(), testConfig(mr.Addr()))
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	defer srv.Close()
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sys/ping", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/sys/ping = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events/E1/queue/join", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET join = %d, want 405 (POST only)", rec.Code)
	}
}

// /queue/serving reads Redis only, so it is mounted even when no database is wired (unlike /queue/me).
func TestWireMountsServingWithoutADatabase(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.HSet("queue:{E1}", "state", "OPEN", "now_serving", "7", "rate_per_min", "60")
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	srv, err := Wire(testConfig(mr.Addr()), Clients{Redis: rdb})
	if err != nil {
		t.Fatalf("Wire() = %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events/E1/queue/serving", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"now_serving":7`) {
		t.Errorf("GET serving = %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events/E1/queue/me", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET me without a DB = %d, want 404 (not mounted)", rec.Code)
	}
}

func TestNew_failsWhenRedisIsDown(t *testing.T) {
	if _, err := New(context.Background(), testConfig("127.0.0.1:1")); err == nil {
		t.Fatal("New() with unreachable Redis returned nil error")
	}
}

func TestNew_rejectsBadDatabaseURL(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := testConfig(mr.Addr())
	cfg.DatabaseURL = "::not a url::"
	if _, err := New(context.Background(), cfg); err == nil {
		t.Fatal("New() with a bad DATABASE_URL returned nil error")
	}
}

func TestWire_mockCaptchaAndNoSecret(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := testConfig(mr.Addr())
	cfg.JWTSecret = ""
	if _, err := Wire(cfg, Clients{}); err == nil {
		t.Fatal("Wire() without JWT secret returned nil error")
	}
}
