package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/domainname"
)

const tunnelServiceDomain = "cfargotunnel.com"

type RoutingStore interface {
	TryCloudflareReconcileLock(context.Context) (func() error, bool, error)
	CloudflareReconcileSnapshot(context.Context) (domain.CloudflareConnection, error)
	CompleteCloudflareReconcile(context.Context, domain.CloudflareRoutingUpdate) (bool, error)
	CompleteCloudflareConsoleOrigin(context.Context, string, string) (bool, error)
	RecordCloudflareConsoleOriginFailure(context.Context, string, string) error
	ListRetiringCloudflareWildcardDNS(context.Context) ([]domain.CloudflareRetiringWildcardDNS, error)
	CompleteCloudflareWildcardRetirement(context.Context, string) error
	RecordCloudflareReconcileFailure(context.Context, string) error
}

type ClientFactory func(string) (Client, error)

type Reconciler struct {
	store       RoutingStore
	client      ClientFactory
	interval    time.Duration
	logger      *slog.Logger
	httpTimeout time.Duration
}

type ReconcileResult struct {
	LockAcquired          bool
	Changed               bool
	Status                string
	EdgeTLSStatus         string
	EdgeTLSReason         string
	Hostname              string
	Zone                  string
	ConsoleOriginDesired  string
	ConsoleOriginObserved string
}

func NewReconciler(store RoutingStore, client ClientFactory, interval time.Duration, logger *slog.Logger) (*Reconciler, error) {
	if store == nil || client == nil {
		return nil, errors.New("Cloudflare reconciler requires a store and API client")
	}
	if interval < time.Second || interval > 5*time.Minute {
		return nil, errors.New("Cloudflare reconcile interval must be between 1s and 5m")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{store: store, client: client, interval: interval, logger: logger, httpTimeout: 25 * time.Second}, nil
}

// Run performs an immediate startup reconcile, then bounded periodic passes.
// A provider failure is persisted and retried on the next cadence; it does not
// stop unrelated worker capabilities.
func (r *Reconciler) Run(ctx context.Context) error {
	r.runPass(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.runPass(ctx)
		}
	}
}

func (r *Reconciler) runPass(ctx context.Context) {
	origin, originErr := r.ReconcileConsoleOrigin(ctx)
	if errors.Is(originErr, ErrLockNotAcquired) {
		r.logger.Debug("cloudflare console-origin reconcile skipped; another worker or host command holds the lock")
		return
	}
	if originErr != nil {
		r.logger.Error("cloudflare Console-origin reconcile failed", "error", safeReconcileError(originErr, ""))
	} else if origin != "" {
		r.logger.Debug("cloudflare Console-origin ready", "origin", origin)
	}
	result, err := r.Reconcile(ctx)
	switch {
	case errors.Is(err, ErrLockNotAcquired):
		r.logger.Debug("cloudflare reconcile skipped; another worker holds the lock")
	case errors.Is(err, ErrUnauthorized):
		r.logger.Error("cloudflare token unauthorized", "error", safeReconcileError(err, ""))
	case result.EdgeTLSStatus == EdgeTLSError && err != nil:
		r.logger.Error("cloudflare edge TLS inspection failed", "edge_tls_status", result.EdgeTLSStatus, "error", safeReconcileError(err, ""))
	case result.EdgeTLSStatus == EdgeTLSActionRequired:
		r.logger.Error("cloudflare edge TLS action required", "workload_hostname", result.Hostname, "zone", result.Zone, "reason", result.EdgeTLSReason)
	case result.EdgeTLSStatus == EdgeTLSPending:
		r.logger.Info("cloudflare edge TLS pending", "workload_hostname", result.Hostname, "zone", result.Zone, "reason", result.EdgeTLSReason)
	case errors.Is(err, ErrRoutingConflict):
		r.logger.Error("cloudflare reconcile conflict", "error", safeReconcileError(err, ""))
	case err != nil:
		r.logger.Error("cloudflare reconcile failed", "error", safeReconcileError(err, ""))
	case result.Changed:
		r.logger.Info("cloudflare reconcile success", "workload_hostname", result.Hostname, "zone", result.Zone)
	case result.Status == "unconfigured":
		r.logger.Debug("cloudflare reconcile skipped; no Cloudflare connection is configured")
	default:
		r.logger.Debug("cloudflare already converged", "workload_hostname", result.Hostname, "zone", result.Zone)
	}
}

