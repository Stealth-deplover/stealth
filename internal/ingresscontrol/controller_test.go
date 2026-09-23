package ingresscontrol

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/domain"
)

type controllerFakeStore struct {
	connection           domain.CloudflareConnection
	status               domain.CloudflareRoutingStatus
	setCalls             []string
	verifyCalls          []string
	setError             error
	setErrorOrigin       string
	setErrorAfterPersist bool
}

func (s *controllerFakeStore) CloudflareConnectionDetails(context.Context) (domain.CloudflareConnection, error) {
	return s.connection, nil
}

func (s *controllerFakeStore) CloudflareRoutingStatus(context.Context) (domain.CloudflareRoutingStatus, error) {
	return s.status, nil
}

func (s *controllerFakeStore) SetCloudflareConsoleOriginDesired(_ context.Context, origin, action string) (bool, error) {
	s.setCalls = append(s.setCalls, action+":"+origin)
	if !s.status.Configured {
		return false, errors.New("not configured")
	}
	if s.status.ConsoleOriginDesired == origin {
		return false, nil
	}
	s.status.ConsoleOriginDesired = origin
	s.status.ConsoleOriginStatus = "pending"
	s.status.ConsoleOriginLastError = ""
	s.status.ConsolePublicVerifiedAt = nil
	s.status.ConsolePublicVerifiedOrigin = ""
	s.connection.ConsoleOriginDesired = origin
	if s.setError != nil && (s.setErrorOrigin == "" || s.setErrorOrigin == origin) && s.setErrorAfterPersist {
		return false, s.setError
	}
	if s.setError != nil && (s.setErrorOrigin == "" || s.setErrorOrigin == origin) {
		return false, s.setError
	}
	return true, nil
}

func (s *controllerFakeStore) RecordCloudflareConsoleOriginVerification(_ context.Context, origin string) (bool, error) {
	s.verifyCalls = append(s.verifyCalls, origin)
	if s.status.ConsoleOriginDesired != origin || s.status.ConsoleOriginObserved != origin || s.status.ConsoleOriginStatus != "ready" {
		return false, nil
	}
	s.status.ConsolePublicVerifiedOrigin = origin
	s.connection.ConsolePublicVerifiedOrigin = origin
	return true, nil
}

type controllerFakeReconciler struct {
	store       *controllerFakeStore
	calls       int
	errors      []error
	verifyErr   error
	verifyCalls int
}

func (r *controllerFakeReconciler) Reconcile(context.Context) (cloudflare.ReconcileResult, error) {
	r.calls++
	if r.calls <= len(r.errors) && r.errors[r.calls-1] != nil {
		r.store.status.ConsoleOriginStatus = "error"
		r.store.status.ConsoleOriginLastError = "Cloudflare API request failed"
		return cloudflare.ReconcileResult{}, r.errors[r.calls-1]
	}
	origin := r.store.status.ConsoleOriginDesired
	r.store.status.ConsoleOriginObserved = origin
	r.store.status.ConsoleOriginStatus = "ready"
	r.store.status.ConsoleOriginLastError = ""
	r.store.connection.ConsoleOriginObserved = origin
	r.store.connection.ConsoleOriginStatus = "ready"
	return cloudflare.ReconcileResult{Status: "ready", ConsoleOriginDesired: origin, ConsoleOriginObserved: origin}, nil
}

func (r *controllerFakeReconciler) VerifyProvider(context.Context) (string, error) {
	r.verifyCalls++
	return r.store.status.ConsoleOriginObserved, r.verifyErr
}

type controllerFakeProbe struct {
	localErr    error
	captureErr  error
	publicErrs  []error
	publicCalls int
	siteErr     error
	siteHost    string
	siteDomain  string
	siteDigest  string
}

