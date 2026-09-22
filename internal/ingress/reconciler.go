// Package ingress materializes PostgreSQL-derived platform Site routes into
// the existing Traefik file provider. It never treats the generated file as
// authoritative and it owns only its single generated snapshot plus reload
// sentinel.
package ingress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/domainname"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	"log/slog"
)

const (
	GeneratedFilename          = "platform-sites.yaml"
	DefaultPlatformSiteBackend = "http://api:8082"
)

var ErrLockNotAcquired = errors.New("platform route reconciliation lock is held by another worker")

// Store is intentionally narrower than Repository. The worker needs only a
// distributed lease and the authoritative desired-state snapshot.
type Store interface {
	TryPlatformRouteReconcileLock(context.Context) (func() error, bool, error)
	ListPlatformRoutes(context.Context) ([]domain.PlatformRoute, error)
}

type Reconciler struct {
	store      Store
	outputFile string
	reloadFile string
	backendURL string
	interval   time.Duration
	logger     *slog.Logger
}

type Result struct {
	Routes        int
	LockAcquired  bool
	Changed       bool
	ReloadUpdated bool
}

func New(store Store, generatedDir, reloadFile string, interval time.Duration, logger *slog.Logger) (*Reconciler, error) {
	if store == nil {
		return nil, errors.New("platform route store is required")
	}
	generatedDir = filepath.Clean(strings.TrimSpace(generatedDir))
	reloadFile = filepath.Clean(strings.TrimSpace(reloadFile))
	if !filepath.IsAbs(generatedDir) || !filepath.IsAbs(reloadFile) || generatedDir == string(filepath.Separator) || reloadFile == string(filepath.Separator) {
		return nil, errors.New("platform route paths must be absolute non-root paths")
	}
	if pathWithin(generatedDir, reloadFile) {
		return nil, errors.New("Traefik reload sentinel must be outside the generated route directory")
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		store:      store,
		outputFile: filepath.Join(generatedDir, GeneratedFilename),
		reloadFile: reloadFile,
		backendURL: DefaultPlatformSiteBackend,
		interval:   interval,
		logger:     logger,
	}, nil
}

func pathWithin(directory, path string) bool {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

// Run performs an immediate reconstruction and then retries on a bounded
// ticker. Transient database or filesystem failures preserve the last known
// good file and do not take down unrelated worker loops.
func (r *Reconciler) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.reconcileAndLog(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAndLog(ctx)
		}
	}
}

func (r *Reconciler) reconcileAndLog(ctx context.Context) {
	result, err := r.Reconcile(ctx)
	if errors.Is(err, ErrLockNotAcquired) {
		r.logger.Info("platform route reconcile lock not acquired")
		return
	}
	if err != nil {
		r.logger.Error("platform route reconcile failed", "error", err)
		return
	}
	if result.Changed {
		r.logger.Info("platform routes reconciled", "routes", result.Routes, "reload_updated", result.ReloadUpdated)
		return
	}
	r.logger.Info("platform route reconcile no-op", "routes", result.Routes)
}

// Reconcile publishes one complete deterministic snapshot. The distributed
// lease remains held through render and publication so an older worker cannot
// overwrite a newer snapshot.
func (r *Reconciler) Reconcile(ctx context.Context) (result Result, err error) {
	release, acquired, err := r.store.TryPlatformRouteReconcileLock(ctx)
	if err != nil {
		return result, fmt.Errorf("acquire platform route lock: %w", err)
	}
	if !acquired {
		return result, ErrLockNotAcquired
	}
	result.LockAcquired = true
	defer func() {
		releaseErr := release()
		if releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release platform route lock: %w", releaseErr))
		}
	}()

	routes, err := r.store.ListPlatformRoutes(ctx)
	if err != nil {
		return result, fmt.Errorf("read platform route snapshot: %w", err)
	}
	result.Routes = len(routes)
	contents, err := Render(routes, r.backendURL)
	if err != nil {
		return result, fmt.Errorf("render platform routes: %w", err)
	}
	reloadContents := reloadSentinel(contents)
	result.Changed, result.ReloadUpdated, err = publishSnapshot(r.outputFile, contents, r.reloadFile, reloadContents)
	if err != nil {
		return result, fmt.Errorf("publish platform routes: %w", err)
	}
	return result, nil
}

type dynamicConfig struct {
	HTTP dynamicHTTP `yaml:"http"`
}

type dynamicHTTP struct {
	Routers  map[string]dynamicRouter  `yaml:"routers"`
	Services map[string]dynamicService `yaml:"services"`
}

type dynamicRouter struct {
	EntryPoints []string `yaml:"entryPoints"`
	Rule        string   `yaml:"rule"`
	Priority    int      `yaml:"priority"`
	Service     string   `yaml:"service"`
}

type dynamicService struct {
	LoadBalancer dynamicLoadBalancer `yaml:"loadBalancer"`
}

type dynamicLoadBalancer struct {
	Servers        []dynamicServer `yaml:"servers"`
	PassHostHeader bool            `yaml:"passHostHeader"`
}

type dynamicServer struct {
	URL string `yaml:"url"`
}

