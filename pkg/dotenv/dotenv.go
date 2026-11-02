package dotenv

import (
	"log/slog"

	"github.com/joho/godotenv"
)

// LoadEnv loads the .env file into the process environment if present.
// A missing file is not an error: real environments inject variables directly.
func LoadEnv() {
	if err := godotenv.Load(); err != nil {
		slog.Debug("no .env file loaded", slog.Any("err", err))
	}
}