func (r *Reconciler) Reconcile(ctx context.Context) (result ReconcileResult, err error) {
	if r == nil || r.store == nil || r.client == nil {
		return result, errors.New("Cloudflare reconciler is not configured")
	}
	release, acquired, err := r.store.TryCloudflareReconcileLock(ctx)
	if err != nil {
		return result, fmt.Errorf("acquire Cloudflare reconcile lock: %w", err)
	}
	if !acquired {
		return result, ErrLockNotAcquired
	}
	result.LockAcquired = true
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release Cloudflare reconcile lock: %w", releaseErr))
		}
	}()

	connection, err := r.store.CloudflareReconcileSnapshot(ctx)
	if err != nil {
		return result, fmt.Errorf("read Cloudflare desired state: %w", err)
	}
	result.Status = connection.Status
	result.ConsoleOriginDesired = connection.ConsoleOriginDesired
	if !hasCloudflareConnectionIntent(connection) {
		// workload_base_domain is provider-neutral. An instance without a
		// Cloudflare identity and credential is deliberately outside this
		// reconciler, even when it has a workload domain configured.
		result.Status = "unconfigured"
		result.EdgeTLSStatus = EdgeTLSNotApplicable
		return result, nil
	}
	r.logger.Info("cloudflare reconcile start", "tunnel_id", connection.TunnelID)
	if !connectionConfigured(connection) {
		result.Status = "error"
		err = errors.New("Cloudflare connection is unavailable; an Instance Owner must reconnect the scoped token")
		return result, r.recordFailure(ctx, connection.APIToken, err)
	}
	if connection.ConsoleOriginDesired != ConsoleOriginProxy && connection.ConsoleOriginDesired != ConsoleOriginTraefik {
		return result, r.recordFailure(ctx, connection.APIToken, errors.New("saved Console origin is invalid; run stealth ingress rollback"))
	}
	provider, err := r.client(connection.APIToken)
	if err != nil {
		return result, r.recordFailure(ctx, connection.APIToken, errors.New("Cloudflare API client could not be initialized"))
	}
	providerCtx, cancel := context.WithTimeout(ctx, r.httpTimeout)
	defer cancel()

	zones, err := validateExistingConnection(providerCtx, provider, connection.AccountID, connection.ConsoleZoneID, connection.ConsoleHostname, connection.TunnelID, connection.TunnelName)
	if err != nil {
		return result, r.recordFailure(ctx, connection.APIToken, err)
	}

	var workloadZone Zone
	var wildcardHostname, wildcardRecordID string
	if connection.WorkloadBaseDomain != nil {
		workloadDomain, normalizeErr := domainname.NormalizeDomain(*connection.WorkloadBaseDomain)
		if normalizeErr != nil || workloadDomain != *connection.WorkloadBaseDomain {
			return result, r.recordFailure(ctx, connection.APIToken, errors.New("saved workload domain is invalid; correct the Instance domain setting"))
		}
		workloadZone, err = longestContainingZone(zones, workloadDomain)
		if err != nil {
			return result, r.recordFailure(ctx, connection.APIToken, err)
		}
		wildcardHostname = "*." + workloadDomain
		record, changed, ensureErr := ensureWildcardDNS(providerCtx, provider, workloadZone.ID, wildcardHostname, connection.TunnelID+"."+tunnelServiceDomain, connection.WorkloadZoneID, connection.WildcardHostname, connection.WildcardRecordID)
		if ensureErr != nil {
			return result, r.recordFailure(ctx, connection.APIToken, ensureErr)
		}
		wildcardRecordID = record.ID
		result.Changed = result.Changed || changed
		if changed {
			r.logger.Info("cloudflare wildcard DNS reconciled", "hostname", wildcardHostname, "zone", workloadZone.Name, "record_id", record.ID)
		}
	}
	result.Hostname = wildcardHostname
	result.Zone = workloadZone.Name

	desiredIngress := desiredTunnelIngress(connection.ConsoleHostname, wildcardHostname, connection.ConsoleOriginDesired)
	currentIngress, err := provider.TunnelConfiguration(providerCtx, connection.AccountID, connection.TunnelID)
	if err != nil {
		return result, r.recordFailure(ctx, connection.APIToken, err)
	}
	if !sameIngress(currentIngress, desiredIngress) {
		if err := provider.ConfigureTunnel(providerCtx, connection.AccountID, connection.TunnelID, desiredIngress); err != nil {
			return result, r.recordFailure(ctx, connection.APIToken, err)
		}
		verified, err := provider.TunnelConfiguration(providerCtx, connection.AccountID, connection.TunnelID)
		if err != nil {
			return result, r.recordFailure(ctx, connection.APIToken, err)
		}
		if !sameIngress(verified, desiredIngress) {
			return result, r.recordFailure(ctx, connection.APIToken, errors.New("Cloudflare tunnel ingress did not match the requested Console and workload routes"))
		}
		result.Changed = true
		r.logger.Info("cloudflare tunnel ingress updated", "console_origin", consoleOriginService(connection.ConsoleOriginDesired), "workload_origin", workloadOrigin(wildcardHostname))
	}
	result.ConsoleOriginObserved = connection.ConsoleOriginDesired

	tlsObservation := edgeTLSObservation{Status: EdgeTLSNotApplicable}
	var tlsInspectionErr error
	if connection.WorkloadBaseDomain != nil {
		tlsObservation, tlsInspectionErr = inspectWorkloadEdgeTLS(providerCtx, provider, workloadZone, *connection.WorkloadBaseDomain)
	}
	tlsObservation.Reason = boundedEdgeTLSReason(tlsObservation.Reason)
	result.EdgeTLSStatus = tlsObservation.Status
	result.EdgeTLSReason = tlsObservation.Reason

	update := domain.CloudflareRoutingUpdate{
		ExpectedWorkloadBaseDomain: cloneString(connection.WorkloadBaseDomain),
		EdgeTLSStatus:              tlsObservation.Status,
		EdgeTLSError:               tlsObservation.Reason,
		ConsoleOriginDesired:       connection.ConsoleOriginDesired,
		ConsoleOriginObserved:      result.ConsoleOriginObserved,
	}
	if connection.WorkloadBaseDomain != nil {
		update.WorkloadZoneID = workloadZone.ID
		update.WorkloadZoneName = workloadZone.Name
		update.WildcardHostname = wildcardHostname
		update.WildcardRecordID = wildcardRecordID
	}
	completed, err := r.store.CompleteCloudflareReconcile(ctx, update)
	if err != nil {
		return result, r.recordFailure(ctx, connection.APIToken, fmt.Errorf("persist Cloudflare observed state: %w", err))
	}
	if !completed {
		result.Status = "pending"
		return result, nil
	}
	result.Status = cloudflareRoutingStatus(tlsObservation.Status)
	if tlsInspectionErr != nil {
		if errors.Is(tlsInspectionErr, ErrUnauthorized) {
			return result, ErrUnauthorized
		}
		return result, errors.New(safeReconcileError(tlsInspectionErr, connection.APIToken))
	}
	if result.Status != "ready" {
		return result, nil
	}
	retired, err := r.cleanupRetiring(providerCtx, provider)
	if err != nil {
		return result, r.recordFailure(ctx, connection.APIToken, err)
	}
	result.Changed = result.Changed || retired
	if result.Changed {
		r.logger.Info("cloudflare reconcile success", "workload_hostname", result.Hostname, "zone", result.Zone)
	}
	return result, nil
}

