package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/delivery"
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

	// EventHandler implements lambda.Handler (Invoke(context.Context, []byte) ([]byte, error)).
	// lambda.StartHandler calls Invoke directly via the interface without any
	// reflection-based JSON dispatch, which avoids the
	// "cannot unmarshal object into Go value of type []uint8" error that
	// lambda.Start triggers in aws-lambda-go ≥ v1.55 when the handler takes
	// a []byte parameter (the generic dispatch no longer special-cases it).
	lambda.StartHandler(server.Events())
}
