// Command migrate applies the embedded PostgreSQL migrations and exits. API
// and worker startup still run the same idempotent check as a safety net, but
// production releases should call this command explicitly before starting
// application processes.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/runtime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalContext, 10*time.Minute)
	defer cancel()

	logger.Info("applying database migrations")
	resources, err := runtime.Open(ctx, cfg, runtime.OpenOptions{ApplyMigrations: true})
	if err != nil {
		logger.Error("runtime resource configuration error", "error", err)
		os.Exit(1)
	}
	defer resources.Close()
	logger.Info("database migrations complete")
}