// ReconcileConsoleOrigin is the emergency-safe origin transition used by
// host-initiated cutover and rollback. It shares the distributed Cloudflare
// lock and only changes the Console rule in the existing Named Tunnel config.
// Workload DNS, certificates and retiring records are deliberately outside
// this recovery primitive.
func (r *Reconciler) ReconcileConsoleOrigin(ctx context.Context) (origin string, err error) {
	if r == nil || r.store == nil || r.client == nil {
		return "", errors.New("Cloudflare reconciler is not configured")
	}
	release, acquired, err := r.store.TryCloudflareReconcileLock(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire Cloudflare reconcile lock: %w", err)
	}
	if !acquired {
		return "", ErrLockNotAcquired
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release Cloudflare reconcile lock: %w", releaseErr))
		}
	}()

	connection, err := r.store.CloudflareReconcileSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read Cloudflare desired state: %w", err)
	}
	if !hasCloudflareConnectionIntent(connection) {
		return "", nil
	}
	desired := connection.ConsoleOriginDesired
	if desired != ConsoleOriginProxy && desired != ConsoleOriginTraefik {
		return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, errors.New("saved Console origin is invalid; run stealth ingress rollback"))
	}
	if !connectionConfigured(connection) {
		return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, errors.New("Cloudflare connection is unavailable; an Instance Owner must reconnect the scoped token for the existing Named Tunnel"))
	}
	provider, err := r.client(connection.APIToken)
	if err != nil {
		return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, errors.New("Cloudflare API client could not be initialized"))
	}
	providerCtx, cancel := context.WithTimeout(ctx, r.httpTimeout)
	defer cancel()
	if err := validateExistingTunnel(providerCtx, provider, connection.AccountID, connection.TunnelID, connection.TunnelName); err != nil {
		return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, err)
	}
	current, err := provider.TunnelConfiguration(providerCtx, connection.AccountID, connection.TunnelID)
	if err != nil {
		return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, err)
	}
	want, observed, err := patchConsoleIngress(current, connection.ConsoleHostname, connection.WildcardHostname, desired)
	if err != nil {
		return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, err)
	}
	if observed != desired {
		if err := provider.ConfigureTunnel(providerCtx, connection.AccountID, connection.TunnelID, want); err != nil {
			return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, err)
		}
	}
	verified, err := provider.TunnelConfiguration(providerCtx, connection.AccountID, connection.TunnelID)
	if err != nil {
		return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, err)
	}
	if !sameTunnelRules(verified, want) {
		return "", r.recordConsoleOriginFailure(ctx, connection.APIToken, desired, errors.New("Cloudflare Tunnel Console-origin read-back did not match the requested change; HIGH SEVERITY: provider rollback is unverified"))
	}
	completed, err := r.store.CompleteCloudflareConsoleOrigin(ctx, desired, desired)
	if err != nil {
		return "", fmt.Errorf("persist Cloudflare Console-origin observation: %w", err)
	}
	if !completed {
		return "", ErrCloudflareDesiredStateChanged
	}
	r.logger.Info("cloudflare console origin reconciled", "desired_origin", desired, "observed_origin", desired, "changed", observed != desired)
	return desired, nil
}

// VerifyConsoleOrigin checks only the existing Tunnel's Console route. It is
// intentionally independent of workload zone and certificate readiness.
func (r *Reconciler) VerifyConsoleOrigin(ctx context.Context) (origin string, err error) {
	if r == nil || r.store == nil || r.client == nil {
		return "", errors.New("Cloudflare reconciler is not configured")
	}
	release, acquired, err := r.store.TryCloudflareReconcileLock(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire Cloudflare reconcile lock: %w", err)
	}
	if !acquired {
		return "", ErrLockNotAcquired
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release Cloudflare reconcile lock: %w", releaseErr))
		}
	}()
	connection, err := r.store.CloudflareReconcileSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read Cloudflare desired state: %w", err)
	}
	if !connectionConfigured(connection) {
		return "", errors.New("Cloudflare Named Tunnel is not fully configured")
	}
	if connection.ConsoleOriginDesired != ConsoleOriginProxy && connection.ConsoleOriginDesired != ConsoleOriginTraefik {
		return "", errors.New("saved Console origin is invalid")
	}
	provider, err := r.client(connection.APIToken)
	if err != nil {
		return "", errors.New("Cloudflare API client could not be initialized")
	}
	providerCtx, cancel := context.WithTimeout(ctx, r.httpTimeout)
	defer cancel()
	if err := validateExistingTunnel(providerCtx, provider, connection.AccountID, connection.TunnelID, connection.TunnelName); err != nil {
		return "", err
	}
	current, err := provider.TunnelConfiguration(providerCtx, connection.AccountID, connection.TunnelID)
	if err != nil {
		return "", err
	}
	_, observed, err := patchConsoleIngress(current, connection.ConsoleHostname, connection.WildcardHostname, connection.ConsoleOriginDesired)
	if err != nil {
		return "", err
	}
	if observed != connection.ConsoleOriginDesired {
		return "", errors.New("Cloudflare Tunnel Console origin differs from durable desired state")
	}
	return observed, nil
}

