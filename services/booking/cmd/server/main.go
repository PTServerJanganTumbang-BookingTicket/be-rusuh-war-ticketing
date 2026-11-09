package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/dotenv"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/httpserver"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/logger"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/delivery"
)

func main() {
	dotenv.LoadEnv()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", slog.Any("err", err))
		os.Exit(1)
	}

	logger.Setup(cfg.Log.Level, cfg.Log.Format)

	handler, deps, err := delivery.New(context.Background(), cfg)
	if err != nil {
		slog.Error("Failed to connect", slog.Any("err", err))
		os.Exit(1)
	}
	defer deps.Close()

	httpserver.Run(cfg.App.Port, handler, func() {
		slog.Info("Server starting", slog.Int("port", cfg.App.Port))
	})
}
