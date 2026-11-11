// Lambda entrypoint for the seed command.
// Runs Postgres seeding from inside the VPC so it can reach Aurora.
//
// Redis seeding is handled separately via infra/redis-debug, which provides
// CLI-style Redis access from inside the VPC.
//
// Deploy with: cd infra/seed-lambda && ./deploy.sh
// Invoke with: aws lambda invoke --function-name rusuh-prod-seed --payload '{}' out.json
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/seed"
)

type Request struct {
	// Reset is deprecated - Redis seeding moved to infra/redis-debug
	Reset bool `json:"reset,omitempty"`
}

type Response struct {
	Events  int    `json:"events"`
	Message string `json:"message"`
}

func handler(ctx context.Context, req Request) (Response, error) {
	if req.Reset {
		slog.Warn("reset flag is deprecated: Redis seeding has moved to infra/redis-debug")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return Response{}, fmt.Errorf("DATABASE_URL is not set")
	}

	events := seed.JakartaEvents()

	// Postgres
	pool, err := pgutil.NewPool(ctx, dsn, 2, time.Minute)
	if err != nil {
		return Response{}, fmt.Errorf("db connect: %w", err)
	}
	defer pool.Close()

	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return seed.Postgres(ctx, tx, events)
	}); err != nil {
		return Response{}, fmt.Errorf("postgres seed: %w", err)
	}
	slog.Info("postgres seeded", slog.Int("events", len(events)))

	return Response{
		Events:  len(events),
		Message: "postgres seeded (use infra/redis-debug for Redis seeding)",
	}, nil
}

func main() {
	lambda.Start(handler)
}