// VerifyProvider reads the current durable desired state and Cloudflare
// Tunnel configuration without changing either. It shares the reconciler's
// PostgreSQL lock so an operator verification cannot observe a mid-write
// configuration from another worker.
func (r *Reconciler) VerifyProvider(ctx context.Context) (origin string, err error) {
	if r == nil || r.store == nil || r.client == nil {
		return "", errors.New("Cloudflare reconciler is not configured")
	}
	release, acquired, err := r.store.TryCloudflareReconcileLock(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire Cloudflare reconcile lock: %w", err)
	}
	if !acquired {
		return "", ErrLockNotAcquired
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release Cloudflare reconcile lock: %w", releaseErr))
		}
	}()

	connection, err := r.store.CloudflareReconcileSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read Cloudflare desired state: %w", err)
	}
	if !connectionConfigured(connection) {
		return "", errors.New("Cloudflare Named Tunnel is not fully configured")
	}
	if connection.ConsoleOriginDesired != ConsoleOriginProxy && connection.ConsoleOriginDesired != ConsoleOriginTraefik {
		return "", errors.New("saved Console origin is invalid")
	}
	provider, err := r.client(connection.APIToken)
	if err != nil {
		return "", errors.New("Cloudflare API client could not be initialized")
	}
	providerCtx, cancel := context.WithTimeout(ctx, r.httpTimeout)
	defer cancel()
	zones, err := validateExistingConnection(providerCtx, provider, connection.AccountID, connection.ConsoleZoneID, connection.ConsoleHostname, connection.TunnelID, connection.TunnelName)
	if err != nil {
		return "", err
	}
	wildcardHostname := ""
	if connection.WorkloadBaseDomain != nil {
		workloadDomain, normalizeErr := domainname.NormalizeDomain(*connection.WorkloadBaseDomain)
		if normalizeErr != nil || workloadDomain != *connection.WorkloadBaseDomain {
			return "", errors.New("saved workload domain is invalid")
		}
		if _, err := longestContainingZone(zones, workloadDomain); err != nil {
			return "", err
		}
		wildcardHostname = "*." + workloadDomain
	}
	current, err := provider.TunnelConfiguration(providerCtx, connection.AccountID, connection.TunnelID)
	if err != nil {
		return "", err
	}
	want := desiredTunnelIngress(connection.ConsoleHostname, wildcardHostname, connection.ConsoleOriginDesired)
	if !sameIngress(current, want) {
		return "", errors.New("Cloudflare Tunnel ingress differs from the durable Console and workload routing contract")
	}
	return connection.ConsoleOriginDesired, nil
}

// ValidateExistingTunnel validates a replacement token against the durable
// tunnel identity and discovers the Console DNS record without changing any
// provider state. If zoneID is empty, it uses longest-suffix zone discovery.
func ValidateExistingTunnel(ctx context.Context, client Client, accountID, zoneID, consoleHostname, tunnelID, expectedTunnelName string, workloadBaseDomain *string) (domain.CloudflareConnection, error) {
	if client == nil {
		return domain.CloudflareConnection{}, errors.New("Cloudflare client is unavailable")
	}
	accountID = strings.TrimSpace(accountID)
	tunnelID = strings.TrimSpace(tunnelID)
	consoleHostname, err := domainname.NormalizeHostname(consoleHostname)
	if err != nil || accountID == "" || tunnelID == "" {
		return domain.CloudflareConnection{}, errors.New("Cloudflare account, tunnel, and Console hostname are required")
	}
	accounts, err := client.ListAccounts(ctx)
	if err != nil {
		return domain.CloudflareConnection{}, err
	}
	if !containsAccount(accounts, accountID) {
		return domain.CloudflareConnection{}, errors.New("Cloudflare token cannot access the configured account")
	}
	zones, err := client.ListZones(ctx, accountID)
	if err != nil {
		return domain.CloudflareConnection{}, err
	}
	var consoleZone Zone
	if zoneID != "" {
		for _, zone := range zones {
			if zone.ID == zoneID && zoneContainsName(zone.Name, consoleHostname) {
				consoleZone = zone
				break
			}
		}
		if consoleZone.ID == "" {
			return domain.CloudflareConnection{}, errors.New("Cloudflare token cannot read the configured Console zone")
		}
	} else {
		consoleZone, err = longestContainingZone(zones, consoleHostname)
		if err != nil {
			return domain.CloudflareConnection{}, fmt.Errorf("Console zone discovery failed: %w", err)
		}
	}
	status, err := client.TunnelStatus(ctx, accountID, tunnelID)
	if err != nil {
		return domain.CloudflareConnection{}, err
	}
	if status.ID != "" && status.ID != tunnelID {
		return domain.CloudflareConnection{}, errors.New("Cloudflare returned a different tunnel identity")
	}
	tunnelName := strings.TrimSpace(status.Name)
	if expectedTunnelName != "" && tunnelName != "" && tunnelName != expectedTunnelName {
		return domain.CloudflareConnection{}, errors.New("Cloudflare tunnel name does not match the saved connection")
	}
	if tunnelName == "" {
		tunnelName = strings.TrimSpace(expectedTunnelName)
	}
	if tunnelName == "" {
		return domain.CloudflareConnection{}, errors.New("Cloudflare did not return the existing tunnel name")
	}
	ingress, err := client.TunnelConfiguration(ctx, accountID, tunnelID)
	if err != nil {
		return domain.CloudflareConnection{}, err
	}
	if !hasConsoleRouteAndCatchAll(ingress, consoleHostname) {
		return domain.CloudflareConnection{}, errors.New("existing tunnel does not contain a supported Console origin and 404 catch-all")
	}
	consoleRecords, err := client.ListDNSRecords(ctx, consoleZone.ID, consoleHostname)
	if err != nil {
		return domain.CloudflareConnection{}, validationProviderError("Cloudflare token cannot read the Console DNS record", err)
	}
	consoleRecord, err := findMatchingDNS(consoleRecords, consoleHostname, tunnelID+"."+tunnelServiceDomain)
	if err != nil {
		return domain.CloudflareConnection{}, fmt.Errorf("Console DNS validation failed: %w", err)
	}
	if workloadBaseDomain != nil {
		workloadDomain, normalizeErr := domainname.NormalizeDomain(*workloadBaseDomain)
		if normalizeErr != nil {
			return domain.CloudflareConnection{}, errors.New("workload domain is invalid")
		}
		workloadZone, zoneErr := longestContainingZone(zones, workloadDomain)
		if zoneErr != nil {
			return domain.CloudflareConnection{}, zoneErr
		}
		if _, err := client.ListDNSRecords(ctx, workloadZone.ID, "*."+workloadDomain); err != nil {
			return domain.CloudflareConnection{}, validationProviderError("Cloudflare token cannot read the workload DNS zone", err)
		}
		if _, err := client.ListCertificatePacks(ctx, workloadZone.ID); err != nil {
			return domain.CloudflareConnection{}, validationProviderError("Cloudflare token cannot inspect edge certificates; grant SSL and Certificates Read for the workload zone", err)
		}
		// The settings are read-only and informational; Total TLS is not
		// accepted as proof because Cloudflare excludes Tunnel hostnames.
		_, _ = client.TotalTLSSettings(ctx, workloadZone.ID)
	}
	return domain.CloudflareConnection{
		AccountID: accountID, ConsoleZoneID: consoleZone.ID, ConsoleHostname: consoleHostname,
		TunnelID: tunnelID, TunnelName: tunnelName, ConsoleRecordID: consoleRecord.ID,
	}, nil
}

