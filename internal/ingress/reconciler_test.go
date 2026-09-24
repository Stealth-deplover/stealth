package ingress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
)

type fakeStore struct {
	routes       []domain.PlatformRoute
	appRoutes    []domain.AppPlatformRoute
	listErr      error
	appListErr   error
	lockAcquired bool
	releases     int
}

func (s *fakeStore) ListAppPlatformRoutes(context.Context) ([]domain.AppPlatformRoute, error) {
	if s.appListErr != nil {
		return nil, s.appListErr
	}
	return append([]domain.AppPlatformRoute(nil), s.appRoutes...), nil
}

func (s *fakeStore) TryPlatformRouteReconcileLock(context.Context) (func() error, bool, error) {
	if !s.lockAcquired {
		return nil, false, nil
	}
	return func() error {
		s.releases++
		return nil
	}, true, nil
}

func (s *fakeStore) ListPlatformRoutes(context.Context) ([]domain.PlatformRoute, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]domain.PlatformRoute(nil), s.routes...), nil
}

func TestRenderIsDeterministicAndUsesNarrowBackend(t *testing.T) {
	routes := []domain.PlatformRoute{
		{SiteID: "018f0d5e-7c19-7abc-8d1e-1234567890ac", Hostname: "beta.apps.example.com"},
		{SiteID: "018f0d5e-7c19-7abc-8d1e-1234567890ab", Hostname: "alpha.apps.example.com"},
	}
	first, err := Render(routes, DefaultPlatformSiteBackend)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render([]domain.PlatformRoute{routes[1], routes[0]}, DefaultPlatformSiteBackend)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("render is not byte-stable:\n%s\n---\n%s", first, second)
	}
	contents := string(first)
	if !strings.Contains(contents, "Host(`alpha.apps.example.com`)") || !strings.Contains(contents, "Host(`beta.apps.example.com`)") {
		t.Fatalf("rendered rules missing: %s", contents)
	}
	if !strings.Contains(contents, "url: http://api:8082") || !strings.Contains(contents, "passHostHeader: true") {
		t.Fatalf("rendered service is not the narrow Site listener: %s", contents)
	}
	if strings.Contains(contents, "v1/") || strings.Contains(contents, "healthz") || strings.Contains(contents, "metrics") {
		t.Fatalf("rendered platform config contains control-plane routes: %s", contents)
	}
}

func TestRenderEmptyProducesNoopTraefikConfiguration(t *testing.T) {
	contents, err := Render(nil, DefaultPlatformSiteBackend)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "# Stealth platform route snapshot: no eligible Sites\n" {
		t.Fatalf("empty render = %q", contents)
	}
	if strings.Contains(string(contents), "routers:") || strings.Contains(string(contents), "services:") {
		t.Fatalf("empty render contains standalone Traefik sections: %s", contents)
	}
}

