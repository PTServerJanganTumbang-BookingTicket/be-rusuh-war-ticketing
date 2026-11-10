package delivery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"

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
