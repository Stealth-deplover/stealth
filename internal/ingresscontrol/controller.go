// Package ingresscontrol implements the bounded host-initiated public Console
// origin transition. Provider writes still go through the Cloudflare worker
// reconciler so there is one authoritative Tunnel desired-state generator.
package ingresscontrol

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/domainname"
)

const (
	OriginProxy   = "proxy"
	OriginTraefik = "traefik"
)

type Store interface {
	CloudflareConnectionDetails(context.Context) (domain.CloudflareConnection, error)
	CloudflareRoutingStatus(context.Context) (domain.CloudflareRoutingStatus, error)
	SetCloudflareConsoleOriginDesired(context.Context, string, string) (bool, error)
	RecordCloudflareConsoleOriginVerification(context.Context, string) (bool, error)
}

type Reconciler interface {
	Reconcile(context.Context) (cloudflare.ReconcileResult, error)
	VerifyProvider(context.Context) (string, error)
}

type Probe interface {
	LocalTraefik(context.Context, string) error
	CapturePublicConsole(context.Context, string) (PublicEvidence, error)
	VerifyPublicConsole(context.Context, string, *PublicEvidence) (PublicEvidence, error)
	VerifyPublicSite(context.Context, string, string, string) error
}

type PublicEvidence struct {
	Routes map[string]ResponseEvidence `json:"routes"`
}

type ResponseEvidence struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	BodySHA256 string            `json:"body_sha256,omitempty"`
}

type Controller struct {
	store      Store
	reconciler Reconciler
	probe      Probe
	publicURL  string
	logger     *slog.Logger
}