func validationProviderError(message string, err error) error {
	if errors.Is(err, ErrUnauthorized) {
		return fmt.Errorf("%s: %w", message, ErrUnauthorized)
	}
	return errors.New(message)
}

func validateExistingConnection(ctx context.Context, client Client, accountID, consoleZoneID, consoleHostname, tunnelID, tunnelName string) ([]Zone, error) {
	if err := validateExistingTunnel(ctx, client, accountID, tunnelID, tunnelName); err != nil {
		return nil, err
	}
	zones, err := client.ListZones(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !zoneContainsHostname(zones, consoleZoneID, consoleHostname) {
		return nil, errors.New("Cloudflare token cannot read the configured Console zone")
	}
	return zones, nil
}

func validateExistingTunnel(ctx context.Context, client Client, accountID, tunnelID, tunnelName string) error {
	accounts, err := client.ListAccounts(ctx)
	if err != nil {
		return err
	}
	if !containsAccount(accounts, accountID) {
		return errors.New("Cloudflare token cannot access the configured account")
	}
	status, err := client.TunnelStatus(ctx, accountID, tunnelID)
	if err != nil {
		return err
	}
	if status.ID != "" && status.ID != tunnelID {
		return errors.New("Cloudflare returned a different tunnel identity")
	}
	if tunnelName != "" && status.Name != "" && status.Name != tunnelName {
		return errors.New("configured Cloudflare tunnel identity changed")
	}
	return nil
}

func connectionConfigured(connection domain.CloudflareConnection) bool {
	return strings.TrimSpace(connection.APIToken) != "" && strings.TrimSpace(connection.AccountID) != "" && strings.TrimSpace(connection.ConsoleZoneID) != "" && strings.TrimSpace(connection.ConsoleHostname) != "" && strings.TrimSpace(connection.TunnelID) != "" && strings.TrimSpace(connection.TunnelName) != "" && strings.TrimSpace(connection.ConsoleRecordID) != ""
}

func hasCloudflareConnectionIntent(connection domain.CloudflareConnection) bool {
	return strings.TrimSpace(connection.APIToken) != "" || strings.TrimSpace(connection.AccountID) != "" ||
		strings.TrimSpace(connection.ConsoleZoneID) != "" || strings.TrimSpace(connection.ConsoleHostname) != "" ||
		strings.TrimSpace(connection.TunnelID) != "" || strings.TrimSpace(connection.TunnelName) != "" ||
		strings.TrimSpace(connection.ConsoleRecordID) != ""
}

func cloudflareRoutingStatus(edgeTLSStatus string) string {
	switch edgeTLSStatus {
	case EdgeTLSNotApplicable, EdgeTLSReady:
		return "ready"
	case EdgeTLSPending:
		return "pending"
	default:
		return "error"
	}
}

func boundedEdgeTLSReason(reason string) string {
	reason = strings.Map(func(r rune) rune {
		if r == '\x00' || r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, strings.TrimSpace(reason))
	if len(reason) > 512 {
		return reason[:512]
	}
	return reason
}

const (
	ConsoleOriginProxy   = "proxy"
	ConsoleOriginTraefik = "traefik"
)

func consoleOriginService(origin string) string {
	switch origin {
	case ConsoleOriginProxy:
		return "http://proxy:80"
	case ConsoleOriginTraefik:
		return "http://traefik:8080"
	default:
		return ""
	}
}

func desiredTunnelIngress(consoleHostname, wildcardHostname string, consoleOrigins ...string) []IngressRule {
	consoleOrigin := ConsoleOriginProxy
	if len(consoleOrigins) > 0 && consoleOriginService(consoleOrigins[0]) != "" {
		consoleOrigin = consoleOrigins[0]
	}
	result := []IngressRule{consoleIngressRule(consoleHostname, consoleOrigin)}
	if wildcardHostname != "" {
		result = append(result, IngressRule{Hostname: wildcardHostname, Service: "http://traefik:8080"})
	}
	return append(result, IngressRule{Service: "http_status:404"})
}

func consoleIngressRule(hostname, origin string) IngressRule {
	return IngressRule{Hostname: hostname, Service: consoleOriginService(origin)}
}

func patchConsoleIngress(rules []IngressRule, consoleHostname, workloadHostname, desiredOrigin string) ([]IngressRule, string, error) {
	if len(rules) == 0 || len(rules) > 64 || (desiredOrigin != ConsoleOriginProxy && desiredOrigin != ConsoleOriginTraefik) {
		return nil, "", errors.New("Cloudflare Tunnel ingress is structurally invalid for a Console-origin change")
	}
	consoleName, err := domainname.NormalizeHostname(consoleHostname)
	if err != nil {
		return nil, "", errors.New("saved Console hostname is invalid")
	}
	wildcardName := ""
	if workloadHostname != "" {
		wildcardName, err = canonicalWildcardHostname(workloadHostname)
		if err != nil {
			return nil, "", errors.New("saved workload wildcard hostname is invalid")
		}
	}
	consoleIndex, catchAllCount := -1, 0
	seen := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		if strings.TrimSpace(rule.Service) == "" {
			return nil, "", errors.New("Cloudflare Tunnel ingress contains an empty service")
		}
		if rule.Hostname == "" {
			catchAllCount++
			if index != len(rules)-1 || rule.Service != "http_status:404" {
				return nil, "", errors.New("Cloudflare Tunnel catch-all is unsafe; expected a final http_status:404 rule")
			}
			continue
		}
		name, nameErr := canonicalIngressHostname(rule.Hostname)
		if nameErr != nil {
			return nil, "", errors.New("Cloudflare Tunnel ingress contains an invalid hostname")
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, "", errors.New("Cloudflare Tunnel ingress contains duplicate hostname rules; refusing an ambiguous Console-origin change")
		}
		seen[name] = struct{}{}
		if name == consoleName {
			if consoleIndex >= 0 {
				return nil, "", errors.New("Cloudflare Tunnel has multiple Console rules; refusing an ambiguous origin change")
			}
			consoleIndex = index
			if rule.Service != "http://proxy:80" && rule.Service != "http://traefik:8080" {
				return nil, "", errors.New("Cloudflare Console rule points to an unsupported origin; HIGH SEVERITY: refusing to overwrite provider configuration")
			}
		}
		if wildcardName != "" && name == wildcardName && rule.Service != "http://traefik:8080" {
			return nil, "", errors.New("Cloudflare workload rule points to an unexpected origin; HIGH SEVERITY: refusing a Console-origin change")
		}
	}
	if catchAllCount != 1 || consoleIndex < 0 {
		return nil, "", errors.New("Cloudflare Tunnel ingress is missing a unique Console rule or final 404 catch-all")
	}
	// A missing known workload rule is not ambiguous for this operation. Keep
	// whatever is present untouched and let the full workload reconciler repair
	// it after the Console rule has converged.
	observed := ConsoleOriginProxy
	if rules[consoleIndex].Service == "http://traefik:8080" {
		observed = ConsoleOriginTraefik
	}
	result := append([]IngressRule(nil), rules...)
	result[consoleIndex] = cloneIngressRule(result[consoleIndex])
	result[consoleIndex].Service = consoleIngressRule(consoleHostname, desiredOrigin).Service
	return result, observed, nil
}

func cloneIngressRule(rule IngressRule) IngressRule {
	clone := rule
	clone.Origin = append(rule.Origin[:0:0], rule.Origin...)
	if rule.Extra != nil {
		clone.Extra = make(map[string]json.RawMessage, len(rule.Extra))
		for key, value := range rule.Extra {
			clone.Extra[key] = append(json.RawMessage(nil), value...)
		}
	}
	return clone
}

func sameTunnelRules(left, right []IngressRule) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !sameIngressHostname(left[i].Hostname, right[i].Hostname) || left[i].Service != right[i].Service || !sameJSON(left[i].Origin, right[i].Origin) || !sameExtra(left[i].Extra, right[i].Extra) {
			return false
		}
	}
	return true
}

