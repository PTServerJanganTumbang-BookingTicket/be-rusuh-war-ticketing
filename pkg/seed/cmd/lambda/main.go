// Lambda entrypoint for the seed command.
// Runs the same seed as `make seed` but from inside the VPC, so it can reach Aurora and ElastiCache.
//
// Deploy with: cd infra/seed-lambda && ./deploy.sh
// Invoke with: aws lambda invoke --function-name rusuh-prod-seed --payload '{}' out.json
// Reset+seed:  aws lambda invoke --function-name rusuh-prod-seed --payload '{"reset":true}' out.json
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/envutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/seed"
)

type Request struct {
	Reset bool `json:"reset"` // if true, delete existing event keys before seeding
}

type Response struct {
	Events       int    `json:"events"`
	RedisCreated int    `json:"redis_created"`
	Reset        bool   `json:"reset"`
	Message      string `json:"message"`
}

func handler(ctx context.Context, req Request) (Response, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return Response{}, fmt.Errorf("DATABASE_URL is not set")
	}
	addr := envutil.Env("REDIS_ADDR", "127.0.0.1:6379")

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

	// Redis
	var rdb goredis.UniversalClient
	if envutil.EnvBool("REDIS_CLUSTER", false) {
		rdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{addr}})
	} else {
		rdb = goredis.NewClient(&goredis.Options{Addr: addr})
	}
	defer func() { _ = rdb.Close() }()

	if req.Reset {
		slog.Info("resetting redis keys...")
		if err := seed.ResetRedis(ctx, rdb, events); err != nil {
			return Response{}, fmt.Errorf("redis reset: %w", err)
		}
	}

	n, err := seed.Redis(ctx, rdb, events, time.Now())
	if err != nil {
		return Response{}, fmt.Errorf("redis seed: %w", err)
	}
	slog.Info("redis seeded", slog.Int("created", n))

	msg := "seeded"
	if req.Reset {
		msg = "reset and seeded"
	}

	return Response{
		Events:       len(events),
		RedisCreated: n,
		Reset:        req.Reset,
		Message:      msg,
	}, nil
}

func main() {
	lambda.Start(handler)
}
