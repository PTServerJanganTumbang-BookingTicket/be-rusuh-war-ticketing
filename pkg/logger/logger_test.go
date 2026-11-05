package logger

import (
	"log/slog"
	"testing"
)

func TestSetup_JSON(t *testing.T) {
	Setup("info", "json")
	// Verify the default logger was replaced (no panic, no error).
	slog.Info("test json logger")
}

func TestSetup_Text(t *testing.T) {
	Setup("debug", "text")
	slog.Debug("test text logger")
}

func TestSetup_WarnLevel(t *testing.T) {
	Setup("warn", "json")
	slog.Warn("test warn level")
}

func TestSetup_ErrorLevel(t *testing.T) {
	Setup("error", "json")
	slog.Error("test error level")
}

func TestSetup_DefaultsToInfo(t *testing.T) {
	// Unknown level → Info
	Setup("unknown", "json")
	slog.Info("test default level")
}
