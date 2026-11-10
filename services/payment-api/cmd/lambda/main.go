package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/delivery"
)

func main() {
	dotenv.LoadEnv()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", slog.Any("err", err))
		os.Exit(1)
	}

	// Wired once per container, not per invocation: the pool and the Redis
	// client are the expensive part, and rebuilding them on every request
	// would exhaust database connections under a flash crowd.
	server, err := delivery.New(context.Background(), cfg)
	if err != nil {
		slog.Error("Failed to wire dependencies", slog.Any("err", err))
		os.Exit(1)
	}

	lambda.Start(httpadapter.NewV2(server.Handler).ProxyWithContext)
}
