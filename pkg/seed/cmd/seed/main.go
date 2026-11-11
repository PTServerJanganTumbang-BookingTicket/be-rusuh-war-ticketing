// Command seed fills the local Postgres with the sample Jakarta events (docs/seed-data.md).
//
// Redis seeding is handled separately via infra/redis-debug, which runs inside the VPC.
//
//	make seed         upsert the catalog to Postgres
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/seed"
)

func main() {
	// Keep the flag for backwards compatibility but ignore it with a warning
	reset := flag.Bool("reset", false, "deprecated: Redis seeding moved to infra/redis-debug")
	flag.Parse()

	if *reset {
		slog.Warn("--reset flag is deprecated: Redis seeding has moved to infra/redis-debug")
	}

	dotenv.LoadEnv()
	if err := run(context.Background()); err != nil {
		slog.Error("seed failed", slog.Any("err", err))
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set (make seed sets it from .env)")
	}

	events := seed.JakartaEvents()

	pool, err := pgutil.NewPool(ctx, dsn, 2, time.Minute)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return seed.Postgres(ctx, tx, events) }); err != nil {
		return fmt.Errorf("postgres (are the migrations applied?): %w", err)
	}

	slog.Info("seeded postgres", slog.Int("events", len(events)))
	slog.Info("to seed Redis, use: cd infra/seed && ./deploy.sh && ./seed-redis.sh")
	return nil
}
