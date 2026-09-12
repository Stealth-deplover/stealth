// Command worker is the isolated Functions execution worker. It owns the
// Docker socket and must run as a separately hardened service; the API never
// starts user processes.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/agentrunner"
	"github.com/Stealth-deplover/stealth/internal/buildinfo"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/functionrunner"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/functionstore"
	"github.com/Stealth-deplover/stealth/internal/messagingrunner"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/Stealth-deplover/stealth/internal/realtime"
	"github.com/Stealth-deplover/stealth/internal/realtimepublisher"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/sitestore"
	"github.com/Stealth-deplover/stealth/internal/webhookrunner"
	"github.com/Stealth-deplover/stealth/internal/workersupervisor"
	"github.com/jackc/pgx/v5/pgxpool"
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
	if err := cfg.ValidateSites(); err != nil {
		logger.Error("sites configuration error", "error", err)
		os.Exit(1)
	}
	logger.Info("starting worker", "version", buildinfo.Version, "commit", buildinfo.Commit, "build_time", buildinfo.BuildTime)
	telemetryShutdown, err := observability.Setup(context.Background(), observability.TracerConfig{
		Endpoint:    cfg.TelemetryOTLPEndpoint,
		ServiceName: firstNonEmpty(cfg.TelemetryServiceName, "stealth-worker"),
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
	store, err := functionstore.New(filepath.Join(cfg.StorageRoot, "functions"), cfg.FunctionsMaxArtifactSize)
	if err != nil {
		logger.Error("function artifact storage error", "error", err)
		os.Exit(1)
	}
	siteSourceStore, err := functionstore.New(filepath.Join(cfg.StorageRoot, "site-archives"), cfg.SitesMaxArtifactSize)
	if err != nil {
		logger.Error("site source artifact storage error", "error", err)
		os.Exit(1)
	}
	sitePublicStore, err := sitestore.New(filepath.Join(cfg.StorageRoot, "sites"))
	if err != nil {
		logger.Error("site artifact storage error", "error", err)
		os.Exit(1)
	}
	cipher, err := functionsecret.New(cfg.FunctionsSecretKey)
	if err != nil {
		logger.Error("function secret configuration error", "error", err)
		os.Exit(1)
	}
	repo := repository.NewWithDependencies(pool, repository.Dependencies{WebhookCipher: cipher})
	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("redis configuration error", "error", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()
	realtimePublisher, err := realtimepublisher.New(repo, realtime.NewBroker(redisClient), cfg.FunctionsWorkerID, logger)
	if err != nil {
		logger.Error("realtime publisher configuration error", "error", err)
		os.Exit(1)
	}
	realtimePublisher.PollInterval = cfg.FunctionsRunnerPoll
	realtimePublisher.LeaseAge = cfg.FunctionsRunnerLeaseAge
	webhookWorker, err := webhookrunner.New(repo, cipher, cfg.FunctionsWorkerID, logger)
	if err != nil {
		logger.Error("webhook worker configuration error", "error", err)
		os.Exit(1)
	}
	webhookWorker.PollInterval = cfg.FunctionsRunnerPoll
	webhookWorker.LeaseAge = cfg.FunctionsRunnerLeaseAge
	messagingWorker, err := messagingrunner.New(repo, cipher, cfg.FunctionsWorkerID, logger)
	if err != nil {
		logger.Error("messaging worker configuration error", "error", err)
		os.Exit(1)
	}
	messagingWorker.PollInterval = cfg.FunctionsRunnerPoll
	messagingWorker.LeaseAge = cfg.FunctionsRunnerLeaseAge
	var agentWorker *agentrunner.Worker
	if cfg.AgentRunnerEnabled {
		// Provider adapters are deliberately opt-in and process-local. This
		// release wires the durable lifecycle but does not invent a provider
		// credential or execute a prompt without a trusted adapter.
		agentWorker, err = agentrunner.New(repo, cfg.FunctionsWorkerID, agentrunner.NewRegistry(), logger)
		if err != nil {
			logger.Error("Agent worker configuration error", "error", err)
			os.Exit(1)
		}
		agentWorker.PollInterval = cfg.FunctionsRunnerPoll
		agentWorker.LeaseAge = cfg.FunctionsRunnerLeaseAge
		agentWorker.ExecutionTimeout = cfg.AgentRunnerExecutionTimeout
		logger.Warn("Agent runner enabled without provider adapters; queued Agent runs will remain queued")
	}
	if !cfg.FunctionsRunnerEnabled {
		logger.Info("functions runner is disabled; webhook and messaging runners remain active")
		workerContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		registrations := []workersupervisor.Registration{
			{Name: "realtime publisher", Runner: realtimePublisher},
			{Name: "webhook worker", Runner: webhookWorker},
			{Name: "messaging worker", Runner: messagingWorker},
		}
		if agentWorker != nil {
			registrations = append(registrations, workersupervisor.Registration{Name: "Agent worker", Runner: agentWorker})
		}
		if err := workersupervisor.Run(workerContext, registrations...); err != nil {
			logger.Error("worker stopped with error", "error", err)
			os.Exit(1)
		}
		return
	}
	executor := functionrunner.NewDockerExecutor(cfg.FunctionsRunnerStagingVolume)
	executor.HelperImage = cfg.FunctionsRunnerHelperImage
	executor.NodeImage = cfg.FunctionsRunnerNodeImage
	executor.PythonImage = cfg.FunctionsRunnerPythonImage
	executor.GoImage = cfg.FunctionsRunnerGoImage
	worker, err := functionrunner.NewWorker(repo, store, cipher, executor, cfg.FunctionsWorkerID, cfg.FunctionsRunnerStagingRoot, logger)
	if err != nil {
		logger.Error("worker configuration error", "error", err)
		os.Exit(1)
	}
	worker.PollInterval = cfg.FunctionsRunnerPoll
	worker.LeaseAge = cfg.FunctionsRunnerLeaseAge
	worker.BuildTimeout = cfg.FunctionsRunnerBuildTimeout
	worker.ArchiveLimit.MaxCompressed = cfg.FunctionsMaxArtifactSize
	siteWorker, err := functionrunner.NewSiteWorker(repo, siteSourceStore, sitePublicStore, executor, cfg.FunctionsWorkerID, cfg.FunctionsRunnerStagingRoot, logger)
	if err != nil {
		logger.Error("site worker configuration error", "error", err)
		os.Exit(1)
	}
	siteWorker.PollInterval = cfg.FunctionsRunnerPoll
	siteWorker.LeaseAge = cfg.FunctionsRunnerLeaseAge
	siteWorker.BuildTimeout = cfg.FunctionsRunnerBuildTimeout
	siteWorker.ArchiveLimit.MaxBytes = cfg.SitesMaxExpandedBytes
	siteWorker.ArchiveLimit.MaxEntry = cfg.SitesMaxExpandedBytes
	siteWorker.ArchiveLimit.MaxFiles = cfg.SitesMaxFiles
	siteWorker.ArchiveLimit.MaxCompressed = cfg.SitesMaxArtifactSize
	siteWorker.Metrics = worker.Metrics
	if agentWorker != nil {
		agentWorker.Metrics = worker.Metrics
	}
	realtimePublisher.Metrics = worker.Metrics
	workerContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	metricsServer := &http.Server{
		Addr:              cfg.FunctionsRunnerMetricsAddress,
		Handler:           workerMetricsHandler(worker.MetricsHandler(), cfg.MetricsToken),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	registrations := []workersupervisor.Registration{
		{Name: "function worker", Runner: worker},
		{Name: "site worker", Runner: siteWorker},
		{Name: "realtime publisher", Runner: realtimePublisher},
		{Name: "webhook worker", Runner: webhookWorker},
		{Name: "messaging worker", Runner: messagingWorker},
		{Name: "worker metrics", Runner: workersupervisor.RunnerFunc(func(ctx context.Context) error {
			return serveWorkerMetrics(ctx, metricsServer, logger)
		})},
	}
	if agentWorker != nil {
		registrations = append(registrations, workersupervisor.Registration{Name: "Agent worker", Runner: agentWorker})
	}
	if err := workersupervisor.Run(workerContext, registrations...); err != nil {
		logger.Error("worker stopped with error", "error", err)
		os.Exit(1)
	}
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// workerMetricsHandler keeps the private Prometheus listener useful to an
// orchestrator as well as a scraper. The health endpoint intentionally only
// reports that the worker process and listener are alive; the process exits
// when its queue loops fail, so a supervisor can restart it from that signal.
func workerMetricsHandler(metrics http.Handler, metricsToken string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", observability.ProtectedMetricsHandler(metrics, metricsToken))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(buildinfo.Current())
	})
	return mux
}