func New(store Store, reconciler Reconciler, probe Probe, publicURL string, logger *slog.Logger) (*Controller, error) {
	if store == nil || reconciler == nil || probe == nil {
		return nil, errors.New("ingress control requires a store, Cloudflare reconciler, and probe")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Controller{store: store, reconciler: reconciler, probe: probe, publicURL: strings.TrimSpace(publicURL), logger: logger}, nil
}

func (c *Controller) Status(ctx context.Context, out io.Writer) error {
	status, connection, err := c.current(ctx)
	if err != nil {
		return err
	}
	if !status.Configured {
		if cloudflareIdentityPresent(connection) {
			fmt.Fprintln(out, "Cloudflare Tunnel: degraded (saved identity has no usable credential)")
			fmt.Fprintln(out, "Action: reconnect the scoped Cloudflare token for the existing Named Tunnel.")
			if reason := firstNonEmpty(status.ConsoleOriginLastError, status.LastError); reason != "" {
				fmt.Fprintf(out, "Provider error: %s\n", reason)
			}
			return nil
		}
		fmt.Fprintln(out, "Cloudflare Tunnel: not configured")
		fmt.Fprintln(out, "Console origin: not applicable")
		return nil
	}
	if err := requireConnection(status, connection); err != nil {
		fmt.Fprintln(out, "Cloudflare Tunnel: incomplete")
		fmt.Fprintln(out, "Action: reconnect the existing Named Tunnel before changing the Console origin.")
		if reason := firstNonEmpty(status.ConsoleOriginLastError, status.LastError); reason != "" {
			fmt.Fprintf(out, "Provider error: %s\n", reason)
		}
		return nil
	}
	fmt.Fprintln(out, "Cloudflare Tunnel: configured")
	fmt.Fprintf(out, "Console hostname: %s\n", connection.ConsoleHostname)
	fmt.Fprintf(out, "Desired origin: %s\n", status.ConsoleOriginDesired)
	fmt.Fprintf(out, "Observed origin: %s\n", status.ConsoleOriginObserved)
	fmt.Fprintf(out, "Provider status: %s\n", status.ConsoleOriginStatus)
	if status.ConsoleOriginLastError != "" {
		fmt.Fprintf(out, "Provider error: %s\n", status.ConsoleOriginLastError)
	}
	if status.ConsolePublicVerifiedAt == nil {
		fmt.Fprintln(out, "Public verification: not verified")
	} else {
		fmt.Fprintf(out, "Public verification: %s at %s\n", status.ConsolePublicVerifiedOrigin, status.ConsolePublicVerifiedAt.UTC().Format("2006-01-02T15:04:05Z"))
	}
	fmt.Fprintln(out, "Rollback origin: proxy/Nginx (retained)")
	return nil
}

func (c *Controller) Verify(ctx context.Context, siteHostname, siteSHA256 string) error {
	status, connection, err := c.current(ctx)
	if err != nil {
		return err
	}
	if err := requireConnection(status, connection); err != nil {
		return err
	}
	if err := c.requirePublicHost(connection.ConsoleHostname); err != nil {
		return err
	}
	if status.ConsoleOriginDesired != status.ConsoleOriginObserved || status.ConsoleOriginStatus != "ready" {
		return errors.New("Cloudflare Console origin is not converged; inspect `stealth ingress status` and retry after reconciliation")
	}
	providerOrigin, err := c.reconciler.VerifyProvider(ctx)
	if err != nil {
		return fmt.Errorf("Cloudflare Tunnel verification failed: %w", err)
	}
	if providerOrigin != status.ConsoleOriginDesired {
		return errors.New("Cloudflare Tunnel Console origin differs from persisted observed state; run `stealth ingress status` and retry after reconciliation")
	}
	if err := c.probe.LocalTraefik(ctx, connection.ConsoleHostname); err != nil {
		return fmt.Errorf("Traefik local preflight failed: %w", err)
	}
	if _, err := c.probe.VerifyPublicConsole(ctx, c.publicURL, nil); err != nil {
		return err
	}
	if siteHostname != "" {
		if err := c.verifySiteReadiness(ctx, status, connection, siteHostname, siteSHA256); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) Cutover(ctx context.Context, out io.Writer) error {
	status, connection, err := c.current(ctx)
	if err != nil {
		return err
	}
	if err := requireConnection(status, connection); err != nil {
		return err
	}
	if err := c.requirePublicHost(connection.ConsoleHostname); err != nil {
		return err
	}
	if err := c.probe.LocalTraefik(ctx, connection.ConsoleHostname); err != nil {
		return fmt.Errorf("Traefik local preflight failed; Console origin was not changed: %w", err)
	}
	if err := c.reconcileCurrentOrigin(ctx); err != nil {
		return fmt.Errorf("could not verify the current Cloudflare Console origin; no cutover was requested: %w", err)
	}
	status, connection, err = c.current(ctx)
	if err != nil {
		return err
	}
	if status.ConsoleOriginObserved != status.ConsoleOriginDesired || status.ConsoleOriginStatus != "ready" {
		return errors.New("current Cloudflare Console origin is unknown or not converged; no cutover was requested")
	}
	baseline, err := c.probe.CapturePublicConsole(ctx, c.publicURL)
	if err != nil {
		return fmt.Errorf("public HTTPS baseline failed; Console origin was not changed: %w", err)
	}
	if status.ConsoleOriginDesired == OriginTraefik && status.ConsoleOriginObserved == OriginTraefik {
		if _, err := c.probe.VerifyPublicConsole(ctx, c.publicURL, &baseline); err != nil {
			return fmt.Errorf("Traefik is already the provider origin, but public verification failed: %w", err)
		}
		if err := c.persistVerifiedOrigin(ctx, OriginTraefik); err != nil {
			return err
		}
		fmt.Fprintln(out, "Console origin is already Traefik; public HTTPS verification passed.")
		return nil
	}

	if _, err := c.store.SetCloudflareConsoleOriginDesired(ctx, OriginTraefik, "cutover"); err != nil {
		// A PostgreSQL commit can succeed even if the caller observes a
		// connection or cancellation error while receiving the result. Read
		// durable state with a detached bound context before deciding whether
		// the worker could later switch the public origin unexpectedly.
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		requested, _, checkErr := c.current(checkCtx)
		if checkErr != nil {
			return fmt.Errorf("could not persist or inspect Console origin cutover request: %w; HIGH SEVERITY: durable state is uncertain, run `stealth ingress rollback` from the installation host", errors.Join(err, checkErr))
		}
		if requested.ConsoleOriginDesired == OriginTraefik {
			return c.rollbackAfterFailedCutover(ctx, baseline, fmt.Errorf("cutover request result was uncertain: %w", err), out)
		}
		return fmt.Errorf("could not persist Console origin cutover request: %w", err)
	}
	c.logger.Info("cloudflare console origin desired changed", "previous_origin", status.ConsoleOriginDesired, "desired_origin", OriginTraefik, "source", "host_cli")
	if err := c.reconcileRequestedOrigin(ctx, OriginTraefik); err != nil {
		return c.rollbackAfterFailedCutover(ctx, baseline, err, out)
	}
	if _, err := c.probe.VerifyPublicConsole(ctx, c.publicURL, &baseline); err != nil {
		c.logger.Warn("cloudflare console cutover public verification failed", "error", safeReason(err))
		return c.rollbackAfterFailedCutover(ctx, baseline, err, out)
	}
	if err := c.persistVerifiedOrigin(ctx, OriginTraefik); err != nil {
		return err
	}
	c.logger.Info("cloudflare console cutover public verification succeeded", "origin", OriginTraefik)
	fmt.Fprintln(out, "Console origin cutover verified through Traefik. Nginx remains available for rollback.")
	return nil
}

func (c *Controller) Rollback(ctx context.Context, out io.Writer) error {
	status, connection, err := c.current(ctx)
	if err != nil {
		return err
	}
	if err := requireConnection(status, connection); err != nil {
		return err
	}
	if err := c.requirePublicHost(connection.ConsoleHostname); err != nil {
		return err
	}
	if _, err := c.store.SetCloudflareConsoleOriginDesired(ctx, OriginProxy, "rollback"); err != nil {
		return fmt.Errorf("could not persist Nginx rollback request: %w", err)
	}
	c.logger.Info("cloudflare console origin desired changed", "previous_origin", status.ConsoleOriginDesired, "desired_origin", OriginProxy, "source", "host_cli")
	if err := c.reconcileRequestedOrigin(ctx, OriginProxy); err != nil {
		return fmt.Errorf("Nginx rollback did not converge through Cloudflare; run `stealth ingress status` and retry: %w", err)
	}
	if _, err := c.probe.VerifyPublicConsole(ctx, c.publicURL, nil); err != nil {
		return fmt.Errorf("Cloudflare now targets Nginx, but public recovery verification failed: %w", err)
	}
	if err := c.persistVerifiedOrigin(ctx, OriginProxy); err != nil {
		return err
	}
	c.logger.Info("cloudflare console rollback public verification succeeded", "origin", OriginProxy)
	fmt.Fprintln(out, "Console origin is verified through proxy/Nginx.")
	return nil
}

func (c *Controller) rollbackAfterFailedCutover(ctx context.Context, baseline PublicEvidence, verificationErr error, out io.Writer) error {
	c.logger.Warn("cloudflare automatic console rollback started", "origin", OriginProxy)
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	if _, err := c.store.SetCloudflareConsoleOriginDesired(rollbackCtx, OriginProxy, "rollback"); err != nil {
		c.logger.Error("cloudflare automatic console rollback failed", "error", safeReason(err))
		return fmt.Errorf("cutover failed (%v); HIGH SEVERITY: automatic rollback could not be persisted: %w. Run `stealth ingress rollback` from the installation host", safeReason(verificationErr), err)
	}
	if err := c.reconcileRequestedOrigin(rollbackCtx, OriginProxy); err != nil {
		c.logger.Error("cloudflare automatic console rollback failed", "error", safeReason(err))
		return fmt.Errorf("cutover failed (%v); HIGH SEVERITY: provider rollback did not verify: %w. Run `stealth ingress rollback` from the installation host", safeReason(verificationErr), err)
	}
	if _, err := c.probe.VerifyPublicConsole(rollbackCtx, c.publicURL, &baseline); err != nil {
		c.logger.Error("cloudflare automatic console rollback failed", "error", safeReason(err))
		return fmt.Errorf("cutover failed (%v); HIGH SEVERITY: provider targets Nginx but public recovery failed: %w. Check Cloudflare HSTS and security policy, then run `stealth ingress verify`", safeReason(verificationErr), err)
	}
	if err := c.persistVerifiedOrigin(rollbackCtx, OriginProxy); err != nil {
		return fmt.Errorf("cutover failed (%v); service recovered through Nginx, but recovery status could not be persisted: %w", safeReason(verificationErr), err)
	}
	c.logger.Warn("cloudflare automatic console rollback succeeded", "origin", OriginProxy)
	fmt.Fprintln(out, "Cutover failed verification; rollback succeeded and service was restored through Nginx.")
	return fmt.Errorf("Console origin cutover failed after the origin request: %w", verificationErr)
}

func (c *Controller) reconcileCurrentOrigin(ctx context.Context) error {
	status, _, err := c.current(ctx)
	if err != nil {
		return err
	}
	return c.reconcileRequestedOrigin(ctx, status.ConsoleOriginDesired)
}

func (c *Controller) reconcileRequestedOrigin(ctx context.Context, origin string) error {
	var reconcileErr error
	for attempt := 0; attempt < 30; attempt++ {
		_, reconcileErr = c.reconciler.Reconcile(ctx)
		status, connection, statusErr := c.current(ctx)
		if statusErr != nil {
			return statusErr
		}
		if status.ConsoleOriginDesired == origin && status.ConsoleOriginObserved == origin && status.ConsoleOriginStatus == "ready" {
			if connection.ConsoleHostname == "" {
				return errors.New("Cloudflare Console hostname is missing")
			}
			// A workload certificate inspection error is separate from Console
			// origin convergence. Still read the Tunnel after every pass so a
			// lock-skipped worker cannot make persisted, stale observation look
			// like provider verification for this host operation.
			providerOrigin, verifyErr := c.reconciler.VerifyProvider(ctx)
			if verifyErr == nil && providerOrigin == origin {
				return nil
			}
			if errors.Is(verifyErr, cloudflare.ErrLockNotAcquired) {
				reconcileErr = verifyErr
			} else if verifyErr != nil {
				return fmt.Errorf("Cloudflare Tunnel origin verification failed: %w", verifyErr)
			} else {
				return errors.New("Cloudflare Tunnel did not verify the requested Console origin")
			}
		}
		if !errors.Is(reconcileErr, cloudflare.ErrLockNotAcquired) {
			if reconcileErr != nil {
				return reconcileErr
			}
			return errors.New("Cloudflare Tunnel did not verify the requested Console origin")
		}
		if attempt == 29 {
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return reconcileErr
}

func (c *Controller) persistVerifiedOrigin(ctx context.Context, origin string) error {
	verified, err := c.store.RecordCloudflareConsoleOriginVerification(ctx, origin)
	if err != nil {
		return fmt.Errorf("persist public Console verification: %w", err)
	}
	if !verified {
		return errors.New("Console origin changed while public verification was running; rerun `stealth ingress verify`")
	}
	return nil
}

func (c *Controller) verifySiteReadiness(ctx context.Context, status domain.CloudflareRoutingStatus, connection domain.CloudflareConnection, hostname, digest string) error {
	if !status.Configured || connection.WorkloadBaseDomain == nil || status.Status != "ready" || status.EdgeTLSStatus != "ready" {
		return errors.New("platform Site routing is not ready: Cloudflare wildcard routing and edge TLS must both be ready before public Site verification")
	}
	canonical, err := normalizePlatformSiteHostname(hostname, *connection.WorkloadBaseDomain)
	if err != nil {
		return errors.New("Site hostname must be exactly one platform label beneath the configured workload_base_domain")
	}
	if digest != "" {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 {
			return errors.New("--site-sha256 must be a 64-character hexadecimal SHA-256 digest")
		}
	}
	return c.probe.VerifyPublicSite(ctx, canonical, *connection.WorkloadBaseDomain, strings.ToLower(digest))
}

func normalizePlatformSiteHostname(hostname, workloadBaseDomain string) (string, error) {
	host, err := domainname.NormalizeHostname(hostname)
	if err != nil {
		return "", err
	}
	base, err := domainname.NormalizeDomain(workloadBaseDomain)
	if err != nil || base != workloadBaseDomain {
		return "", errors.New("invalid configured workload domain")
	}
	suffix := "." + base
	if !strings.HasSuffix(host, suffix) {
		return "", errors.New("Site hostname is outside configured workload domain")
	}
	label := strings.TrimSuffix(host, suffix)
	if label == "" || strings.Contains(label, ".") {
		return "", errors.New("Site hostname must be one label beneath workload domain")
	}
	return host, nil
}

func (c *Controller) current(ctx context.Context) (domain.CloudflareRoutingStatus, domain.CloudflareConnection, error) {
	status, err := c.store.CloudflareRoutingStatus(ctx)
	if err != nil {
		return domain.CloudflareRoutingStatus{}, domain.CloudflareConnection{}, err
	}
	connection, err := c.store.CloudflareConnectionDetails(ctx)
	if err != nil {
		return domain.CloudflareRoutingStatus{}, domain.CloudflareConnection{}, err
	}
	return status, connection, nil
}

func (c *Controller) requirePublicHost(consoleHostname string) error {
	parsed, err := parsePublicURL(c.publicURL)
	if err != nil {
		return err
	}
	canonical, err := domainname.NormalizeHostname(consoleHostname)
	actual, actualErr := domainname.NormalizeHostname(parsed.Hostname())
	if err != nil || actualErr != nil || actual != canonical {
		return errors.New("PUBLIC_APP_URL hostname does not match the saved Cloudflare Console tunnel hostname")
	}
	return nil
}

func requireConnection(status domain.CloudflareRoutingStatus, connection domain.CloudflareConnection) error {
	if !status.Configured || strings.TrimSpace(connection.AccountID) == "" || strings.TrimSpace(connection.TunnelID) == "" ||
		strings.TrimSpace(connection.ConsoleZoneID) == "" || strings.TrimSpace(connection.TunnelName) == "" || strings.TrimSpace(connection.ConsoleHostname) == "" {
		return errors.New("Cloudflare Named Tunnel is not fully configured; reconnect the existing tunnel before changing the Console origin")
	}
	return nil
}

func parsePublicURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("PUBLIC_APP_URL must be the HTTPS Console origin without credentials, query, fragment, or path")
	}
	if _, err := domainname.NormalizeHostname(parsed.Hostname()); err != nil {
		return nil, errors.New("PUBLIC_APP_URL must contain a valid public Console hostname")
	}
	return parsed, nil
}

func safeReason(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Map(func(value rune) rune {
		if value == '\n' || value == '\r' || value == 0 {
			return ' '
		}
		return value
	}, strings.TrimSpace(err.Error()))
	if len(message) > 256 {
		message = message[:256]
	}
	return message
}

func cloudflareIdentityPresent(connection domain.CloudflareConnection) bool {
	return strings.TrimSpace(connection.AccountID) != "" || strings.TrimSpace(connection.TunnelID) != "" ||
		strings.TrimSpace(connection.ConsoleHostname) != "" || strings.TrimSpace(connection.ConsoleZoneID) != ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
