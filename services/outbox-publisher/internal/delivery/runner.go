package delivery

import (
	"context"
	"log/slog"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/application/usecase"
	outboxpostgres "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/infrastructure/postgres"
	outboxredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/infrastructure/redis"
	outboxsqs "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/infrastructure/sqs"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/port/inbound"
)

// Runner owns the wired use case and the resources released on shutdown.
type Runner struct {
	Usecase portin.IPublisherUsecase

	cfg   config.Config
	pool  *pgxpool.Pool
	redis *goredis.Client
}

// Close releases the pool and the Redis client.
func (r *Runner) Close() {
	if r.pool != nil {
		r.pool.Close()
	}
	if r.redis != nil {
		_ = r.redis.Close()
	}
}

// New wires everything.
// Dependency order: pool + redis + sqs → adapters → usecase.
func New(ctx context.Context, cfg config.Config) (*Runner, error) {
	pool, err := outboxpostgres.NewPool(ctx, cfg.DB.DSN, cfg.DB.MaxConns, cfg.DB.ConnLifetime)
	if err != nil {
		return nil, err
	}

	redisClient, err := outboxredis.NewClient(ctx, cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		pool.Close()
		return nil, err
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		pool.Close()
		_ = redisClient.Close()
		return nil, err
	}

	uc := usecase.NewPublisher(
		outboxpostgres.NewOutboxRepository(pool),
		outboxredis.NewLeaseGuard(redisClient, cfg.Queue.LeaseTTL, cfg.Queue.LeaseDoneTTL),
		outboxsqs.NewPublisher(awssqs.NewFromConfig(awsCfg), cfg.Queue.NotificationURL),
		cfg.App.BatchSize,
	)

	return &Runner{Usecase: uc, cfg: cfg, pool: pool, redis: redisClient}, nil
}

// DrainUntil keeps draining until the budget runs out or the backlog empties.
//
// Used by the Lambda entrypoint, where EventBridge cannot schedule faster than
// once a minute: rather than one pass per minute, one invocation drains
// repeatedly within its own budget.
//
// It stops as soon as a pass publishes nothing, so an empty outbox costs one
// query rather than a spin for the whole budget.
func (r *Runner) DrainUntil(ctx context.Context, budget time.Duration) (portin.Result, error) {
	deadline := time.Now().Add(budget)
	total := portin.Result{}

	for {
		result, err := r.Usecase.Drain(ctx)
		total.Claimed += result.Claimed
		total.Published += result.Published
		total.Skipped += result.Skipped
		total.Failed += result.Failed
		total.Remaining = result.Remaining
		if err != nil {
			return total, err
		}

		// Nothing moved: either the outbox is empty or every row is held
		// elsewhere. Either way another immediate pass would not help.
		if result.Published == 0 {
			return total, nil
		}
		if time.Now().After(deadline) {
			slog.InfoContext(ctx, "drain budget exhausted with work remaining",
				slog.Int("remaining", total.Remaining),
				slog.String("metric", "OutboxLag"))
			return total, nil
		}
	}
}

// Interval reports the configured pass interval, for server mode.
func (r *Runner) Interval() time.Duration { return r.cfg.App.Interval }

// DrainBudget reports the configured per-invocation budget.
func (r *Runner) DrainBudget() time.Duration { return r.cfg.App.DrainBudget }
