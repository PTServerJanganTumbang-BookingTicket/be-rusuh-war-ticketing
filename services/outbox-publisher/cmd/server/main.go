package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/delivery"
)

func main() {
	dotenv.LoadEnv()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", slog.Any("err", err))
		os.Exit(1)
	}

	setupLogger(cfg.Log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runner, err := delivery.New(ctx, cfg)
	if err != nil {
		slog.Error("Failed to wire dependencies", slog.Any("err", err))
		os.Exit(1)
	}
	defer runner.Close()

	slog.Info("Publisher starting", slog.Duration("interval", runner.Interval()))

	ticker := time.NewTicker(runner.Interval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutdown publisher ...")
			return
		case <-ticker.C:
			// A pass is bounded by the tick so a slow database cannot let
			// passes pile up on top of each other.
			passCtx, cancel := context.WithTimeout(ctx, runner.Interval()*10)
			result, err := runner.Usecase.Drain(passCtx)
			cancel()
			if err != nil {
				slog.Error("drain failed", slog.Any("err", err))
				continue
			}
			if result.Claimed > 0 || result.Failed > 0 {
				slog.Info("drained",
					slog.Int("published", result.Published),
					slog.Int("skipped", result.Skipped),
					slog.Int("failed", result.Failed),
					slog.Int("remaining", result.Remaining),
					slog.String("metric", "OutboxLag"))
			}
		}
	}
}

func setupLogger(logCfg config.Log) {
	var level slog.Level
	switch logCfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if logCfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}