func sameIngressHostname(left, right string) bool {
	if left == "" || right == "" {
		return left == right
	}
	leftName, leftErr := canonicalIngressHostname(left)
	rightName, rightErr := canonicalIngressHostname(right)
	return leftErr == nil && rightErr == nil && leftName == rightName
}

func sameJSON(left, right []byte) bool {
	if len(left) == 0 || string(left) == "null" {
		return len(right) == 0 || string(right) == "null"
	}
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func sameExtra(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if !sameJSON(value, right[key]) {
			return false
		}
	}
	return true
}

func canonicalIngressHostname(hostname string) (string, error) {
	if strings.HasPrefix(hostname, "*.") {
		wildcard, err := canonicalWildcardHostname(hostname)
		return wildcard, err
	}
	return domainname.NormalizeHostname(hostname)
}

func canonicalWildcardHostname(hostname string) (string, error) {
	if !strings.HasPrefix(hostname, "*.") || strings.Count(hostname, "*") != 1 {
		return "", errors.New("invalid wildcard hostname")
	}
	base, err := domainname.NormalizeDomain(strings.TrimPrefix(hostname, "*."))
	if err != nil {
		return "", err
	}
	return "*." + base, nil
}

func sameIngress(left, right []IngressRule) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if canonicalDNSName(left[i].Hostname) != canonicalDNSName(right[i].Hostname) || left[i].Service != right[i].Service {
			return false
		}
	}
	return true
}

