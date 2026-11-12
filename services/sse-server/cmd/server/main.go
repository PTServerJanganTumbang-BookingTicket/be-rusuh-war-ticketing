// Command server runs rusuh-sse-server on ECS Fargate behind the events ALB.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/logger"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/sse-server/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/sse-server/internal/metrics"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/sse-server/internal/stream"
)

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

	srv := stream.NewServer(cfg.Stream, stream.Deps{
		Store:   stream.NewRedisStore(rdb),
		Limiter: ratelimit.New(rdb),
		Source:  stream.NewRedisSource(rdb),
		Metrics: metrics.NewEMF(os.Stdout),
	})

	// The subscription must keep delivering while the drain waits, so it has its own context.
	runCtx, stopRun := context.WithCancel(context.Background())
	defer stopRun()
	go func() { _ = srv.Run(runCtx) }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)

	// No WriteTimeout: a stream stays open for hours.
	handler := srv.Handler()
	if cfg.CORSOrigin != "" {
		handler = corsMiddleware(cfg.CORSOrigin, handler)
	}
	httpSrv := &http.Server{Addr: ":" + strconv.Itoa(cfg.Port), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		slog.Info("SSE server starting", slog.Int("port", cfg.Port))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Failed to serve", slog.Any("err", err))
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	stop() // a second signal now ends the process instead of being swallowed
	slog.Info("SIGTERM: draining")
	drainCtx, cancel := context.WithTimeout(context.Background(), cfg.Stream.DrainDeadline+cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Drain(drainCtx); err != nil {
		slog.Error("Drain did not finish", slog.Any("err", err))
	}
	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel2()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown failed", slog.Any("err", err))
		os.Exit(1)
	}
}

// corsMiddleware adds Access-Control-Allow-Origin headers and handles OPTIONS preflight.
// SSE streams require CORS when the stream host differs from the page host.
// CORS_ALLOW_ORIGIN accepts a comma-separated list of origins, e.g.:
//
//	https://rush-my-ticket.my.id,http://localhost:3000
func corsMiddleware(allowOrigins string, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range strings.Split(allowOrigins, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowed[o] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "X-Queue-Token, Cookie")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
