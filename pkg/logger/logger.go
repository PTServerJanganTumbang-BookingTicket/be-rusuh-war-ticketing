// Package logger configures the process-wide slog default.
package logger

import (
	"log/slog"
	"os"
)

// Setup initialises the slog default handler.
//
// level accepts "debug", "warn" or "error"; any other value defaults to Info.
// format accepts "text"; any other value produces JSON (the production default).
//
// Call this once during start-up, after configuration has been loaded.
func Setup(level, format string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: l}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}
