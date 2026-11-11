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

	// Two kinds of event: API Gateway requests (the public routes) and the internal {"op":"validate"} invocation booking
	// makes directly. The handler tells them apart before the HTTP adapter sees the payload.
	// StartHandlerFunc is generic-based and does not special-case []byte — it
	// tries json.Unmarshal(event, &[]byte{}) which fails with
	// "cannot unmarshal object into Go value of type []uint8" on every
	// API Gateway event. lambda.Start uses reflection and special-cases
	// []byte, passing the raw JSON bytes as-is.
	lambda.Start(server.Events().Invoke)
}
