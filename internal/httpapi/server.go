package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/functionstore"
	"github.com/Stealth-deplover/stealth/internal/gitarchive"
	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/mailer"
	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/realtime"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/setuphandoff"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
	"github.com/Stealth-deplover/stealth/internal/sitestore"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20
const maxMultipartOverhead = 2 << 20

type Server struct {
	config            config.Config
	repo              *repository.Repository
	bootstrap         repository.BootstrapStore
	logger            *slog.Logger
	limiter           ratelimit.Limiter
	storage           storage.BlobStore
	storageReady      bool
	functions         *functionstore.Store
	functionCipher    *functionsecret.Cipher
	functionsReady    bool
	sites             *sitestore.Store
	siteArchives      *functionstore.Store
	siteGitFetcher    gitarchive.SourceFetcher
	siteGitSlots      chan struct{}
	sitesReady        bool
	metrics           *observability.APIMetrics
	realtimeSlots     chan struct{}
	realtimeBroker    *realtime.Broker
	authEmailSender   mailer.AuthSender
	githubClient      githubauth.Client
	githubFlowMu      sync.Mutex
	setupState        setupstate.Store
	setupHandoff      setuphandoff.Store
	setupEngine       *installengine.Engine
	setupRunner       installengine.CommandRunner
	githubManifest    githubauth.ManifestClient
	cloudflareOAuth   CloudflareOAuthClient
	cloudflareFactory CloudflareClientFactory
	setupEvents       *setupEventHub
	setupMu           sync.Mutex
}

// Dependencies carries the collaborators the console API accepts from the
// composition root. Zero values select the behavior of the previous New
// constructor: an open (no-op) auth limiter for embedded/test setups, the
// strict provider Git fetcher, and config-driven email delivery. Tests
// populate individual fields to inject fakes without layered constructor
// variants.
type Dependencies struct {
	AuthLimiter    ratelimit.Limiter
	SiteGitFetcher gitarchive.SourceFetcher
	// EmailSender is the low-level delivery transport. Authentication flows
	// wrap it in mailer.AuthSender; project messaging uses it directly.
	EmailSender       mailer.Sender
	RealtimeBroker    *realtime.Broker
	GitHubClient      githubauth.Client
	BootstrapStore    repository.BootstrapStore
	SetupState        setupstate.Store
	SetupHandoff      setuphandoff.Store
	InstallEngine     *installengine.Engine
	InstallRunner     installengine.CommandRunner
	GitHubManifest    githubauth.ManifestClient
	CloudflareOAuth   CloudflareOAuthClient
	CloudflareFactory CloudflareClientFactory
}

// New builds the console API with production dependencies.
func New(cfg config.Config, repo *repository.Repository, logger *slog.Logger) http.Handler {
	return NewWithDependencies(cfg, repo, logger, Dependencies{})
}