func hasConsoleRouteAndCatchAll(rules []IngressRule, consoleHostname string) bool {
	hasConsole, hasCatchAll := false, false
	for _, rule := range rules {
		if canonicalDNSName(rule.Hostname) == canonicalDNSName(consoleHostname) && (rule.Service == "http://proxy:80" || rule.Service == "http://traefik:8080") {
			hasConsole = true
		}
		if rule.Hostname == "" && rule.Service == "http_status:404" {
			hasCatchAll = true
		}
	}
	return hasConsole && hasCatchAll
}

func ensureWildcardDNS(ctx context.Context, client Client, zoneID, hostname, target, oldZoneID, oldHostname, storedRecordID string) (DNSRecord, bool, error) {
	var changed bool
	if storedRecordID != "" && oldZoneID == zoneID && canonicalDNSName(oldHostname) == canonicalDNSName(hostname) {
		stored, err := client.GetDNSRecord(ctx, zoneID, storedRecordID)
		if err == nil {
			if stored.ID != storedRecordID || !exactOwnedWildcard(stored, hostname, target) {
				return DNSRecord{}, false, fmt.Errorf("%w: stored record %s no longer matches %s and the Stealth tunnel", ErrRoutingConflict, storedRecordID, hostname)
			}
			if stored.Proxied && stored.TTL == 1 {
				return stored, false, nil
			}
			updated, err := client.UpdateDNSRecord(ctx, zoneID, storedRecordID, DNSRecord{Type: "CNAME", Name: hostname, Content: target, Proxied: true, TTL: 1})
			if err != nil {
				return DNSRecord{}, false, fmt.Errorf("restore wildcard DNS proxy and TTL: %w", err)
			}
			if updated.ID != storedRecordID || !exactOwnedWildcard(updated, hostname, target) || !updated.Proxied || updated.TTL != 1 {
				return DNSRecord{}, false, fmt.Errorf("%w: Cloudflare returned an unexpected wildcard DNS record after update", ErrRoutingConflict)
			}
			return updated, true, nil
		}
		if !errors.Is(err, ErrResourceNotFound) {
			return DNSRecord{}, false, fmt.Errorf("verify stored wildcard DNS identity: %w", err)
		}
	}
	records, err := client.ListDNSRecords(ctx, zoneID, hostname)
	if err != nil {
		return DNSRecord{}, false, fmt.Errorf("list wildcard DNS records: %w", err)
	}
	matching, err := findMatchingDNS(records, hostname, target)
	if err != nil {
		return DNSRecord{}, false, err
	}
	if matching.ID == "" {
		matching, err = client.CreateDNSRecord(ctx, zoneID, DNSRecord{Type: "CNAME", Name: hostname, Content: target, Proxied: true, TTL: 1})
		if err != nil {
			return DNSRecord{}, false, fmt.Errorf("create wildcard DNS record: %w", err)
		}
		if !exactOwnedWildcard(matching, hostname, target) || !matching.Proxied || matching.TTL != 1 {
			return DNSRecord{}, false, fmt.Errorf("%w: Cloudflare returned an unexpected wildcard DNS record after create", ErrRoutingConflict)
		}
		changed = true
	}
	if !matching.Proxied || matching.TTL != 1 {
		matchingID := matching.ID
		matching, err = client.UpdateDNSRecord(ctx, zoneID, matching.ID, DNSRecord{Type: "CNAME", Name: hostname, Content: target, Proxied: true, TTL: 1})
		if err != nil {
			return DNSRecord{}, false, fmt.Errorf("configure wildcard DNS record: %w", err)
		}
		if matching.ID != matchingID || !exactOwnedWildcard(matching, hostname, target) || !matching.Proxied || matching.TTL != 1 {
			return DNSRecord{}, false, fmt.Errorf("%w: Cloudflare returned an unexpected wildcard DNS record after update", ErrRoutingConflict)
		}
		changed = true
	}
	return matching, changed, nil
}

func findMatchingDNS(records []DNSRecord, hostname, target string) (DNSRecord, error) {
	hostname, target = canonicalDNSName(hostname), canonicalDNSName(target)
	var matching DNSRecord
	for _, record := range records {
		if canonicalDNSName(record.Name) != hostname {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(record.Type)) != "CNAME" {
			return DNSRecord{}, fmt.Errorf("%w: %s already has an incompatible record type", ErrRoutingConflict, hostname)
		}
		if canonicalDNSName(record.Content) != target {
			return DNSRecord{}, fmt.Errorf("%w: %s already points to another CNAME target", ErrRoutingConflict, hostname)
		}
		if strings.TrimSpace(record.ID) == "" {
			return DNSRecord{}, errors.New("Cloudflare returned an incomplete wildcard DNS record identity")
		}
		if matching.ID != "" {
			return DNSRecord{}, fmt.Errorf("%w: multiple CNAME records exist for %s", ErrRoutingConflict, hostname)
		}
		matching = record
	}
	return matching, nil
}