// Render produces the complete route snapshot. The only user-derived value
// in a Traefik rule is a canonical hostname; all YAML structure is generated
// from typed values rather than string concatenation.
func Render(routes []domain.PlatformRoute, backendURL string) ([]byte, error) {
	if err := validBackendURL(backendURL); err != nil {
		return nil, err
	}
	ordered := append([]domain.PlatformRoute(nil), routes...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Hostname != ordered[j].Hostname {
			return ordered[i].Hostname < ordered[j].Hostname
		}
		return ordered[i].SiteID < ordered[j].SiteID
	})
	config := dynamicConfig{HTTP: dynamicHTTP{
		Routers:  make(map[string]dynamicRouter, len(ordered)),
		Services: make(map[string]dynamicService),
	}}
	seenHosts := make(map[string]struct{}, len(ordered))
	seenRouters := make(map[string]struct{}, len(ordered))
	for _, route := range ordered {
		id, err := uuid.Parse(route.SiteID)
		if err != nil || id == uuid.Nil {
			return nil, fmt.Errorf("route Site ID %q is invalid", route.SiteID)
		}
		hostname, err := domainname.NormalizeHostname(route.Hostname)
		if err != nil {
			return nil, fmt.Errorf("route hostname %q is invalid: %w", route.Hostname, err)
		}
		if _, exists := seenHosts[hostname]; exists {
			return nil, fmt.Errorf("duplicate platform route hostname %q", hostname)
		}
		seenHosts[hostname] = struct{}{}
		routerID := "stealth-site-" + strings.ReplaceAll(id.String(), "-", "")
		if _, exists := seenRouters[routerID]; exists {
			return nil, fmt.Errorf("duplicate platform route router %q", routerID)
		}
		seenRouters[routerID] = struct{}{}
		config.HTTP.Routers[routerID] = dynamicRouter{
			EntryPoints: []string{"web"},
			Rule:        "Host(`" + hostname + "`)",
			Priority:    100,
			Service:     "stealth-platform-sites",
		}
	}
	if len(ordered) > 0 {
		config.HTTP.Services["stealth-platform-sites"] = dynamicService{LoadBalancer: dynamicLoadBalancer{
			Servers:        []dynamicServer{{URL: backendURL}},
			PassHostHeader: true,
		}}
	}
	contents, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}
	if err := validateRendered(contents, len(ordered), backendURL); err != nil {
		return nil, err
	}
	return contents, nil
}

func validBackendURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("platform Site backend URL must be an HTTP(S) origin without credentials or path")
	}
	return nil
}

func validateRendered(contents []byte, routeCount int, backendURL string) error {
	var parsed dynamicConfig
	if err := yaml.Unmarshal(contents, &parsed); err != nil {
		return fmt.Errorf("generated Traefik YAML is invalid: %w", err)
	}
	if len(parsed.HTTP.Routers) != routeCount {
		return fmt.Errorf("generated Traefik router count = %d, want %d", len(parsed.HTTP.Routers), routeCount)
	}
	if routeCount == 0 {
		if len(parsed.HTTP.Services) != 0 {
			return errors.New("empty platform snapshot contains a service")
		}
		return nil
	}
	service, ok := parsed.HTTP.Services["stealth-platform-sites"]
	if !ok || !service.LoadBalancer.PassHostHeader || len(service.LoadBalancer.Servers) != 1 || service.LoadBalancer.Servers[0].URL != backendURL {
		return errors.New("generated platform service is invalid")
	}
	for routerID, router := range parsed.HTTP.Routers {
		if !strings.HasPrefix(routerID, "stealth-site-") || len(router.EntryPoints) != 1 || router.EntryPoints[0] != "web" || router.Service != "stealth-platform-sites" || !strings.HasPrefix(router.Rule, "Host(`") || !strings.HasSuffix(router.Rule, "`)") {
			return fmt.Errorf("generated platform router %q is invalid", routerID)
		}
	}
	return nil
}

func reloadSentinel(contents []byte) []byte {
	digest := sha256.Sum256(contents)
	return []byte("# Stealth platform route snapshot\n# sha256: " + hex.EncodeToString(digest[:]) + "\n")
}

func publishSnapshot(outputFile string, contents []byte, reloadFile string, reloadContents []byte) (changed, reloadChanged bool, err error) {
	current, currentErr := readManagedFile(outputFile)
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return false, false, currentErr
	}
	if !bytes.Equal(current, contents) || errors.Is(currentErr, os.ErrNotExist) {
		if err := publishAtomic(outputFile, contents, 0o644); err != nil {
			return false, false, err
		}
		changed = true
	}
	currentReload, reloadErr := readManagedFile(reloadFile)
	if reloadErr != nil && !errors.Is(reloadErr, os.ErrNotExist) {
		return changed, false, reloadErr
	}
	if !bytes.Equal(currentReload, reloadContents) || errors.Is(reloadErr, os.ErrNotExist) {
		if err := publishAtomic(reloadFile, reloadContents, 0o644); err != nil {
			return changed, false, err
		}
		reloadChanged = true
	}
	return changed || reloadChanged, reloadChanged, nil
}

func readManagedFile(path string) ([]byte, error) {
	if err := validateDestination(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func publishAtomic(path string, contents []byte, mode os.FileMode) error {
	if err := validateDestination(path); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".stealth-platform-routes-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("fsync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("atomically publish %q: %w", path, err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("fsync directory %q: %w", directory, err)
	}
	return nil
}

func validateDestination(path string) error {
	path = filepath.Clean(path)
	if path == "." || path == string(filepath.Separator) || filepath.Base(path) == "." || filepath.Base(path) == ".." {
		return errors.New("managed Traefik path is invalid")
	}
	directory := filepath.Dir(path)
	if err := validateDirectoryPath(directory); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect managed Traefik file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed Traefik file %q is not a normal file", path)
	}
	return nil
}

func validateDirectoryPath(path string) error {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(path, current)
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect managed Traefik directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("managed Traefik directory %q is not a normal directory", current)
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
