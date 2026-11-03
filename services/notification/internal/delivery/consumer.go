package delivery

import (
	"context"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awsses "github.com/aws/aws-sdk-go-v2/service/sesv2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/template"
	notifredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/infrastructure/ses"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/infrastructure/sqsconsumer"
)

// Consumer owns the wired handler and the resources released on shutdown.
type Consumer struct {
	Handler *sqsconsumer.Handler

	redis *goredis.Client
}

// Close releases the Redis client.
func (c *Consumer) Close() {
	if c.redis != nil {
		_ = c.redis.Close()
	}
}

// New wires everything.
// Dependency order: redis + ses → adapters → usecase → handler.
func New(ctx context.Context, cfg config.Config) (*Consumer, error) {
	redisClient, err := notifredis.NewClient(ctx, cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		return nil, err
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		_ = redisClient.Close()
		return nil, err
	}

	renderer := template.NewRenderer(template.Links{
		AppBase:              cfg.App.AppBase,
		MagicTokenTTLMinutes: cfg.App.MagicTokenTTLMinutes,
	})

	uc := usecase.NewNotifier(
		renderer,
		ses.NewSender(awsses.NewFromConfig(awsCfg), cfg.Email.FromAddress, cfg.Email.ConfigurationSet),
		notifredis.NewDedupeStore(redisClient),
	)

	return &Consumer{Handler: sqsconsumer.NewHandler(uc), redis: redisClient}, nil
}
