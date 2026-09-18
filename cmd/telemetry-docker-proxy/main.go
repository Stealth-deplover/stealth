package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/dockermetricsproxy"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := dockermetricsproxy.Healthcheck(ctx, firstNonEmpty(os.Getenv("LISTEN_ADDR"), "127.0.0.1:2375")); err != nil {
			os.Exit(1)
		}
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := dockermetricsproxy.Run(ctx, dockermetricsproxy.ConfigFromEnv()); err != nil {
		logger.Error("Docker metrics proxy stopped", "error", err)
		os.Exit(1)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
