package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

func serveWorkerMetrics(ctx context.Context, server *http.Server, logger *slog.Logger) error {
	if server == nil {
		return errors.New("worker metrics server is not configured")
	}
	errCh := make(chan error, 1)
	go func() {
		if logger != nil {
			logger.Info("worker metrics listening", "address", server.Addr)
		}
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr := server.Shutdown(shutdownContext)
		shutdownCancel()
		serveErr := <-errCh
		if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
			return shutdownErr
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	}
}
