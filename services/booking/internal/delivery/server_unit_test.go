package delivery_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/delivery"
)

func TestNewReportsAnUnreachableDatabaseAndReleasesRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := config.Config{
		App:   config.App{Env: "local"},
		DB:    config.DB{URL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable", MaxConns: 1},
		Redis: config.Redis{Addr: mr.Addr()},
	}
	_, deps, err := delivery.New(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "database") {
		t.Fatalf("err = %v, want a database error", err)
	}
	if deps.DB != nil || deps.Redis != nil {
		t.Fatalf("deps = %+v, want none on failure", deps)
	}
}

func TestNewReportsAnUnparseableDatabaseURLAndClosesRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := config.Config{
		App:   config.App{Env: "local"},
		DB:    config.DB{URL: "://not a dsn"},
		Redis: config.Redis{Addr: mr.Addr()},
	}
	_, deps, err := delivery.New(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "database") {
		t.Fatalf("err = %v, want a database error", err)
	}
	if deps.DB != nil || deps.Redis != nil {
		t.Fatalf("deps = %+v, want none on failure", deps)
	}
}

func TestCloseOfEmptyDepsDoesNothing(t *testing.T) {
	delivery.Deps{}.Close()
}
