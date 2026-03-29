package logger

import (
	"log/slog"
	"os"
)

// New creates a slog.Logger for the agent.
// No DB, no audit — just structured stdout logging.
func New(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	l := slog.New(handler)
	slog.SetDefault(l)
	return l
}