func exactOwnedWildcard(record DNSRecord, hostname, target string) bool {
	return strings.TrimSpace(record.ID) != "" && strings.EqualFold(strings.TrimSpace(record.Type), "CNAME") && canonicalDNSName(record.Name) == canonicalDNSName(hostname) && canonicalDNSName(record.Content) == canonicalDNSName(target)
}

func (r *Reconciler) cleanupRetiring(ctx context.Context, client Client) (bool, error) {
	retiring, err := r.store.ListRetiringCloudflareWildcardDNS(ctx)
	if err != nil {
		return false, fmt.Errorf("list retiring wildcard records: %w", err)
	}
	changed := false
	for _, item := range retiring {
		record, err := client.GetDNSRecord(ctx, item.ZoneID, item.RecordID)
		if errors.Is(err, ErrResourceNotFound) {
			if err := r.store.CompleteCloudflareWildcardRetirement(ctx, item.RecordID); err != nil {
				return changed, fmt.Errorf("complete missing wildcard cleanup record: %w", err)
			}
			changed = true
			continue
		}
		if err != nil {
			return changed, fmt.Errorf("revalidate old wildcard DNS record: %w", err)
		}
		if record.ID != item.RecordID || !exactOwnedWildcard(record, item.Hostname, item.Target) {
			return changed, fmt.Errorf("%w: refusing to delete stored record %s because its hostname or target changed", ErrRoutingConflict, item.RecordID)
		}
		if err := client.DeleteDNSRecord(ctx, item.ZoneID, item.RecordID); err != nil && !errors.Is(err, ErrResourceNotFound) {
			return changed, fmt.Errorf("delete revalidated old wildcard DNS record: %w", err)
		}
		if err := r.store.CompleteCloudflareWildcardRetirement(ctx, item.RecordID); err != nil {
			return changed, fmt.Errorf("complete wildcard cleanup record: %w", err)
		}
		changed = true
		r.logger.Info("cloudflare wildcard DNS deleted", "hostname", item.Hostname, "zone_id", item.ZoneID)
	}
	return changed, nil
}

func longestContainingZone(zones []Zone, hostname string) (Zone, error) {
	hostname = canonicalDNSName(hostname)
	var matches []Zone
	longest := 0
	for _, zone := range zones {
		zoneName, err := domainname.NormalizeDomain(zone.Name)
		if err != nil || !zoneContainsName(zoneName, hostname) {
			continue
		}
		if len(zoneName) > longest {
			matches = matches[:0]
			longest = len(zoneName)
		}
		if len(zoneName) == longest {
			matches = append(matches, Zone{ID: zone.ID, Name: zoneName, Status: zone.Status})
		}
	}
	if len(matches) == 0 {
		return Zone{}, errors.New("Cloudflare token cannot access a DNS zone containing the workload domain; add Zone Read and DNS Edit access for its zone")
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	if len(matches) > 1 && matches[0].Name == matches[1].Name {
		return Zone{}, errors.New("Cloudflare returned multiple equally specific zones for the workload domain")
	}
	return matches[0], nil
}

func zoneContainsName(zoneName, hostname string) bool {
	zoneName = canonicalDNSName(zoneName)
	hostname = canonicalDNSName(hostname)
	return zoneName != "" && (hostname == zoneName || strings.HasSuffix(hostname, "."+zoneName))
}

func containsAccount(accounts []Account, id string) bool {
	for _, account := range accounts {
		if strings.TrimSpace(account.ID) == strings.TrimSpace(id) {
			return true
		}
	}
	return false
}

func workloadOrigin(hostname string) string {
	if hostname == "" {
		return ""
	}
	return "http://traefik:8080"
}

func (r *Reconciler) recordFailure(ctx context.Context, token string, err error) error {
	message := safeReconcileError(err, token)
	if persistErr := r.store.RecordCloudflareReconcileFailure(ctx, message); persistErr != nil {
		return errors.Join(fmt.Errorf("%s", message), fmt.Errorf("persist Cloudflare error state: %w", persistErr))
	}
	if errors.Is(err, ErrRoutingConflict) {
		r.logger.Error("cloudflare reconcile conflict", "error", message)
		return fmt.Errorf("%w: %s", ErrRoutingConflict, message)
	}
	r.logger.Error("cloudflare provider unavailable", "error", message)
	if errors.Is(err, ErrUnauthorized) {
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	}
	return errors.New(message)
}

func (r *Reconciler) recordConsoleOriginFailure(ctx context.Context, token, expected string, err error) error {
	message := safeReconcileError(err, token)
	if persistErr := r.store.RecordCloudflareConsoleOriginFailure(ctx, expected, message); persistErr != nil {
		return errors.Join(errors.New(message), fmt.Errorf("persist Cloudflare Console-origin error state: %w", persistErr))
	}
	r.logger.Error("cloudflare Console-origin reconcile failed", "error", message)
	if errors.Is(err, ErrUnauthorized) {
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	}
	return errors.New(message)
}

func safeReconcileError(err error, token string) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrUnauthorized) {
		return "Cloudflare token was rejected or lacks required permissions; reconnect a scoped token with access to the account and required zones"
	}
	message := strings.TrimSpace(err.Error())
	if token != "" {
		message = strings.ReplaceAll(message, token, "[redacted]")
	}
	message = strings.Map(func(r rune) rune {
		if r == '\x00' || r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, message)
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var (
	ErrLockNotAcquired               = errors.New("Cloudflare reconcile lock was not acquired")
	ErrCloudflareDesiredStateChanged = errors.New("Cloudflare Console-origin desired state changed during provider reconciliation")
)