func TestRenderAppsIsDeterministicAndUsesOnlyPrivateRuntimeTargets(t *testing.T) {
	routes := []domain.AppPlatformRoute{
		{AppID: "018f0d5e-7c19-7abc-8d1e-1234567890ac", Hostname: "beta.apps.example.com", Address: "172.22.0.8", Port: 8080},
		{AppID: "018f0d5e-7c19-7abc-8d1e-1234567890ab", Hostname: "alpha.apps.example.com", Address: "172.22.0.7", Port: 3000},
	}
	first, err := RenderApps(routes)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderApps([]domain.AppPlatformRoute{routes[1], routes[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(first, second) {
		t.Fatalf("App route render is not byte-stable:\n%s\n---\n%s", first, second)
	}
	contents := string(first)
	for _, want := range []string{
		"Host(`alpha.apps.example.com`)", "Host(`beta.apps.example.com`)",
		"http://172.22.0.7:3000", "http://172.22.0.8:8080",
		"stealth-app-service-018f0d5e7c197abc8d1e1234567890ab",
	} {
		if !strings.Contains(contents, want) {
			t.Errorf("generated App routes are missing %q: %s", want, contents)
		}
	}
}

func TestRenderAppsFailsClosedOnMalformedHostnameAndUntrustedTargets(t *testing.T) {
	routes := []domain.AppPlatformRoute{
		{AppID: "018f0d5e-7c19-7abc-8d1e-1234567890ab", Hostname: "safe.apps.example.com`,PathPrefix(`/admin", Address: "172.22.0.7", Port: 8080},
		{AppID: "018f0d5e-7c19-7abc-8d1e-1234567890ac", Hostname: "public-target.apps.example.com", Address: "198.51.100.8", Port: 8080},
		{AppID: "018f0d5e-7c19-7abc-8d1e-1234567890ad", Hostname: "url-target.apps.example.com", Address: "http://attacker.invalid", Port: 8080},
		{AppID: "018f0d5e-7c19-7abc-8d1e-1234567890ae", Hostname: "bad-port.apps.example.com", Address: "172.22.0.9", Port: 65536},
	}
	contents, err := RenderApps(routes)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "# Stealth App route snapshot: no eligible Apps\n" {
		t.Fatalf("untrusted rows produced a route: %s", contents)
	}
}

func TestReconcilePreservesLastKnownGoodAppSnapshotWhileSitesConverge(t *testing.T) {
	directory := t.TempDir()
	generated := filepath.Join(directory, "generated")
	if err := os.Mkdir(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		lockAcquired: true,
		routes:       []domain.PlatformRoute{{SiteID: "018f0d5e-7c19-7abc-8d1e-1234567890ab", Hostname: "site.apps.example.com"}},
		appRoutes:    []domain.AppPlatformRoute{{AppID: "018f0d5e-7c19-7abc-8d1e-1234567890ac", Hostname: "app.apps.example.com", Address: "172.22.0.8", Port: 8080}},
	}
	reconciler, err := New(store, generated, filepath.Join(directory, ".reload.yaml"), time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(generated, GeneratedAppFilename)
	knownGoodApp, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	store.routes = []domain.PlatformRoute{{SiteID: "018f0d5e-7c19-7abc-8d1e-1234567890ad", Hostname: "updated.apps.example.com"}}
	store.appListErr = errors.New("temporary App snapshot failure")
	if _, err := reconciler.Reconcile(context.Background()); err == nil {
		t.Fatal("App snapshot failure was hidden")
	}
	appAfterFailure, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(knownGoodApp, appAfterFailure) {
		t.Fatal("App snapshot failure replaced last-known-good routes")
	}
	siteAfterFailure, err := os.ReadFile(filepath.Join(generated, GeneratedFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(siteAfterFailure), "updated.apps.example.com") {
		t.Fatalf("independent Site snapshot did not converge: %s", siteAfterFailure)
	}
}

func TestReconcilePublishesIdempotentlyAndRemovesStaleRoutes(t *testing.T) {
	directory := t.TempDir()
	generated := filepath.Join(directory, "generated")
	if err := os.Mkdir(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	reload := filepath.Join(directory, ".reload.yaml")
	store := &fakeStore{
		lockAcquired: true,
		routes: []domain.PlatformRoute{{
			SiteID:   "018f0d5e-7c19-7abc-8d1e-1234567890ab",
			Hostname: "portfolio.apps.example.com",
		}},
	}
	reconciler, err := New(store, generated, reload, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.ReloadUpdated || first.Routes != 1 {
		t.Fatalf("initial result = %+v", first)
	}
	firstContents, err := os.ReadFile(filepath.Join(generated, GeneratedFilename))
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(store, generated, reload, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || second.ReloadUpdated {
		t.Fatalf("idempotent result = %+v", second)
	}
	store.routes = nil
	third, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !third.Changed || third.Routes != 0 {
		t.Fatalf("stale route removal result = %+v", third)
	}
	cleared, err := os.ReadFile(filepath.Join(generated, GeneratedFilename))
	if err != nil {
		t.Fatal(err)
	}
	if bytesEqual(firstContents, cleared) || strings.Contains(string(cleared), "portfolio.apps.example.com") {
		t.Fatalf("stale route remained in generated snapshot: %s", cleared)
	}
	if store.releases != 3 {
		t.Fatalf("lock releases = %d, want 3", store.releases)
	}
}

func TestReconcilePreservesLastKnownGoodOnSnapshotOrRenderFailure(t *testing.T) {
	directory := t.TempDir()
	generated := filepath.Join(directory, "generated")
	if err := os.Mkdir(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{lockAcquired: true, routes: []domain.PlatformRoute{{SiteID: "018f0d5e-7c19-7abc-8d1e-1234567890ab", Hostname: "ok.apps.example.com"}}}
	reconciler, err := New(store, generated, filepath.Join(directory, ".reload.yaml"), time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(generated, GeneratedFilename)
	knownGood, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	store.listErr = errors.New("database unavailable")
	if _, err := reconciler.Reconcile(context.Background()); err == nil {
		t.Fatal("database failure was hidden")
	}
	afterDatabaseFailure, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(knownGood, afterDatabaseFailure) {
		t.Fatal("database failure replaced last-known-good snapshot")
	}
	store.listErr = nil
	store.routes = []domain.PlatformRoute{{SiteID: "018f0d5e-7c19-7abc-8d1e-1234567890ab", Hostname: "bad host"}}
	if _, err := reconciler.Reconcile(context.Background()); err == nil {
		t.Fatal("invalid route was rendered")
	}
	afterRenderFailure, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(knownGood, afterRenderFailure) {
		t.Fatal("render failure replaced last-known-good snapshot")
	}
}

func TestReconcileSkipsWhenAnotherWorkerOwnsLock(t *testing.T) {
	directory := t.TempDir()
	generated := filepath.Join(directory, "generated")
	if err := os.Mkdir(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	reconciler, err := New(store, generated, filepath.Join(directory, ".reload.yaml"), time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reconciler.Reconcile(context.Background())
	if !errors.Is(err, ErrLockNotAcquired) {
		t.Fatalf("lock result = %v, want ErrLockNotAcquired", err)
	}
	if _, err := os.Stat(filepath.Join(generated, GeneratedFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock-skipped reconcile published a file: %v", err)
	}
}

func TestNewRejectsReloadPathInsideGeneratedDirectory(t *testing.T) {
	directory := t.TempDir()
	if _, err := New(&fakeStore{}, filepath.Join(directory, "generated"), filepath.Join(directory, "generated", ".reload.yaml"), time.Second, nil); err == nil {
		t.Fatal("reload sentinel inside generated directory was accepted")
	}
}

func TestReconcileRejectsSymlinkDestination(t *testing.T) {
	directory := t.TempDir()
	generated := filepath.Join(directory, "generated")
	if err := os.Mkdir(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	liveTarget := filepath.Join(directory, "outside.yaml")
	if err := os.WriteFile(liveTarget, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(liveTarget, filepath.Join(generated, GeneratedFilename)); err != nil {
		t.Skipf("symlink test unavailable: %v", err)
	}
	store := &fakeStore{lockAcquired: true, routes: []domain.PlatformRoute{{SiteID: "018f0d5e-7c19-7abc-8d1e-1234567890ab", Hostname: "safe.apps.example.com"}}}
	reconciler, err := New(store, generated, filepath.Join(directory, ".reload.yaml"), time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background()); err == nil {
		t.Fatal("symlink destination was accepted")
	}
	contents, err := os.ReadFile(liveTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "keep" {
		t.Fatalf("symlink target changed to %q", contents)
	}
}

func TestReconcileRejectsSymlinkDirectoryComponent(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(directory, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink test unavailable: %v", err)
	}
	store := &fakeStore{lockAcquired: true, routes: []domain.PlatformRoute{{SiteID: "018f0d5e-7c19-7abc-8d1e-1234567890ab", Hostname: "safe.apps.example.com"}}}
	reconciler, err := New(store, filepath.Join(link, "generated"), filepath.Join(directory, ".reload.yaml"), time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background()); err == nil {
		t.Fatal("symlink directory component was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "generated", GeneratedFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink target was modified: %v", err)
	}
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
