package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/logger"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/delivery"
)

func main() {
	dotenv.LoadEnv()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", slog.Any("err", err))
		os.Exit(1)
	}
	logger.Setup(cfg.LogLevel, cfg.LogFmt)

	// Wired once per container, not per invocation: the Redis client and the
	// pool are the expensive part, and a flash crowd would exhaust them.
	server, err := delivery.New(context.Background(), cfg)
	if err != nil {
		slog.Error("Failed to wire dependencies", slog.Any("err", err))
		os.Exit(1)
	}
	lambda.Start(httpadapter.NewV2(server.Handler).ProxyWithContext)
}
