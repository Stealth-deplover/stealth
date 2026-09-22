package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	LockAcquired bool
	Changed      bool
	Status       string
	Hostname     string
	Zone         string
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
	result, err := r.Reconcile(ctx)
	switch {
	case errors.Is(err, ErrLockNotAcquired):
		r.logger.Debug("cloudflare reconcile skipped; another worker holds the lock")
	case errors.Is(err, ErrUnauthorized):
		r.logger.Error("cloudflare token unauthorized", "error", safeReconcileError(err, ""))
	case errors.Is(err, ErrRoutingConflict):
		r.logger.Error("cloudflare reconcile conflict", "error", safeReconcileError(err, ""))
	case err != nil:
		r.logger.Error("cloudflare reconcile failed", "error", safeReconcileError(err, ""))
	case result.Changed:
		r.logger.Info("cloudflare reconcile success", "workload_hostname", result.Hostname, "zone", result.Zone)
	default:
		r.logger.Debug("cloudflare already converged", "workload_hostname", result.Hostname, "zone", result.Zone)
	}
}

func (r *Reconciler) Reconcile(ctx context.Context) (result ReconcileResult, err error) {
	if r == nil || r.store == nil || r.client == nil {
		return result, errors.New("Cloudflare reconciler is not configured")
	}
	r.logger.Info("cloudflare reconcile start")
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
	if !connectionConfigured(connection) {
		if connection.Status == "unconfigured" && connection.WorkloadBaseDomain == nil {
			return result, nil
		}
		err = errors.New("Cloudflare connection is unavailable; an Instance Owner must reconnect the scoped token")
		return result, r.recordFailure(ctx, connection.APIToken, err)
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

	desiredIngress := desiredTunnelIngress(connection.ConsoleHostname, wildcardHostname)
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
		r.logger.Info("cloudflare tunnel ingress updated", "console_origin", "http://proxy:80", "workload_origin", workloadOrigin(wildcardHostname))
	}

	update := domain.CloudflareRoutingUpdate{ExpectedWorkloadBaseDomain: cloneString(connection.WorkloadBaseDomain)}
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
	result.Status = "ready"
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
		return domain.CloudflareConnection{}, errors.New("existing tunnel does not contain the Console proxy route and 404 catch-all")
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
	accounts, err := client.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	if !containsAccount(accounts, accountID) {
		return nil, errors.New("Cloudflare token cannot access the configured account")
	}
	zones, err := client.ListZones(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !zoneContainsHostname(zones, consoleZoneID, consoleHostname) {
		return nil, errors.New("Cloudflare token cannot read the configured Console zone")
	}
	status, err := client.TunnelStatus(ctx, accountID, tunnelID)
	if err != nil {
		return nil, err
	}
	if status.ID != "" && status.ID != tunnelID {
		return nil, errors.New("Cloudflare returned a different tunnel identity")
	}
	if tunnelName != "" && status.Name != "" && status.Name != tunnelName {
		return nil, errors.New("configured Cloudflare tunnel identity changed")
	}
	return zones, nil
}

func connectionConfigured(connection domain.CloudflareConnection) bool {
	return strings.TrimSpace(connection.APIToken) != "" && strings.TrimSpace(connection.AccountID) != "" && strings.TrimSpace(connection.ConsoleZoneID) != "" && strings.TrimSpace(connection.ConsoleHostname) != "" && strings.TrimSpace(connection.TunnelID) != "" && strings.TrimSpace(connection.TunnelName) != "" && strings.TrimSpace(connection.ConsoleRecordID) != ""
}

func desiredTunnelIngress(consoleHostname, wildcardHostname string) []IngressRule {
	result := []IngressRule{{Hostname: consoleHostname, Service: "http://proxy:80"}}
	if wildcardHostname != "" {
		result = append(result, IngressRule{Hostname: wildcardHostname, Service: "http://traefik:8080"})
	}
	return append(result, IngressRule{Service: "http_status:404"})
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
		if canonicalDNSName(rule.Hostname) == canonicalDNSName(consoleHostname) && rule.Service == "http://proxy:80" {
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

var ErrLockNotAcquired = errors.New("Cloudflare reconcile lock was not acquired")
