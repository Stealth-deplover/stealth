package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/buildinfo"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/functionsecret"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/observability"
	"github.com/nazxf/stealth-api/internal/ratelimit"
	"github.com/nazxf/stealth-api/internal/realtime"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/tlsmanager"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}
	if err := cfg.ValidateFunctions(); err != nil {
		logger.Error("functions configuration error", "error", err)
		os.Exit(1)
	}
	if err := cfg.ValidateStorage(); err != nil {
		logger.Error("storage configuration error", "error", err)
		os.Exit(1)
	}
	if err := cfg.ValidateSites(); err != nil {
		logger.Error("sites configuration error", "error", err)
		os.Exit(1)
	}
	logger.Info("starting API", "version", buildinfo.Version, "commit", buildinfo.Commit, "build_time", buildinfo.BuildTime)
	telemetryShutdown, err := observability.Setup(context.Background(), observability.TracerConfig{
		Endpoint:    cfg.TelemetryOTLPEndpoint,
		ServiceName: firstNonEmpty(cfg.TelemetryServiceName, "stealth-api"),
		SampleRatio: cfg.TelemetrySampleRatio,
	})
	if err != nil {
		logger.Error("telemetry configuration error", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := telemetryShutdown(shutdownContext); err != nil {
			logger.Error("telemetry shutdown error", "error", err)
		}
	}()
	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("redis configuration error", "error", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration error", "error", err)
		os.Exit(1)
	}
	poolConfig.MaxConns = cfg.DatabaseMaxConns
	poolConfig.MinConns = cfg.DatabaseMinConns
	poolConfig.MaxConnLifetime = cfg.DatabaseMaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.DatabaseMaxConnIdleTime
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		logger.Error("database connection error", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		logger.Error("migration error", "error", err)
		os.Exit(1)
	}
	// Uploads and downloads are streamed. Header parsing remains short to
	// resist slowloris clients, while body/read and write deadlines allow the
	// configured file size to traverse a slow self-hosted link.
	webhookCipher, err := functionsecret.New(cfg.FunctionsSecretKey)
	if err != nil {
		logger.Error("webhook secret configuration error", "error", err)
		os.Exit(1)
	}
	repo := repository.NewWithDependencies(pool, repository.Dependencies{WebhookCipher: webhookCipher})
	handler := httpapi.NewWithDependencies(cfg, repo, logger, httpapi.Dependencies{AuthLimiter: ratelimit.NewRedisLimiter(redisClient), RealtimeBroker: realtime.NewBroker(redisClient)})
	server := &http.Server{Addr: cfg.HTTPAddress, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	servers := []*http.Server{server}
	var tlsServer, challengeServer *http.Server
	var tlsListener, challengeListener net.Listener
	type serverResult struct {
		name string
		err  error
	}
	errCh := make(chan serverResult, 3)
	if cfg.ACMEEnabled {
		certificateManager, managerErr := tlsmanager.New(tlsmanager.Options{
			CacheDir:     filepath.Clean(cfg.ACMECertCacheDir),
			Email:        cfg.ACMEEmail,
			DirectoryURL: cfg.ACMEDirectoryURL,
			Verifier:     repo,
			StatusWriter: repo,
			Logger:       logger,
		})
		if managerErr != nil {
			logger.Error("ACME configuration error", "error", managerErr)
			os.Exit(1)
		}
		tlsServer = &http.Server{Addr: cfg.ACMETLSAddress, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
		challengeServer = &http.Server{Addr: cfg.ACMEHTTPChallengeAddress, Handler: certificateManager.HTTPHandler(nil), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 20}
		var listenErr error
		tlsListener, listenErr = net.Listen("tcp", cfg.ACMETLSAddress)
		if listenErr != nil {
			logger.Error("HTTPS listener error", "address", cfg.ACMETLSAddress, "error", listenErr)
			os.Exit(1)
		}
		challengeListener, listenErr = net.Listen("tcp", cfg.ACMEHTTPChallengeAddress)
		if listenErr != nil {
			_ = tlsListener.Close()
			logger.Error("ACME challenge listener error", "address", cfg.ACMEHTTPChallengeAddress, "error", listenErr)
			os.Exit(1)
		}
		servers = append(servers, tlsServer, challengeServer)
		tlsListener = tls.NewListener(tlsListener, certificateManager.TLSConfig())
	}
	go func() {
		logger.Info("api listening", "address", cfg.HTTPAddress)
		errCh <- serverResult{name: "api", err: server.ListenAndServe()}
	}()
	if cfg.ACMEEnabled {
		go func() {
			logger.Info("https listening", "address", cfg.ACMETLSAddress)
			errCh <- serverResult{name: "https", err: tlsServer.Serve(tlsListener)}
		}()
		go func() {
			logger.Info("ACME HTTP-01 challenge listening", "address", cfg.ACMEHTTPChallengeAddress)
			errCh <- serverResult{name: "acme-challenge", err: challengeServer.Serve(challengeListener)}
		}()
	}
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-signalContext.Done():
		logger.Info("shutdown signal received")
	case result := <-errCh:
		if !errors.Is(result.err, http.ErrServerClosed) {
			logger.Error("server error", "server", result.name, "error", result.err)
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer shutdownCancel()
		for _, runningServer := range servers {
			if shutdownErr := runningServer.Shutdown(shutdownCtx); shutdownErr != nil {
				logger.Error("shutdown error", "server", runningServer.Addr, "error", shutdownErr)
			}
		}
		if !errors.Is(result.err, http.ErrServerClosed) {
			os.Exit(1)
		}
		return
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()
	for _, runningServer := range servers {
		if err := runningServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown error", "server", runningServer.Addr, "error", err)
			os.Exit(1)
		}
	}
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
