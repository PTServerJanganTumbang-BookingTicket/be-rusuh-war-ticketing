// Command seed fills the local stack with the sample Jakarta events (docs/seed-data.md).
//
//	make seed         upsert the catalog, create the Redis keys that are missing
//	make seed-reset   first delete the sample events' Redis keys, then seed again
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/seed"
)

func main() {
	reset := flag.Bool("reset", false, "delete the sample events' Redis keys before seeding (local only)")
	flag.Parse()
	dotenv.LoadEnv()
	if err := run(context.Background(), *reset); err != nil {
		slog.Error("seed failed", slog.Any("err", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, reset bool) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set (make seed sets it from .env)")
	}
	addr := envutil.Env("REDIS_ADDR", "127.0.0.1:16379")
	if err := seed.CheckLocal(dsn, addr, envutil.EnvBool("SEED_ALLOW_REMOTE", false)); err != nil {
		return err
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

	var rdb goredis.UniversalClient
	if envutil.EnvBool("REDIS_CLUSTER", true) {
		rdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{addr}})
	} else {
		rdb = goredis.NewClient(&goredis.Options{Addr: addr})
	}
	defer func() { _ = rdb.Close() }()
	if reset {
		if err := seed.ResetRedis(ctx, rdb, events); err != nil {
			return err
		}
	}
	n, err := seed.Redis(ctx, rdb, events, time.Now())
	if err != nil {
		return err
	}
	slog.Info("seeded", slog.Int("events", len(events)), slog.Int("redis_created", n), slog.Bool("reset", reset))
	return nil
}
