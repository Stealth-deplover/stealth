// Command worker is the isolated Functions execution worker. It owns the
// Docker socket and must run as a separately hardened service; the API never
// starts user processes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/adminnotification"
	"github.com/Stealth-deplover/stealth/internal/agentrunner"
	"github.com/Stealth-deplover/stealth/internal/appbuilder"
	"github.com/Stealth-deplover/stealth/internal/appstore"
	"github.com/Stealth-deplover/stealth/internal/artifactcleanup"
	"github.com/Stealth-deplover/stealth/internal/buildinfo"
	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/cloudflareimport"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionrunner"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/functionstore"
	"github.com/Stealth-deplover/stealth/internal/ingress"
	"github.com/Stealth-deplover/stealth/internal/mailer"
	"github.com/Stealth-deplover/stealth/internal/messagingrunner"
	"github.com/Stealth-deplover/stealth/internal/monitoring"
	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/Stealth-deplover/stealth/internal/realtime"
	"github.com/Stealth-deplover/stealth/internal/realtimepublisher"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/runtime"
	"github.com/Stealth-deplover/stealth/internal/sitestore"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
	"github.com/Stealth-deplover/stealth/internal/webhookrunner"
	"github.com/Stealth-deplover/stealth/internal/workersupervisor"
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
	if err := cfg.ValidateApps(); err != nil {
		logger.Error("Apps configuration error", "error", err)
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
	resources, err := runtime.Open(ctx, cfg, runtime.OpenOptions{
		WithRedis:       true,
		ApplyMigrations: true,
	})
	if err != nil {
		logger.Error("runtime resource configuration error", "error", err)
		os.Exit(1)
	}
	defer resources.Close()
	pool := resources.Pool
	redisClient := resources.Redis
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
	appArtifactStore, err := appstore.New(cfg.StorageRoot, cfg.AppsMaxSourceArchiveBytes, cfg.AppsMaxImageArchiveBytes)
	if err != nil {
		logger.Error("App artifact storage error", "error", err)
		os.Exit(1)
	}
	cipher, err := functionsecret.New(cfg.FunctionsSecretKey)
	if err != nil {
		logger.Error("function secret configuration error", "error", err)
		os.Exit(1)
	}
	repo := repository.NewWithDependencies(pool, repository.Dependencies{WebhookCipher: cipher, AdminCipher: cipher, CloudflareCipher: cipher})
	importLegacyCloudflareConnection(ctx, cfg.CloudflareImportFile, cipher, repo, logger)
	cloudflareReconciler, err := cloudflare.NewReconciler(repo, func(token string) (cloudflare.Client, error) {
		return cloudflare.NewClient(token, cfg.CloudflareAPIBaseURL, http.DefaultClient)
	}, cfg.CloudflareReconcileInterval, logger)
	if err != nil {
		logger.Error("Cloudflare routing reconciler configuration error", "error", err)
		os.Exit(1)
	}
	platformRouteReconciler, err := ingress.New(repo, cfg.TraefikGeneratedDir, cfg.TraefikReloadFile, cfg.PlatformRouteReconcileInterval, logger)
	if err != nil {
		logger.Error("platform route reconciler configuration error", "error", err)
		os.Exit(1)
	}
	telemetryStore, telemetryErr := telemetry.New(telemetry.Config{
		Address:          cfg.TelemetryClickHouseAddr,
		Database:         cfg.TelemetryClickHouseDatabase,
		Username:         cfg.TelemetryClickHouseUser,
		Password:         cfg.TelemetryClickHousePassword,
		MaxQueryDuration: cfg.TelemetryMaxQueryDuration,
		MaxQueryRange:    cfg.TelemetryMaxQueryRange,
		MaxQueryRows:     cfg.TelemetryMaxQueryRows,
		Retention:        cfg.TelemetryRetention,
	})
	if telemetryErr != nil && !errors.Is(telemetryErr, telemetry.ErrDisabled) {
		logger.Warn("telemetry alert store configuration error", "error", telemetryErr)
	}
	if telemetryStore != nil {
		defer telemetryStore.Close()
	}
	var userStorage artifactcleanup.Cleaner
	if cfg.StorageDriver == "s3" {
		userStorage, err = storage.NewS3(storage.S3Options{
			Endpoint:       cfg.StorageS3Endpoint,
			Region:         cfg.StorageS3Region,
			Bucket:         cfg.StorageS3Bucket,
			AccessKey:      cfg.StorageS3AccessKey,
			SecretKey:      cfg.StorageS3SecretKey,
			UseSSL:         cfg.StorageS3UseSSL,
			ForcePathStyle: cfg.StorageS3PathStyle,
			Prefix:         cfg.StorageS3Prefix,
			StagingRoot:    cfg.StorageS3StagingRoot,
		}, cfg.StorageMaxFileSize)
	} else {
		userStorage, err = storage.New(cfg.StorageRoot, cfg.StorageMaxFileSize)
	}
	if err != nil {
		logger.Error("user artifact cleanup storage configuration error", "error", err)
		userStorage = nil
	}
	artifactCleanupWorker, err := artifactcleanup.New(repo, artifactcleanup.Stores{
		Storage:      userStorage,
		Functions:    store,
		SiteArchives: siteSourceStore,
		Sites:        sitePublicStore,
		AppSources:   appArtifactStore.Sources,
		AppImages:    appArtifactStore.Images,
	}, cfg.FunctionsWorkerID, logger)
	if err != nil {
		logger.Error("artifact cleanup worker configuration error", "error", err)
		os.Exit(1)
	}
	artifactCleanupWorker.PollInterval = cfg.FunctionsRunnerPoll
	artifactCleanupWorker.LeaseAge = cfg.FunctionsRunnerLeaseAge
	realtimePublisher, err := realtimepublisher.New(repo, realtime.NewBroker(redisClient), cfg.FunctionsWorkerID, logger)
	if err != nil {
		logger.Error("realtime publisher configuration error", "error", err)
		os.Exit(1)
	}
	realtimePublisher.PollInterval = cfg.FunctionsRunnerPoll
	realtimePublisher.LeaseAge = cfg.FunctionsRunnerLeaseAge
	monitorWorker, err := monitoring.NewWorker(repo, cipher, cfg.FunctionsWorkerID, logger)
	if err != nil {
		logger.Error("admin monitoring worker configuration error", "error", err)
		os.Exit(1)
	}
	if telemetryStore != nil {
		telemetryAlerts, alertErr := monitoring.NewTelemetryAlertEvaluator(repo, telemetryStore, logger)
		if alertErr != nil {
			logger.Warn("telemetry alert evaluator is unavailable", "error", alertErr)
		} else {
			monitorWorker.TelemetryAlerts = telemetryAlerts
		}
	}
	monitorWorker.PollInterval = cfg.FunctionsRunnerPoll
	monitorWorker.LeaseAge = cfg.FunctionsRunnerLeaseAge
	notificationWorker, err := adminnotification.New(repo, cipher, mailer.NewFromConfig(cfg, logger), cfg.FunctionsWorkerID, logger)
	if err != nil {
		logger.Error("admin notification worker configuration error", "error", err)
		os.Exit(1)
	}
	notificationWorker.PollInterval = cfg.FunctionsRunnerPoll
	notificationWorker.LeaseAge = cfg.FunctionsRunnerLeaseAge
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
	appBuildWorker, err := appbuilder.New(repo, appArtifactStore, &appbuilder.BuildKitClient{Address: cfg.AppsBuildkitAddress}, cfg.FunctionsWorkerID, cfg.AppsBuildStagingRoot, logger)
	if err != nil {
		logger.Error("App build worker configuration error", "error", err)
		os.Exit(1)
	}
	appBuildWorker.PollInterval = cfg.AppsBuildPollInterval
	appBuildWorker.LeaseAge = cfg.AppsBuildLeaseAge
	appBuildWorker.BuildTimeout = cfg.AppsBuildTimeout
	appBuildWorker.ArchiveLimit.MaxBytes = cfg.AppsMaxExpandedSourceBytes
	appBuildWorker.ArchiveLimit.MaxEntry = cfg.AppsMaxExpandedSourceBytes
	appBuildWorker.ArchiveLimit.MaxFiles = cfg.AppsMaxSourceFiles
	appBuildWorker.ArchiveLimit.MaxCompressed = cfg.AppsMaxSourceArchiveBytes
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
		logger.Info("functions runner is disabled; independent App build, webhook, and messaging workers remain active")
		workerContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		appBuildWorker.Metrics = observability.NewWorkerMetrics()
		realtimePublisher.Metrics = appBuildWorker.Metrics
		metricsServer := &http.Server{
			Addr:              cfg.FunctionsRunnerMetricsAddress,
			Handler:           workerMetricsHandler(appBuildWorker.Metrics.Handler(), cfg.MetricsToken),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		registrations := []workersupervisor.Registration{
			{Name: "Cloudflare routing reconciler", Runner: cloudflareReconciler},
			{Name: "platform route reconciler", Runner: platformRouteReconciler},
			{Name: "artifact cleanup worker", Runner: artifactCleanupWorker},
			{Name: "App build worker", Runner: appBuildWorker},
			{Name: "realtime publisher", Runner: realtimePublisher},
			{Name: "webhook worker", Runner: webhookWorker},
			{Name: "messaging worker", Runner: messagingWorker},
			{Name: "admin monitoring worker", Runner: monitorWorker},
			{Name: "admin notification worker", Runner: notificationWorker},
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
	appBuildWorker.Metrics = worker.Metrics
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
		{Name: "Cloudflare routing reconciler", Runner: cloudflareReconciler},
		{Name: "platform route reconciler", Runner: platformRouteReconciler},
		{Name: "artifact cleanup worker", Runner: artifactCleanupWorker},
		{Name: "function worker", Runner: worker},
		{Name: "site worker", Runner: siteWorker},
		{Name: "App build worker", Runner: appBuildWorker},
		{Name: "realtime publisher", Runner: realtimePublisher},
		{Name: "webhook worker", Runner: webhookWorker},
		{Name: "messaging worker", Runner: messagingWorker},
		{Name: "admin monitoring worker", Runner: monitorWorker},
		{Name: "admin notification worker", Runner: notificationWorker},
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

type legacyCloudflareImportRepository interface {
	CloudflareRoutingStatus(context.Context) (domain.CloudflareRoutingStatus, error)
	MarkCloudflareConnectionUnavailable(context.Context, string) error
	ImportCloudflareConnectionOnce(context.Context, repository.CloudflareConnectionInput, string) (bool, error)
}

func importLegacyCloudflareConnection(ctx context.Context, artifactFile string, cipher *functionsecret.Cipher, repo legacyCloudflareImportRepository, logger *slog.Logger) {
	if repo == nil || cipher == nil || strings.TrimSpace(artifactFile) == "" {
		return
	}
	status, err := repo.CloudflareRoutingStatus(ctx)
	if err != nil {
		logger.Warn("Cloudflare import status could not be read", "error", err)
		return
	}
	if status.Configured {
		return
	}
	envelope, err := cloudflareimport.Read(artifactFile, cipher)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		logger.Warn("narrow Cloudflare import artifact could not be loaded", "reason", err)
		return
	}
	if envelope.State == cloudflareimport.StateReconnectRequired {
		reason := "Encrypted setup state contained incomplete Cloudflare configuration; reconnect as an Instance Owner using the existing account and tunnel ID."
		if markErr := repo.MarkCloudflareConnectionUnavailable(ctx, reason); markErr != nil {
			logger.Warn("Cloudflare connection remains unavailable", "error", markErr)
		}
		logger.Warn("Cloudflare import requires owner reconnection", "reason", "legacy tunnel binding is incomplete")
		return
	}
	input := repository.CloudflareConnectionInput{
		AccountID: envelope.AccountID, ConsoleZoneID: envelope.ConsoleZoneID, ConsoleHostname: envelope.ConsoleHostname,
		TunnelID: envelope.TunnelID, TunnelName: envelope.TunnelName, ConsoleRecordID: envelope.ConsoleRecordID,
		APIToken: strings.TrimSpace(envelope.APIToken),
	}
	reason := "Encrypted Cloudflare import did not contain a recoverable API token; reconnect as an Instance Owner."
	imported, err := repo.ImportCloudflareConnectionOnce(ctx, input, reason)
	if err != nil {
		logger.Warn("Cloudflare setup-state import failed", "error", err)
		return
	}
	if imported && input.APIToken != "" {
		logger.Info("Cloudflare connection imported from narrow encrypted setup artifact", "tunnel_id", envelope.TunnelID)
	} else if imported {
		logger.Warn("Cloudflare import has no recoverable API token; owner reconnection is required")
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