func (p *controllerFakeProbe) LocalTraefik(context.Context, string) error { return p.localErr }
func (p *controllerFakeProbe) CapturePublicConsole(context.Context, string) (PublicEvidence, error) {
	return PublicEvidence{Routes: map[string]ResponseEvidence{"/": {StatusCode: 200}}}, p.captureErr
}
func (p *controllerFakeProbe) VerifyPublicConsole(context.Context, string, *PublicEvidence) (PublicEvidence, error) {
	p.publicCalls++
	if p.publicCalls <= len(p.publicErrs) {
		return PublicEvidence{}, p.publicErrs[p.publicCalls-1]
	}
	return PublicEvidence{Routes: map[string]ResponseEvidence{"/": {StatusCode: 200}}}, nil
}
func (p *controllerFakeProbe) VerifyPublicSite(_ context.Context, hostname, workloadDomain, digest string) error {
	p.siteHost, p.siteDomain, p.siteDigest = hostname, workloadDomain, digest
	return p.siteErr
}

func newControllerFixture(t *testing.T, desired, observed string) (*Controller, *controllerFakeStore, *controllerFakeReconciler, *controllerFakeProbe) {
	t.Helper()
	workload := "apps.example.com"
	store := &controllerFakeStore{
		connection: domain.CloudflareConnection{
			AccountID: "account", ConsoleZoneID: "zone", ConsoleHostname: "cloud.example.com", TunnelID: "tunnel", TunnelName: "stealth-prod", ConsoleRecordID: "console-record",
			ConsoleOriginDesired: desired, ConsoleOriginObserved: observed, WorkloadBaseDomain: &workload,
		},
		status: domain.CloudflareRoutingStatus{
			Configured: true, Status: "ready", ConsoleHostname: "cloud.example.com", ConsoleOriginDesired: desired,
			ConsoleOriginObserved: observed, ConsoleOriginStatus: "ready", EdgeTLSStatus: cloudflare.EdgeTLSReady,
		},
	}
	reconciler := &controllerFakeReconciler{store: store}
	probe := &controllerFakeProbe{}
	controller, err := New(store, reconciler, probe, "https://cloud.example.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return controller, store, reconciler, probe
}

func TestCutoverPreflightFailureDoesNotMutateDesiredState(t *testing.T) {
	controller, store, reconciler, probe := newControllerFixture(t, OriginProxy, OriginProxy)
	probe.localErr = errors.New("Traefik route returned 502")
	if err := controller.Cutover(context.Background(), io.Discard); err == nil || !strings.Contains(err.Error(), "local preflight") {
		t.Fatalf("cutover error = %v", err)
	}
	if len(store.setCalls) != 0 || reconciler.calls != 0 {
		t.Fatalf("preflight failure mutated state: set=%v reconcile_calls=%d", store.setCalls, reconciler.calls)
	}
}

func TestCutoverProviderAndPublicVerificationSucceeds(t *testing.T) {
	controller, store, reconciler, probe := newControllerFixture(t, OriginProxy, OriginProxy)
	var output strings.Builder
	if err := controller.Cutover(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if store.status.ConsoleOriginDesired != OriginTraefik || store.status.ConsoleOriginObserved != OriginTraefik || store.status.ConsoleOriginStatus != "ready" {
		t.Fatalf("Console origin state = %#v", store.status)
	}
	if reconciler.calls != 2 || reconciler.verifyCalls != 2 || len(store.verifyCalls) != 1 || store.verifyCalls[0] != OriginTraefik || probe.publicCalls != 1 {
		t.Fatalf("cutover orchestration reconcile=%d provider_verify=%d persisted_verification=%v public=%d", reconciler.calls, reconciler.verifyCalls, store.verifyCalls, probe.publicCalls)
	}
	if !strings.Contains(output.String(), "verified through Traefik") {
		t.Fatalf("cutover output = %q", output.String())
	}
}

func TestCutoverPublicFailureRollsBackAndVerifiesNginxRecovery(t *testing.T) {
	controller, store, reconciler, probe := newControllerFixture(t, OriginProxy, OriginProxy)
	probe.publicErrs = []error{errors.New("HSTS policy changed after cutover"), nil}
	var output strings.Builder
	err := controller.Cutover(context.Background(), &output)
	if err == nil || !strings.Contains(err.Error(), "Console origin cutover failed after the origin request") {
		t.Fatalf("cutover error = %v", err)
	}
	if store.status.ConsoleOriginDesired != OriginProxy || store.status.ConsoleOriginObserved != OriginProxy || store.status.ConsolePublicVerifiedOrigin != OriginProxy {
		t.Fatalf("automatic rollback state = %#v", store.status)
	}
	if got := strings.Join(store.setCalls, ","); got != "cutover:traefik,rollback:proxy" {
		t.Fatalf("desired-state transitions = %s", got)
	}
	if reconciler.calls != 3 || probe.publicCalls != 2 || !strings.Contains(output.String(), "rollback succeeded") || !strings.Contains(output.String(), "Nginx") {
		t.Fatalf("rollback evidence reconcile=%d public=%d output=%q", reconciler.calls, probe.publicCalls, output.String())
	}
}

func TestAmbiguousCutoverCommitIsInspectedAndRolledBack(t *testing.T) {
	controller, store, reconciler, probe := newControllerFixture(t, OriginProxy, OriginProxy)
	store.setError = errors.New("database connection lost while returning commit result")
	store.setErrorOrigin = OriginTraefik
	store.setErrorAfterPersist = true
	var output strings.Builder
	err := controller.Cutover(context.Background(), &output)
	if err == nil || !strings.Contains(err.Error(), "cutover request result was uncertain") {
		t.Fatalf("ambiguous cutover error = %v", err)
	}
	if got := strings.Join(store.setCalls, ","); got != "cutover:traefik,rollback:proxy" {
		t.Fatalf("durable transitions after ambiguous commit = %s", got)
	}
	if store.status.ConsoleOriginDesired != OriginProxy || store.status.ConsoleOriginObserved != OriginProxy || probe.publicCalls != 1 || reconciler.calls != 2 {
		t.Fatalf("ambiguous cutover did not restore Nginx: status=%#v reconciles=%d public=%d", store.status, reconciler.calls, probe.publicCalls)
	}
	if !strings.Contains(output.String(), "rollback succeeded") {
		t.Fatalf("ambiguous commit recovery output = %q", output.String())
	}
}

func TestCutoverProviderFailureAttemptsVerifiedRollback(t *testing.T) {
	controller, store, reconciler, _ := newControllerFixture(t, OriginProxy, OriginProxy)
	reconciler.errors = []error{nil, errors.New("Cloudflare request timed out")}
	var output strings.Builder
	if err := controller.Cutover(context.Background(), &output); err == nil {
		t.Fatal("provider failure must fail cutover")
	}
	if store.status.ConsoleOriginDesired != OriginProxy || store.status.ConsoleOriginStatus != "ready" || store.status.ConsoleOriginObserved != OriginProxy {
		t.Fatalf("provider timeout did not restore the Nginx origin: %#v", store.status)
	}
	if reconciler.calls != 3 || !strings.Contains(output.String(), "rollback succeeded") {
		t.Fatalf("provider failure rollback reconcile=%d output=%q", reconciler.calls, output.String())
	}
}

func TestCutoverRollbackFailureIsHighSeverity(t *testing.T) {
	controller, store, reconciler, _ := newControllerFixture(t, OriginProxy, OriginProxy)
	reconciler.errors = []error{nil, errors.New("cutover provider timeout"), errors.New("rollback provider timeout")}
	err := controller.Cutover(context.Background(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "HIGH SEVERITY") {
		t.Fatalf("rollback failure error = %v", err)
	}
	if store.status.ConsoleOriginDesired != OriginProxy || store.status.ConsoleOriginObserved != OriginProxy {
		t.Fatalf("rollback failure state = %#v", store.status)
	}
}

func TestCutoverAlreadyOnTraefikIsVerifiedNoOp(t *testing.T) {
	controller, store, reconciler, probe := newControllerFixture(t, OriginTraefik, OriginTraefik)
	if err := controller.Cutover(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(store.setCalls) != 0 || reconciler.calls != 1 || probe.publicCalls != 1 {
		t.Fatalf("repeat cutover was not a verified no-op: set=%v reconcile=%d public=%d", store.setCalls, reconciler.calls, probe.publicCalls)
	}
}

func TestManualRollbackDoesNotNeedPublicAPIAndRestoresNginx(t *testing.T) {
	controller, store, reconciler, probe := newControllerFixture(t, OriginTraefik, OriginTraefik)
	var output strings.Builder
	if err := controller.Rollback(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if store.status.ConsoleOriginDesired != OriginProxy || store.status.ConsoleOriginObserved != OriginProxy || store.status.ConsolePublicVerifiedOrigin != OriginProxy {
		t.Fatalf("manual rollback status = %#v", store.status)
	}
	if got := strings.Join(store.setCalls, ","); got != "rollback:proxy" || reconciler.calls != 1 || probe.publicCalls != 1 {
		t.Fatalf("manual rollback calls set=%s reconcile=%d public=%d", got, reconciler.calls, probe.publicCalls)
	}
}

func TestVerifyRequiresReadyCloudflareWorkloadAndExactPlatformHost(t *testing.T) {
	controller, store, _, probe := newControllerFixture(t, OriginProxy, OriginProxy)
	if err := controller.Verify(context.Background(), "portfolio.apps.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if probe.siteHost != "portfolio.apps.example.com" {
		t.Fatalf("site host probe = %q", probe.siteHost)
	}
	if err := controller.Verify(context.Background(), "nested.portfolio.apps.example.com", ""); err == nil {
		t.Fatal("multi-label hostname should not be accepted as a platform Site")
	}
	if err := controller.Verify(context.Background(), "portfolio.other.example.net", ""); err == nil {
		t.Fatal("hostname outside workload base domain should be rejected")
	}
	store.status.EdgeTLSStatus = cloudflare.EdgeTLSActionRequired
	if err := controller.Verify(context.Background(), "portfolio.apps.example.com", ""); err == nil || !strings.Contains(err.Error(), "edge TLS") {
		t.Fatalf("unready edge TLS verification error = %v", err)
	}
}

func TestVerifyReadsProviderStateWithoutMutation(t *testing.T) {
	controller, store, reconciler, _ := newControllerFixture(t, OriginProxy, OriginProxy)
	if err := controller.Verify(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	if len(store.setCalls) != 0 || reconciler.verifyCalls != 1 {
		t.Fatalf("verify was not read-only: desired writes=%v provider reads=%d", store.setCalls, reconciler.verifyCalls)
	}
	reconciler.verifyErr = errors.New("Tunnel ingress differs from durable state")
	if err := controller.Verify(context.Background(), "", ""); err == nil || !strings.Contains(err.Error(), "Tunnel verification failed") {
		t.Fatalf("provider drift verification error = %v", err)
	}
	if len(store.setCalls) != 0 {
		t.Fatalf("read-only verify changed desired state: %v", store.setCalls)
	}
}

func TestStatusDistinguishesUnusedCloudflareFromMissingCredential(t *testing.T) {
	controller, store, _, _ := newControllerFixture(t, OriginProxy, OriginProxy)
	store.status.Configured = false
	var output strings.Builder
	if err := controller.Status(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "degraded") || !strings.Contains(output.String(), "reconnect") || strings.Contains(output.String(), "not configured") {
		t.Fatalf("missing credential status = %q", output.String())
	}

	store.connection = domain.CloudflareConnection{}
	store.status = domain.CloudflareRoutingStatus{Configured: false}
	output.Reset()
	if err := controller.Status(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Cloudflare Tunnel: not configured") || !strings.Contains(output.String(), "not applicable") {
		t.Fatalf("provider-neutral status = %q", output.String())
	}
}