// NewWithDependencies builds the console API with injectable collaborators.
// Infrastructure stores are created from the (defaulted) config; failures are
// logged and the affected capability reports not-ready through /readyz
// instead of aborting startup.
func NewWithDependencies(cfg config.Config, repo *repository.Repository, logger *slog.Logger, deps Dependencies) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if deps.AuthLimiter == nil {
		deps.AuthLimiter = ratelimit.NoopLimiter{}
	}
	if deps.SiteGitFetcher == nil {
		deps.SiteGitFetcher = gitarchive.NewFetcher()
	}
	cfg = cfg.WithDefaults()
	if deps.EmailSender == nil {
		deps.EmailSender = mailer.NewFromConfig(cfg, logger)
	}
	authEmailSender := mailer.NewAuthSender(deps.EmailSender)
	if deps.GitHubClient == nil {
		deps.GitHubClient = githubauth.NewClient(nil)
	}
	bootstrapStore := deps.BootstrapStore
	if bootstrapStore == nil && repo != nil {
		bootstrapStore = repo.BootstrapStore()
	}
	var storageStore storage.BlobStore
	var storageErr error
	if strings.EqualFold(strings.TrimSpace(cfg.StorageDriver), "s3") {
		storageStore, storageErr = storage.NewS3(storage.S3Options{
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
		storageStore, storageErr = storage.New(cfg.StorageRoot, cfg.StorageMaxFileSize)
	}
	if storageErr != nil {
		logger.Error("storage configuration error", "error", storageErr)
	}
	functionRoot := filepath.Join(cfg.StorageRoot, "functions")
	functionStore, functionStoreErr := functionstore.New(functionRoot, cfg.FunctionsMaxArtifactSize)
	if functionStoreErr != nil {
		logger.Error("function artifact storage configuration error", "error", functionStoreErr)
	}
	siteRoot := filepath.Join(cfg.StorageRoot, "sites")
	siteStore, siteStoreErr := sitestore.New(siteRoot)
	if siteStoreErr != nil {
		logger.Error("site artifact storage configuration error", "error", siteStoreErr)
	}
	siteArchiveRoot := filepath.Join(cfg.StorageRoot, "site-archives")
	siteArchiveStore, siteArchiveErr := functionstore.New(siteArchiveRoot, cfg.SitesMaxArtifactSize)
	if siteArchiveErr != nil {
		logger.Error("site upload staging storage configuration error", "error", siteArchiveErr)
	}
	functionCipher, functionCipherErr := functionsecret.New(cfg.FunctionsSecretKey)
	if functionCipherErr != nil {
		logger.Error("function secret configuration error", "error", functionCipherErr)
	}
	functionsReady := functionStoreErr == nil && functionCipherErr == nil && cfg.FunctionsMaxArtifactSize > 0 && cfg.FunctionsDefaultQuotaBytes >= cfg.FunctionsMaxArtifactSize
	sitesReady := siteStoreErr == nil && siteArchiveErr == nil && cfg.SitesMaxArtifactSize > 0 && cfg.SitesMaxExpandedBytes > 0 && cfg.SitesMaxFiles > 0
	setupStateStore := deps.SetupState
	if setupStateStore == nil && cfg.SetupMode && functionCipher != nil && cfg.SetupStateFile != "" {
		var setupErr error
		setupStateStore, setupErr = setupstate.NewFileStore(cfg.SetupStateFile, functionCipher)
		if setupErr != nil {
			logger.Error("setup state configuration error", "error", setupErr)
		}
	}
	setupHandoffStore := deps.SetupHandoff
	if setupHandoffStore == nil && functionCipher != nil && cfg.SetupHandoffFile != "" {
		var handoffErr error
		setupHandoffStore, handoffErr = setuphandoff.NewFileStore(cfg.SetupHandoffFile, functionCipher)
		if handoffErr != nil {
			logger.Error("setup handoff configuration error", "error", handoffErr)
		}
	}
	storageReady := storageErr == nil
	installRunner := deps.InstallRunner
	if installRunner == nil {
		installRunner = installengine.OSCommandRunner{}
	}
	setupEngine := deps.InstallEngine
	if setupEngine == nil {
		setupEngine = installengine.New(installengine.Options{Runner: installRunner, HTTPClient: http.DefaultClient})
	}
	githubManifest := deps.GitHubManifest
	if githubManifest == nil {
		var manifestErr error
		githubManifest, manifestErr = githubauth.NewManifestClient("", http.DefaultClient)
		if manifestErr != nil {
			logger.Error("GitHub manifest client configuration error", "error", manifestErr)
		}
	}
	cloudflareOAuth := deps.CloudflareOAuth
	if cloudflareOAuth == nil && cfg.CloudflareOAuthClientID != "" && cfg.CloudflareOAuthClientSecret != "" {
		var oauthErr error
		cloudflareOAuth, oauthErr = cloudflare.NewOAuthClient(cfg.CloudflareOAuthClientID, cfg.CloudflareOAuthClientSecret, http.DefaultClient)
		if oauthErr != nil {
			logger.Error("Cloudflare OAuth configuration error", "error", oauthErr)
		}
	}
	cloudflareFactory := deps.CloudflareFactory
	if cloudflareFactory == nil {
		cloudflareFactory = func(token string) (cloudflare.Client, error) {
			return cloudflare.NewClient(token, cfg.CloudflareAPIBaseURL, http.DefaultClient)
		}
	}
	s := &Server{config: cfg, repo: repo, bootstrap: bootstrapStore, logger: logger, limiter: deps.AuthLimiter, storage: storageStore, storageReady: storageReady, functions: functionStore, functionCipher: functionCipher, functionsReady: functionsReady, sites: siteStore, siteArchives: siteArchiveStore, siteGitFetcher: deps.SiteGitFetcher, siteGitSlots: make(chan struct{}, cfg.SitesGitFetchConcurrency), sitesReady: sitesReady, metrics: observability.NewAPIMetrics(), realtimeSlots: make(chan struct{}, 256), realtimeBroker: deps.RealtimeBroker, authEmailSender: authEmailSender, githubClient: deps.GitHubClient, setupState: setupStateStore, setupHandoff: setupHandoffStore, setupEngine: setupEngine, setupRunner: installRunner, githubManifest: githubManifest, cloudflareOAuth: cloudflareOAuth, cloudflareFactory: cloudflareFactory, setupEvents: newSetupEventHub()}
	if cfg.SetupMode && setupStateStore != nil {
		go s.resumeSetupLifecycle()
	}
	return s.routes()
}

type CloudflareOAuthClient interface {
	AuthorizationURL(string, string, []string) (string, error)
	Exchange(context.Context, string, string) (cloudflare.OAuthToken, error)
}

type CloudflareClientFactory func(string) (cloudflare.Client, error)

type contextKey string

const accountContextKey contextKey = "account"
const sessionContextKey contextKey = "session"
const requestIDContextKey contextKey = "request-id"
const projectUserContextKey contextKey = "project-user"
const projectUserSessionContextKey contextKey = "project-user-session"
const projectActorContextKey contextKey = "project-actor"
const setupSessionContextKey contextKey = "setup-session"

type projectActorKind string

const (
	consoleProjectActor projectActorKind = "console"
	apiKeyProjectActor  projectActorKind = "api_key"
)

type projectActor struct {
	kind     projectActorKind
	apiKeyID uuid.UUID
	scopes   []string
}
