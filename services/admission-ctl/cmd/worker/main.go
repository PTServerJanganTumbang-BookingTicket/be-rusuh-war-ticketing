// Command worker runs admission-ctl on ECS Fargate: the one process that lets customers in (ADR-006 §4.1).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/logger"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/postgres"
	redisadapter "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/config"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// logMetrics writes metrics as structured log lines until the CloudWatch EMF adapter lands.
type logMetrics struct{}

func (logMetrics) Inc(name string) {
	slog.Info("metric", slog.String("name", name), slog.Int("count", 1))
}
func (logMetrics) Gauge(name string, value float64) {
	slog.Debug("gauge", slog.String("name", name), slog.Float64("value", value))
}

func main() {
	dotenv.LoadEnv()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", slog.Any("err", err))
		os.Exit(1)
	}
	logger.Setup(cfg.LogLevel, cfg.LogFmt)

	var rdb goredis.UniversalClient
	if cfg.RedisCluster {
		rdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{cfg.RedisAddr}, Password: cfg.RedisPass})
	} else {
		rdb = goredis.NewClient(&goredis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPass})
	}
	defer func() { _ = rdb.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := pgutil.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns, 30*time.Minute)
	if err != nil {
		slog.Error("Failed to open the database", slog.Any("err", err))
		os.Exit(1)
	}
	defer db.Close()

	bookings := postgres.NewBookings(db)
	tick := usecase.NewTick(cfg.Tick, bookings, redisadapter.NewQueue(rdb), systemClock{}, logMetrics{})
	tick.UseEvents(redisadapter.NewEventSource(rdb), cfg.EventRefresh)
	// A held booking nobody starts paying for within 10 minutes expires, and its seats and NIK locks go back.
	tick.WithHeldSweep(bookings, redisadapter.NewHolds(rdb))
	slog.Info("admission-ctl starting", slog.Duration("interval", cfg.Tick.Interval), slog.Duration("eventRefresh", cfg.EventRefresh))

	// Loop returns when ctx ends. Loop only checks ctx between ticks, but the Redis and Aurora calls inside a tick use
	// ctx too, so a SIGTERM can cut a tick short. A half-admitted customer is then the exception, and RECOVER finishes
	// those on the next start. The HELD sweep alone runs on its own context (its rows are already EXPIRED when the
	// release starts and nothing retries them).
	tick.Loop(ctx)
	slog.Info("admission-ctl stopped")
}
