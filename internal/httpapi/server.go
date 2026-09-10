package httpapi

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/functionstore"
	"github.com/Stealth-deplover/stealth/internal/gitarchive"
	"github.com/Stealth-deplover/stealth/internal/mailer"
	"github.com/Stealth-deplover/stealth/internal/observability"
	"github.com/Stealth-deplover/stealth/internal/ratelimit"
	"github.com/Stealth-deplover/stealth/internal/realtime"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/sitestore"
	"github.com/Stealth-deplover/stealth/internal/storage"
	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20
const maxMultipartOverhead = 2 << 20

type Server struct {
	config         config.Config
	repo           *repository.Repository
	logger         *slog.Logger
	limiter        ratelimit.Limiter
	storage        storage.BlobStore
	storageReady   bool
	functions      *functionstore.Store
	functionCipher *functionsecret.Cipher
	functionsReady bool
	sites          *sitestore.Store
	siteArchives   *functionstore.Store
	siteGitFetcher gitarchive.SourceFetcher
	siteGitSlots   chan struct{}
	sitesReady     bool
	metrics        *observability.APIMetrics
	realtimeSlots  chan struct{}
	realtimeBroker *realtime.Broker
	emailSender    mailer.Sender
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
	EmailSender    mailer.Sender
	RealtimeBroker *realtime.Broker
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
	s := &Server{config: cfg, repo: repo, logger: logger, limiter: deps.AuthLimiter, storage: storageStore, storageReady: storageErr == nil, functions: functionStore, functionCipher: functionCipher, functionsReady: functionsReady, sites: siteStore, siteArchives: siteArchiveStore, siteGitFetcher: deps.SiteGitFetcher, siteGitSlots: make(chan struct{}, cfg.SitesGitFetchConcurrency), sitesReady: sitesReady, metrics: observability.NewAPIMetrics(), realtimeSlots: make(chan struct{}, 256), realtimeBroker: deps.RealtimeBroker, emailSender: deps.EmailSender}
	return s.routes()
}

type contextKey string

const accountContextKey contextKey = "account"
const sessionContextKey contextKey = "session"
const requestIDContextKey contextKey = "request-id"
const projectUserContextKey contextKey = "project-user"
const projectUserSessionContextKey contextKey = "project-user-session"
const projectActorContextKey contextKey = "project-actor"

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
