package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/tidefly-oss/tidefly-agent/internal/agent"
	"github.com/tidefly-oss/tidefly-agent/internal/config"
)

type App struct {
	cfg     *config.Config
	version string
	agent   *agent.Agent
}

func NewApp(version string) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	setupLogger(cfg.Logger.Level)
	slog.Info("tidefly-agent starting", "version", version, "config", cfg.String())

	a, err := agent.New(cfg, version)
	if err != nil {
		return nil, fmt.Errorf("init agent: %w", err)
	}

	return &App{cfg: cfg, version: version, agent: a}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.agent.Run(ctx)
}

func setupLogger(level string) {
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
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})))
}
