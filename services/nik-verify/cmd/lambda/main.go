package main

import (
	"context"
	"encoding/json"
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

	// server.Events() implements lambda.Handler (Invoke(context.Context, []byte) ([]byte, error)).
	// lambda.Start detects the Handler interface and calls Invoke directly,
	// bypassing the reflection-based JSON dispatch that causes
	// "cannot unmarshal object into Go value of type []uint8" when the
	// handler is passed as a function value with a []byte parameter.
	//
	// json.RawMessage is used as the parameter type rather than []byte because
	// lambda.Start in aws-lambda-go v1.55+ uses generic dispatch and calls
	// json.Unmarshal(event, &param). For []byte this fails on a JSON object
	// ("cannot unmarshal object into []uint8"). For json.RawMessage it succeeds:
	// the json package treats RawMessage specially and stores the raw bytes as-is.
	lambda.Start(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		out, err := server.Events().Invoke(ctx, []byte(raw))
		if err != nil {
			return nil, err
		}
		return json.RawMessage(out), nil
	})
}
