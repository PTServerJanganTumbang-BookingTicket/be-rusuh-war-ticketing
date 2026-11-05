package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

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

	// Wired once per container, not per invocation: the pool and the Redis
	// client are the expensive part, and this function is invoked on a tight
	// schedule.
	runner, err := delivery.New(context.Background(), cfg)
	if err != nil {
		slog.Error("Failed to wire dependencies", slog.Any("err", err))
		os.Exit(1)
	}

	// EventBridge cannot schedule faster than once a minute, so rather than
	// one pass per minute each invocation drains repeatedly inside its own
	// budget. Acceptable because publishing is the fallback path — the
	// latency-sensitive side effects run inline in their own services.
	lambda.Start(func(ctx context.Context) (any, error) {
		result, err := runner.DrainUntil(ctx, runner.DrainBudget())
		slog.InfoContext(ctx, "drain complete",
			slog.Int("published", result.Published),
			slog.Int("skipped", result.Skipped),
			slog.Int("failed", result.Failed),
			slog.Int("remaining", result.Remaining),
			slog.String("metric", "OutboxLag"))
		return result, err
	})
}
