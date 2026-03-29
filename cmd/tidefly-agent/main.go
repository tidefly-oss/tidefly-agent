package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tidefly-oss/tidefly-agent/internal/bootstrap"
)

var version = "0.1.0" // set via ldflags: -X main.version=x.y.z

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := bootstrap.NewApp(version)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	return app.Run(ctx)
}
