package delivery_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/delivery"
)

// No database needed: these requests never reach Aurora (health, validation, an id that cannot be a Redis key).
func TestRoutesAnswerWithoutTouchingAurora(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	h := delivery.NewHandler(config.Config{Events: config.Events{QueueCapacity: 100, CacheMaxAgeSeconds: 30, DegradedMaxAge: 5}}, delivery.Deps{Redis: rdb})

	for path, want := range map[string]int{
		"/sys/ping":         http.StatusOK,
		"/events?limit=0":   http.StatusBadRequest,
		"/events?offset=-1": http.StatusBadRequest,
		"/events/%7Bx%7D":   http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d (%s)", path, rec.Code, want, rec.Body.String())
		}
	}
}

func TestNewReportsAnUnreachableRedis(t *testing.T) {
	cfg := config.Config{DB: config.DB{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", MaxConns: 1}, Redis: config.Redis{Addr: "127.0.0.1:1"}}
	if _, _, err := delivery.New(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "redis") {
		t.Fatalf("err = %v, want a redis error", err)
	}
}

func TestNewReportsAnUnreachableDatabaseAndReleasesRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := config.Config{DB: config.DB{URL: "://not a dsn"}, Redis: config.Redis{Addr: mr.Addr()}}
	_, deps, err := delivery.New(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "database") {
		t.Fatalf("err = %v, want a database error", err)
	}
	if deps.DB != nil || deps.Redis != nil {
		t.Fatalf("deps = %+v, want none on failure", deps)
	}
}

func TestNewWiresARealClusterClientWhenConfigured(t *testing.T) {
	cfg := config.Config{DB: config.DB{URL: "://not a dsn"}, Redis: config.Redis{Addr: "127.0.0.1:1", Cluster: true}}
	if _, _, err := delivery.New(context.Background(), cfg); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCloseOfEmptyDepsDoesNothing(t *testing.T) {
	delivery.Deps{}.Close()
}
