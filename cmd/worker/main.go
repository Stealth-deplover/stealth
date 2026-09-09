// Command worker is the isolated Functions execution worker. It owns the
// Docker socket and must run as a separately hardened service; the API never
// starts user processes.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/agentrunner"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/functionrunner"
	"github.com/nazxf/stealth-api/internal/functionsecret"
	"github.com/nazxf/stealth-api/internal/functionstore"
	"github.com/nazxf/stealth-api/internal/messagingrunner"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/observability"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/sitestore"
	"github.com/nazxf/stealth-api/internal/webhookrunner"
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
		webhookWorkerErr := make(chan error, 1)
		go func() { webhookWorkerErr <- webhookWorker.Run(workerContext) }()
		messagingWorkerErr := make(chan error, 1)
		go func() { messagingWorkerErr <- messagingWorker.Run(workerContext) }()
		var agentWorkerErr chan error
		if agentWorker != nil {
			agentWorkerErr = make(chan error, 1)
			go func() { agentWorkerErr <- agentWorker.Run(workerContext) }()
		}
		var runErr error
		select {
		case runErr = <-webhookWorkerErr:
			stop()
			if messagingErr := <-messagingWorkerErr; messagingErr != nil && !errors.Is(messagingErr, context.Canceled) && runErr == nil {
				runErr = messagingErr
			}
			if agentErr := waitAgentWorker(agentWorkerErr); agentErr != nil && !errors.Is(agentErr, context.Canceled) && runErr == nil {
				runErr = agentErr
			}
		case runErr = <-messagingWorkerErr:
			stop()
			if webhookErr := <-webhookWorkerErr; webhookErr != nil && !errors.Is(webhookErr, context.Canceled) && runErr == nil {
				runErr = webhookErr
			}
			if agentErr := waitAgentWorker(agentWorkerErr); agentErr != nil && !errors.Is(agentErr, context.Canceled) && runErr == nil {
				runErr = agentErr
			}
		case agentErr := <-agentWorkerErr:
			stop()
			if agentErr != nil && !errors.Is(agentErr, context.Canceled) {
				runErr = agentErr
			}
			if webhookErr := <-webhookWorkerErr; webhookErr != nil && !errors.Is(webhookErr, context.Canceled) && runErr == nil {
				runErr = webhookErr
			}
			if messagingErr := <-messagingWorkerErr; messagingErr != nil && !errors.Is(messagingErr, context.Canceled) && runErr == nil {
				runErr = messagingErr
			}
		case <-workerContext.Done():
			stop()
			<-webhookWorkerErr
			<-messagingWorkerErr
			if agentWorkerErr != nil {
				<-agentWorkerErr
			}
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			logger.Error("worker stopped with error", "error", runErr)
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
	workerContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	metricsServer := &http.Server{
		Addr:              cfg.FunctionsRunnerMetricsAddress,
		Handler:           workerMetricsHandler(worker.MetricsHandler(), cfg.MetricsToken),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	metricsErr := make(chan error, 1)
	go func() {
		logger.Info("worker metrics listening", "address", cfg.FunctionsRunnerMetricsAddress)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			metricsErr <- err
		}
	}()
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(workerContext) }()
	siteWorkerErr := make(chan error, 1)
	go func() { siteWorkerErr <- siteWorker.Run(workerContext) }()
	webhookWorkerErr := make(chan error, 1)
	go func() { webhookWorkerErr <- webhookWorker.Run(workerContext) }()
	messagingWorkerErr := make(chan error, 1)
	go func() { messagingWorkerErr <- messagingWorker.Run(workerContext) }()
	var agentWorkerErr chan error
	if agentWorker != nil {
		agentWorkerErr = make(chan error, 1)
		go func() { agentWorkerErr <- agentWorker.Run(workerContext) }()
	}

	var runErr, metricsFailure error
	select {
	case runErr = <-workerErr:
		stop()
		if siteErr := <-siteWorkerErr; siteErr != nil && !errors.Is(siteErr, context.Canceled) {
			logger.Error("site worker stopped with error", "error", siteErr)
			if runErr == nil {
				runErr = siteErr
			}
		}
		if webhookErr := <-webhookWorkerErr; webhookErr != nil && !errors.Is(webhookErr, context.Canceled) {
			logger.Error("webhook worker stopped with error", "error", webhookErr)
			if runErr == nil {
				runErr = webhookErr
			}
		}
		if messagingErr := <-messagingWorkerErr; messagingErr != nil && !errors.Is(messagingErr, context.Canceled) {
			logger.Error("messaging worker stopped with error", "error", messagingErr)
			if runErr == nil {
				runErr = messagingErr
			}
		}
		if agentErr := waitAgentWorker(agentWorkerErr); agentErr != nil && !errors.Is(agentErr, context.Canceled) {
			logger.Error("Agent worker stopped with error", "error", agentErr)
			if runErr == nil {
				runErr = agentErr
			}
		}
	case siteErr := <-siteWorkerErr:
		if siteErr != nil && !errors.Is(siteErr, context.Canceled) {
			logger.Error("site worker stopped with error", "error", siteErr)
		}
		stop()
		runErr = <-workerErr
		if webhookErr := <-webhookWorkerErr; webhookErr != nil && !errors.Is(webhookErr, context.Canceled) {
			logger.Error("webhook worker stopped with error", "error", webhookErr)
			if runErr == nil {
				runErr = webhookErr
			}
		}
		if messagingErr := <-messagingWorkerErr; messagingErr != nil && !errors.Is(messagingErr, context.Canceled) {
			logger.Error("messaging worker stopped with error", "error", messagingErr)
			if runErr == nil {
				runErr = messagingErr
			}
		}
		if agentErr := waitAgentWorker(agentWorkerErr); agentErr != nil && !errors.Is(agentErr, context.Canceled) {
			logger.Error("Agent worker stopped with error", "error", agentErr)
			if runErr == nil {
				runErr = agentErr
			}
		}
	case webhookErr := <-webhookWorkerErr:
		if webhookErr != nil && !errors.Is(webhookErr, context.Canceled) {
			logger.Error("webhook worker stopped with error", "error", webhookErr)
			runErr = webhookErr
		}
		stop()
		if workerFailure := <-workerErr; workerFailure != nil && !errors.Is(workerFailure, context.Canceled) && runErr == nil {
			runErr = workerFailure
		}
		if siteFailure := <-siteWorkerErr; siteFailure != nil && !errors.Is(siteFailure, context.Canceled) && runErr == nil {
			runErr = siteFailure
		}
		if messagingFailure := <-messagingWorkerErr; messagingFailure != nil && !errors.Is(messagingFailure, context.Canceled) && runErr == nil {
			runErr = messagingFailure
		}
		if agentFailure := waitAgentWorker(agentWorkerErr); agentFailure != nil && !errors.Is(agentFailure, context.Canceled) && runErr == nil {
			runErr = agentFailure
		}
	case messagingErr := <-messagingWorkerErr:
		if messagingErr != nil && !errors.Is(messagingErr, context.Canceled) {
			logger.Error("messaging worker stopped with error", "error", messagingErr)
			runErr = messagingErr
		}
		stop()
		if workerFailure := <-workerErr; workerFailure != nil && !errors.Is(workerFailure, context.Canceled) && runErr == nil {
			runErr = workerFailure
		}
		if siteFailure := <-siteWorkerErr; siteFailure != nil && !errors.Is(siteFailure, context.Canceled) && runErr == nil {
			runErr = siteFailure
		}
		if webhookFailure := <-webhookWorkerErr; webhookFailure != nil && !errors.Is(webhookFailure, context.Canceled) && runErr == nil {
			runErr = webhookFailure
		}
		if agentFailure := waitAgentWorker(agentWorkerErr); agentFailure != nil && !errors.Is(agentFailure, context.Canceled) && runErr == nil {
			runErr = agentFailure
		}
	case agentErr := <-agentWorkerErr:
		if agentErr != nil && !errors.Is(agentErr, context.Canceled) {
			logger.Error("Agent worker stopped with error", "error", agentErr)
			runErr = agentErr
		}
		stop()
		if workerFailure := <-workerErr; workerFailure != nil && !errors.Is(workerFailure, context.Canceled) && runErr == nil {
			runErr = workerFailure
		}
		if siteFailure := <-siteWorkerErr; siteFailure != nil && !errors.Is(siteFailure, context.Canceled) && runErr == nil {
			runErr = siteFailure
		}
		if webhookFailure := <-webhookWorkerErr; webhookFailure != nil && !errors.Is(webhookFailure, context.Canceled) && runErr == nil {
			runErr = webhookFailure
		}
		if messagingFailure := <-messagingWorkerErr; messagingFailure != nil && !errors.Is(messagingFailure, context.Canceled) && runErr == nil {
			runErr = messagingFailure
		}
	case metricsFailure = <-metricsErr:
		logger.Error("worker metrics server error", "error", metricsFailure)
		stop()
		runErr = <-workerErr
		<-siteWorkerErr
		<-webhookWorkerErr
		<-messagingWorkerErr
		waitAgentWorker(agentWorkerErr)
	case <-workerContext.Done():
		runErr = <-workerErr
		<-siteWorkerErr
		<-webhookWorkerErr
		<-messagingWorkerErr
		waitAgentWorker(agentWorkerErr)
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := metricsServer.Shutdown(shutdownContext); err != nil {
		logger.Error("worker metrics shutdown error", "error", err)
	}
	if metricsFailure != nil {
		os.Exit(1)
	}
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		logger.Error("worker stopped with error", "error", runErr)
		os.Exit(1)
	}
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func waitAgentWorker(errCh <-chan error) error {
	if errCh == nil {
		return nil
	}
	return <-errCh
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
	return mux
}
