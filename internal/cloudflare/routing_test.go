package cloudflare

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
)

type routingFakeStore struct {
	connection domain.CloudflareConnection
	retiring   []domain.CloudflareRetiringWildcardDNS
	locked     bool
	lastError  string
}

func (s *routingFakeStore) TryCloudflareReconcileLock(context.Context) (func() error, bool, error) {
	if s.locked {
		return nil, false, nil
	}
	s.locked = true
	return func() error { s.locked = false; return nil }, true, nil
}

func (s *routingFakeStore) CloudflareReconcileSnapshot(context.Context) (domain.CloudflareConnection, error) {
	return cloneCloudflareConnection(s.connection), nil
}

func (s *routingFakeStore) CompleteCloudflareReconcile(_ context.Context, update domain.CloudflareRoutingUpdate) (bool, error) {
	if !sameOptionalRoutingDomain(s.connection.WorkloadBaseDomain, update.ExpectedWorkloadBaseDomain) {
		return false, nil
	}
	if s.connection.WildcardRecordID != "" && (s.connection.WildcardRecordID != update.WildcardRecordID || s.connection.WildcardHostname != update.WildcardHostname || s.connection.WorkloadZoneID != update.WorkloadZoneID) {
		s.retiring = append(s.retiring, domain.CloudflareRetiringWildcardDNS{
			RecordID: s.connection.WildcardRecordID, ZoneID: s.connection.WorkloadZoneID,
			Hostname: s.connection.WildcardHostname, Target: s.connection.TunnelID + ".cfargotunnel.com",
		})
	}
	s.connection.WorkloadZoneID = update.WorkloadZoneID
	s.connection.WorkloadZoneName = update.WorkloadZoneName
	s.connection.WildcardHostname = update.WildcardHostname
	s.connection.WildcardRecordID = update.WildcardRecordID
	s.connection.EdgeTLSStatus = update.EdgeTLSStatus
	s.connection.EdgeTLSError = update.EdgeTLSError
	s.connection.Status = cloudflareRoutingStatus(update.EdgeTLSStatus)
	s.connection.LastError = ""
	now := time.Now().UTC()
	s.connection.LastReconciledAt = &now
	return true, nil
}

func (s *routingFakeStore) ListRetiringCloudflareWildcardDNS(context.Context) ([]domain.CloudflareRetiringWildcardDNS, error) {
	return append([]domain.CloudflareRetiringWildcardDNS(nil), s.retiring...), nil
}

func (s *routingFakeStore) CompleteCloudflareWildcardRetirement(_ context.Context, recordID string) error {
	remaining := s.retiring[:0]
	for _, item := range s.retiring {
		if item.RecordID != recordID {
			remaining = append(remaining, item)
		}
	}
	s.retiring = remaining
	return nil
}

func (s *routingFakeStore) RecordCloudflareReconcileFailure(_ context.Context, message string) error {
	s.connection.Status = "error"
	s.connection.LastError = message
	s.lastError = message
	return nil
}

type routingFakeClient struct {
	accounts            []Account
	zones               []Zone
	certificatePacks    []CertificatePack
	certificatePacksErr error
	totalTLS            TotalTLSSettings
	totalTLSErr         error
	defaultWildcard     string
	certificateReads    int
	totalTLSReads       int
	ingress             []IngressRule
	records             map[string]map[string]DNSRecord
	listAccountsErr     error
	configureErr        error
	createErr           error
	updateErr           error
	deleteErr           error
	createCount         int
	updateCount         int
	configureCount      int
	deleteCount         int
	events              []string
}

func newRoutingFakeClient() *routingFakeClient {
	return &routingFakeClient{
		accounts: []Account{{ID: "account-a", Name: "Test"}},
		zones: []Zone{
			{ID: "console-zone", Name: "example.com", Type: "full"},
			{ID: "apps-zone", Name: "apps.example.com", Type: "full"},
			{ID: "net-zone", Name: "example.net", Type: "full"},
			{ID: "nested-zone", Name: "sub.example.net", Type: "full"},
		},
		ingress: []IngressRule{{Hostname: "cloud.example.com", Service: "http://proxy:80"}, {Service: "http_status:404"}},
		records: make(map[string]map[string]DNSRecord),
	}
}

func (c *routingFakeClient) ListAccounts(context.Context) ([]Account, error) {
	if c.listAccountsErr != nil {
		return nil, c.listAccountsErr
	}
	return append([]Account(nil), c.accounts...), nil
}
func (c *routingFakeClient) ListZones(context.Context, string) ([]Zone, error) {
	return append([]Zone(nil), c.zones...), nil
}
func (c *routingFakeClient) ListCertificatePacks(context.Context, string) ([]CertificatePack, error) {
	c.certificateReads++
	if c.certificatePacksErr != nil {
		return nil, c.certificatePacksErr
	}
	if c.certificatePacks != nil {
		return append([]CertificatePack(nil), c.certificatePacks...), nil
	}
	if c.defaultWildcard == "" {
		return nil, nil
	}
	return []CertificatePack{{
		ID: "pack-active", Type: "advanced", Status: "active",
		Hosts:        []string{"apps.example.com", c.defaultWildcard},
		Certificates: []Certificate{{ID: "cert-active", Status: "active", Hosts: []string{c.defaultWildcard}}},
	}}, nil
}
func (c *routingFakeClient) TotalTLSSettings(context.Context, string) (TotalTLSSettings, error) {
	c.totalTLSReads++
	if c.totalTLSErr != nil {
		return TotalTLSSettings{}, c.totalTLSErr
	}
	return c.totalTLS, nil
}
func (*routingFakeClient) ListTunnels(context.Context, string, string) ([]Tunnel, error) {
	return nil, nil
}
func (*routingFakeClient) CreateTunnel(context.Context, string, string) (Tunnel, error) {
	return Tunnel{}, errors.New("unexpected tunnel creation")
}
func (c *routingFakeClient) ConfigureTunnel(_ context.Context, _, _ string, rules []IngressRule) error {
	c.configureCount++
	c.events = append(c.events, "configure_tunnel")
	if c.configureErr != nil {
		return c.configureErr
	}
	c.ingress = append([]IngressRule(nil), rules...)
	return nil
}
func (c *routingFakeClient) TunnelConfiguration(context.Context, string, string) ([]IngressRule, error) {
	return append([]IngressRule(nil), c.ingress...), nil
}
func (c *routingFakeClient) ListDNSRecords(_ context.Context, zoneID, name string) ([]DNSRecord, error) {
	var result []DNSRecord
	for _, record := range c.records[zoneID] {
		if canonicalDNSName(record.Name) == canonicalDNSName(name) {
			result = append(result, record)
		}
	}
	return result, nil
}
func (c *routingFakeClient) GetDNSRecord(_ context.Context, zoneID, recordID string) (DNSRecord, error) {
	c.events = append(c.events, "get_dns")
	record, ok := c.records[zoneID][recordID]
	if !ok {
		return DNSRecord{}, ErrResourceNotFound
	}
	return record, nil
}
func (c *routingFakeClient) CreateDNSRecord(_ context.Context, zoneID string, record DNSRecord) (DNSRecord, error) {
	c.createCount++
	c.events = append(c.events, "create_dns")
	if c.createErr != nil {
		return DNSRecord{}, c.createErr
	}
	if c.records[zoneID] == nil {
		c.records[zoneID] = make(map[string]DNSRecord)
	}
	record.ID = fmt.Sprintf("wildcard-%d", c.createCount)
	c.records[zoneID][record.ID] = record
	return record, nil
}
func (c *routingFakeClient) UpdateDNSRecord(_ context.Context, zoneID, recordID string, record DNSRecord) (DNSRecord, error) {
	c.updateCount++
	c.events = append(c.events, "update_dns")
	if c.updateErr != nil {
		return DNSRecord{}, c.updateErr
	}
	if c.records[zoneID] == nil {
		return DNSRecord{}, ErrResourceNotFound
	}
	if _, ok := c.records[zoneID][recordID]; !ok {
		return DNSRecord{}, ErrResourceNotFound
	}
	record.ID = recordID
	c.records[zoneID][recordID] = record
	return record, nil
}
func (c *routingFakeClient) DeleteDNSRecord(_ context.Context, zoneID, recordID string) error {
	c.deleteCount++
	c.events = append(c.events, "delete_dns")
	if c.deleteErr != nil {
		return c.deleteErr
	}
	if c.records[zoneID] != nil {
		delete(c.records[zoneID], recordID)
	}
	return nil
}
func (*routingFakeClient) TunnelStatus(_ context.Context, _, tunnelID string) (TunnelStatus, error) {
	return TunnelStatus{ID: tunnelID, Name: "stealth-prod", Status: "healthy"}, nil
}
func (*routingFakeClient) TunnelToken(context.Context, string, string) (string, error) {
	return "unused-tunnel-token", nil
}

func newRoutingTestReconciler(t *testing.T, store *routingFakeStore, client *routingFakeClient) *Reconciler {
	t.Helper()
	reconciler, err := NewReconciler(store, func(token string) (Client, error) {
		if token != "cf-secret-token" {
			t.Errorf("client token = %q, want stored credential", token)
		}
		client.defaultWildcard = ""
		if store.connection.WorkloadBaseDomain != nil {
			client.defaultWildcard = "*." + *store.connection.WorkloadBaseDomain
		}
		return client, nil
	}, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return reconciler
}

func newRoutingTestStore(workloadDomain *string) *routingFakeStore {
	return &routingFakeStore{connection: domain.CloudflareConnection{
		AccountID: "account-a", ConsoleZoneID: "console-zone", ConsoleHostname: "cloud.example.com",
		TunnelID: "tunnel-a", TunnelName: "stealth-prod", ConsoleRecordID: "console-record",
		APIToken: "cf-secret-token", Status: "pending", WorkloadBaseDomain: workloadDomain,
	}}
}

func TestRoutingReconcilerCreatesWildcardAndOrdersTunnelIngress(t *testing.T) {
	workload := "apps.example.com"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	client.zones = []Zone{{ID: "console-zone", Name: "example.com", Type: "full"}}
	reconciler := newRoutingTestReconciler(t, store, client)
	result, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantIngress := []IngressRule{
		{Hostname: "cloud.example.com", Service: "http://proxy:80"},
		{Hostname: "*.apps.example.com", Service: "http://traefik:8080"},
		{Service: "http_status:404"},
	}
	if !sameIngress(client.ingress, wantIngress) {
		t.Fatalf("tunnel ingress = %#v, want %#v", client.ingress, wantIngress)
	}
	if got := client.records["console-zone"][store.connection.WildcardRecordID]; got.Type != "CNAME" || got.Name != "*.apps.example.com" || got.Content != "tunnel-a.cfargotunnel.com" || !got.Proxied || got.TTL != 1 {
		t.Fatalf("wildcard record = %#v", got)
	}
	if store.connection.Status != "ready" || store.connection.WorkloadZoneID != "console-zone" || store.connection.WildcardHostname != "*.apps.example.com" || !result.Changed {
		t.Fatalf("stored routing state = %#v result=%#v", store.connection, result)
	}
}

func TestRoutingReconcilerNoOpsWhenCloudflareIsUnconfigured(t *testing.T) {
	workload := "apps.example.com"
	for _, workloadDomain := range []*string{&workload, nil} {
		store := &routingFakeStore{connection: domain.CloudflareConnection{WorkloadBaseDomain: workloadDomain}}
		clientFactoryCalls := 0
		reconciler, err := NewReconciler(store, func(string) (Client, error) {
			clientFactoryCalls++
			return nil, errors.New("provider client must not be created for an unused provider")
		}, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		result, err := reconciler.Reconcile(context.Background())
		if err != nil || result.Status != "unconfigured" || result.EdgeTLSStatus != EdgeTLSNotApplicable {
			t.Fatalf("unconfigured Cloudflare reconcile = %#v, %v", result, err)
		}
		if clientFactoryCalls != 0 || store.connection.Status != "" || store.lastError != "" {
			t.Fatalf("unconfigured provider was acted on: client_calls=%d store=%#v", clientFactoryCalls, store)
		}
	}
}

func TestRoutingReconcilerReportsIdentityWithoutCredentialAsReconnectRequired(t *testing.T) {
	store := &routingFakeStore{connection: domain.CloudflareConnection{
		AccountID: "account-a", ConsoleZoneID: "console-zone", ConsoleHostname: "cloud.example.com",
		TunnelID: "tunnel-a", TunnelName: "stealth-prod", ConsoleRecordID: "console-record",
	}}
	clientFactoryCalls := 0
	reconciler, err := NewReconciler(store, func(string) (Client, error) {
		clientFactoryCalls++
		return nil, errors.New("unreachable")
	}, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconciler.Reconcile(context.Background())
	if err == nil || result.Status != "error" || store.connection.Status != "error" || !strings.Contains(store.lastError, "reconnect") {
		t.Fatalf("incomplete Cloudflare identity = %#v, store=%#v, err=%v", result, store, err)
	}
	if clientFactoryCalls != 0 {
		t.Fatalf("client factory called without credential %d times", clientFactoryCalls)
	}
}

func TestTLSInspectionFailureKeepsWildcardAndTunnelResources(t *testing.T) {
	workload := "apps.example.com"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	client.certificatePacksErr = errors.New("provider echoed cf-secret-token")
	reconciler := newRoutingTestReconciler(t, store, client)
	result, err := reconciler.Reconcile(context.Background())
	if err == nil || result.Status != "error" || result.EdgeTLSStatus != EdgeTLSError || store.connection.Status != "error" {
		t.Fatalf("TLS API failure result=%#v state=%#v error=%v", result, store.connection, err)
	}
	if strings.Contains(err.Error(), "cf-secret-token") || strings.Contains(store.connection.EdgeTLSError, "cf-secret-token") {
		t.Fatalf("TLS inspection leaked token: err=%q tls_error=%q", err, store.connection.EdgeTLSError)
	}
	if store.connection.WildcardRecordID == "" || client.records["apps-zone"][store.connection.WildcardRecordID].Name != "*.apps.example.com" {
		t.Fatalf("TLS failure removed or failed to save wildcard DNS: %#v", store.connection)
	}
	if !sameIngress(client.ingress, desiredTunnelIngress("cloud.example.com", "*.apps.example.com")) || client.deleteCount != 0 {
		t.Fatalf("TLS failure changed working tunnel or cleaned provider state: ingress=%#v deletes=%d", client.ingress, client.deleteCount)
	}
}

func TestTLSCertificatePermissionUnauthorizedPreservesRouting(t *testing.T) {
	workload := "apps.example.com"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	client.certificatePacksErr = fmt.Errorf("%w: cf-secret-token echoed", ErrUnauthorized)
	reconciler := newRoutingTestReconciler(t, store, client)
	result, err := reconciler.Reconcile(context.Background())
	if !errors.Is(err, ErrUnauthorized) || result.Status != "error" || result.EdgeTLSStatus != EdgeTLSError {
		t.Fatalf("unauthorized certificate inspection result=%#v err=%v", result, err)
	}
	if strings.Contains(err.Error(), "cf-secret-token") || store.connection.EdgeTLSError == "" {
		t.Fatalf("certificate permission failure leaked credential or omitted status: err=%q tls_error=%q", err, store.connection.EdgeTLSError)
	}
	if store.connection.WildcardRecordID == "" || client.deleteCount != 0 || !sameIngress(client.ingress, desiredTunnelIngress("cloud.example.com", "*.apps.example.com")) {
		t.Fatalf("unauthorized TLS read destroyed routing: state=%#v ingress=%#v", store.connection, client.ingress)
	}
}

func TestRoutingRequiresActiveExactWorkloadWildcardAndPreservesDNSAndTunnel(t *testing.T) {
	workload := "apps.example.com"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	client.zones = []Zone{{ID: "console-zone", Name: "example.com", Type: "full"}}
	client.certificatePacks = []CertificatePack{{
		ID: "universal-parent", Type: "universal", Status: "active", Hosts: []string{"example.com", "*.example.com"},
		Certificates: []Certificate{{ID: "cert-parent", Status: "active", Hosts: []string{"*.example.com"}}},
	}}
	reconciler := newRoutingTestReconciler(t, store, client)
	result, err := reconciler.Reconcile(context.Background())
	if err != nil || result.Status != "error" || result.EdgeTLSStatus != EdgeTLSActionRequired {
		t.Fatalf("deeper wildcard TLS result=%#v err=%v", result, err)
	}
	if !strings.Contains(result.EdgeTLSReason, "*.apps.example.com") || store.connection.LastError != "" {
		t.Fatalf("missing wildcard coverage reason/status = %q/%q", result.EdgeTLSReason, store.connection.LastError)
	}
	if store.connection.WildcardRecordID == "" || client.records["console-zone"][store.connection.WildcardRecordID].Name != "*.apps.example.com" {
		t.Fatalf("DNS wildcard was not retained: state=%#v records=%#v", store.connection, client.records)
	}
	wantIngress := desiredTunnelIngress("cloud.example.com", "*.apps.example.com")
	if !sameIngress(client.ingress, wantIngress) || client.deleteCount != 0 {
		t.Fatalf("TLS readiness failure changed tunnel routes: ingress=%#v deletes=%d", client.ingress, client.deleteCount)
	}
}

func TestRoutingReconcilerDiscoversDifferentWorkloadZoneAndLongestSuffix(t *testing.T) {
	workload := "apps.sub.example.net"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	client.zones = []Zone{{ID: "console-zone", Name: "example.com"}, {ID: "net-zone", Name: "example.net"}, {ID: "nested-zone", Name: "sub.example.net"}}
	reconciler := newRoutingTestReconciler(t, store, client)
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.connection.WorkloadZoneID != "nested-zone" || store.connection.WorkloadZoneName != "sub.example.net" {
		t.Fatalf("selected workload zone = %q %q", store.connection.WorkloadZoneID, store.connection.WorkloadZoneName)
	}
	if _, ok := client.records["nested-zone"][store.connection.WildcardRecordID]; !ok {
		t.Fatalf("wildcard was not created in the discovered workload zone: %#v", client.records)
	}
	if client.ingress[0].Service != "http://proxy:80" || client.ingress[1].Service != "http://traefik:8080" {
		t.Fatalf("Console/workload ingress origins = %#v", client.ingress)
	}
}

func TestRoutingReconcilerAdoptsAndRepairsOwnedWildcardFlags(t *testing.T) {
	workload := "apps.example.net"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	store.connection.WorkloadZoneID = "net-zone"
	store.connection.WorkloadZoneName = "example.net"
	store.connection.WildcardHostname = "*.apps.example.net"
	store.connection.WildcardRecordID = "record-existing"
	client.records["net-zone"] = map[string]DNSRecord{
		"record-existing": {ID: "record-existing", Type: "CNAME", Name: "*.apps.example.net", Content: "tunnel-a.cfargotunnel.com", Proxied: false, TTL: 300},
	}
	client.ingress = desiredTunnelIngress("cloud.example.com", "*.apps.example.net")
	reconciler := newRoutingTestReconciler(t, store, client)
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated := client.records["net-zone"]["record-existing"]
	if !updated.Proxied || updated.TTL != 1 || client.createCount != 0 || client.updateCount != 1 {
		t.Fatalf("adopted record = %#v creates=%d updates=%d", updated, client.createCount, client.updateCount)
	}
}

func TestRoutingReconcilerRefusesIncompatibleDNSWithoutTunnelMutation(t *testing.T) {
	workload := "apps.example.com"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	client.records["apps-zone"] = map[string]DNSRecord{
		"operator-a": {ID: "operator-a", Type: "A", Name: "*.apps.example.com", Content: "192.0.2.1"},
	}
	reconciler := newRoutingTestReconciler(t, store, client)
	_, err := reconciler.Reconcile(context.Background())
	if !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("Reconcile() error = %v, want DNS conflict", err)
	}
	if store.connection.Status != "error" || client.configureCount != 0 || client.createCount != 0 {
		t.Fatalf("conflict mutated routing: state=%#v client=%#v", store.connection, client)
	}
	if client.records["apps-zone"]["operator-a"].Content != "192.0.2.1" {
		t.Fatal("incompatible operator record was modified")
	}
}

func TestRoutingReconcilerRefusesWildcardWithAnotherCNAMETarget(t *testing.T) {
	workload := "apps.example.com"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	client.records["apps-zone"] = map[string]DNSRecord{
		"operator-cname": {ID: "operator-cname", Type: "CNAME", Name: "*.apps.example.com", Content: "operator.example.net", Proxied: true, TTL: 1},
	}
	reconciler := newRoutingTestReconciler(t, store, client)
	if _, err := reconciler.Reconcile(context.Background()); !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("Reconcile() error = %v, want a conflicting CNAME target", err)
	}
	if client.configureCount != 0 || client.createCount != 0 || client.updateCount != 0 || client.deleteCount != 0 {
		t.Fatalf("CNAME target conflict caused provider mutation: %#v", client)
	}
	if client.records["apps-zone"]["operator-cname"].Content != "operator.example.net" {
		t.Fatal("operator-owned CNAME was modified")
	}
}

func TestRoutingReconcilerRetriesIdempotently(t *testing.T) {
	workload := "apps.example.com"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	reconciler := newRoutingTestReconciler(t, store, client)
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	creates, configures := client.createCount, client.configureCount
	result, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || client.createCount != creates || client.configureCount != configures {
		t.Fatalf("retry was not a no-op: result=%#v creates=%d configures=%d", result, client.createCount, client.configureCount)
	}
}

func TestRoutingReconcilerChangesDomainBeforeSafeDNSCleanup(t *testing.T) {
	store, client := newRoutingTestStore(nil), newRoutingFakeClient()
	store.connection.WorkloadZoneID = "net-zone"
	store.connection.WorkloadZoneName = "example.net"
	store.connection.WildcardHostname = "*.old.example.net"
	store.connection.WildcardRecordID = "old-record"
	client.records["net-zone"] = map[string]DNSRecord{
		"old-record": {ID: "old-record", Type: "CNAME", Name: "*.old.example.net", Content: "tunnel-a.cfargotunnel.com", Proxied: true, TTL: 1},
	}
	client.ingress = desiredTunnelIngress("cloud.example.com", "*.old.example.net")
	reconciler := newRoutingTestReconciler(t, store, client)
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []IngressRule{{Hostname: "cloud.example.com", Service: "http://proxy:80"}, {Service: "http_status:404"}}
	if !sameIngress(client.ingress, want) || client.deleteCount != 1 || len(client.records["net-zone"]) != 0 || store.connection.WildcardRecordID != "" {
		t.Fatalf("clear result ingress=%#v deletes=%d records=%#v state=%#v", client.ingress, client.deleteCount, client.records, store.connection)
	}
}

func TestRoutingReconcilerEstablishesNewDomainBeforeRemovingOldWildcard(t *testing.T) {
	newDomain := "apps.example.com"
	store, client := newRoutingTestStore(&newDomain), newRoutingFakeClient()
	store.connection.WorkloadZoneID = "net-zone"
	store.connection.WorkloadZoneName = "example.net"
	store.connection.WildcardHostname = "*.old.example.net"
	store.connection.WildcardRecordID = "old-record"
	client.records["net-zone"] = map[string]DNSRecord{
		"old-record": {ID: "old-record", Type: "CNAME", Name: "*.old.example.net", Content: "tunnel-a.cfargotunnel.com", Proxied: true, TTL: 1},
	}
	client.ingress = desiredTunnelIngress("cloud.example.com", "*.old.example.net")
	reconciler := newRoutingTestReconciler(t, store, client)
	if _, err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	createIndex, routeIndex, deleteIndex := -1, -1, -1
	for index, event := range client.events {
		switch event {
		case "create_dns":
			createIndex = index
		case "configure_tunnel":
			routeIndex = index
		case "delete_dns":
			deleteIndex = index
		}
	}
	if createIndex < 0 || routeIndex <= createIndex || deleteIndex <= routeIndex {
		t.Fatalf("provider operation order = %#v, want create new DNS < update tunnel < delete old DNS", client.events)
	}
	if _, exists := client.records["net-zone"]["old-record"]; exists {
		t.Fatal("previous owned wildcard record remains after safe replacement")
	}
	if got := client.records["apps-zone"][store.connection.WildcardRecordID]; got.Name != "*.apps.example.com" || !got.Proxied || got.TTL != 1 {
		t.Fatalf("new wildcard record = %#v", got)
	}
}

func TestRoutingReconcilerRefusesDeletionAfterOwnershipDrift(t *testing.T) {
	store, client := newRoutingTestStore(nil), newRoutingFakeClient()
	store.connection.WorkloadZoneID = "net-zone"
	store.connection.WorkloadZoneName = "example.net"
	store.connection.WildcardHostname = "*.old.example.net"
	store.connection.WildcardRecordID = "old-record"
	client.records["net-zone"] = map[string]DNSRecord{
		"old-record": {ID: "old-record", Type: "CNAME", Name: "*.old.example.net", Content: "operator.example.net", Proxied: true, TTL: 1},
	}
	client.ingress = desiredTunnelIngress("cloud.example.com", "*.old.example.net")
	reconciler := newRoutingTestReconciler(t, store, client)
	_, err := reconciler.Reconcile(context.Background())
	if !errors.Is(err, ErrRoutingConflict) {
		t.Fatalf("Reconcile() error = %v, want ownership drift", err)
	}
	if client.deleteCount != 0 || client.records["net-zone"]["old-record"].Content != "operator.example.net" || store.connection.Status != "error" {
		t.Fatalf("ownership drift cleanup state=%#v records=%#v deletes=%d", store.connection, client.records, client.deleteCount)
	}
}

func TestRoutingReconcilerPreservesLastKnownGoodRecordOnIngressFailure(t *testing.T) {
	newDomain := "deploy.example.net"
	store, client := newRoutingTestStore(&newDomain), newRoutingFakeClient()
	store.connection.WorkloadZoneID = "console-zone"
	store.connection.WorkloadZoneName = "example.com"
	store.connection.WildcardHostname = "*.apps.example.com"
	store.connection.WildcardRecordID = "old-record"
	client.records["console-zone"] = map[string]DNSRecord{
		"old-record": {ID: "old-record", Type: "CNAME", Name: "*.apps.example.com", Content: "tunnel-a.cfargotunnel.com", Proxied: true, TTL: 1},
	}
	client.configureErr = errors.New("provider timeout")
	reconciler := newRoutingTestReconciler(t, store, client)
	if _, err := reconciler.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile() succeeded despite tunnel configuration failure")
	}
	if _, ok := client.records["console-zone"]["old-record"]; !ok || client.deleteCount != 0 || store.connection.WildcardRecordID != "old-record" {
		t.Fatalf("old route was destroyed: state=%#v records=%#v", store.connection, client.records)
	}
}

func TestRoutingReconcilerLeavesOldProviderStateWhenNewZoneIsUnavailable(t *testing.T) {
	newDomain := "apps.example.net"
	store, client := newRoutingTestStore(&newDomain), newRoutingFakeClient()
	store.connection.WorkloadZoneID = "old-zone"
	store.connection.WorkloadZoneName = "old.example.net"
	store.connection.WildcardHostname = "*.old.example.net"
	store.connection.WildcardRecordID = "old-record"
	client.zones = []Zone{{ID: "console-zone", Name: "example.com"}}
	client.records["old-zone"] = map[string]DNSRecord{
		"old-record": {ID: "old-record", Type: "CNAME", Name: "*.old.example.net", Content: "tunnel-a.cfargotunnel.com", Proxied: true, TTL: 1},
	}
	client.ingress = desiredTunnelIngress("cloud.example.com", "*.old.example.net")
	reconciler := newRoutingTestReconciler(t, store, client)
	_, err := reconciler.Reconcile(context.Background())
	if err == nil || store.connection.Status != "error" {
		t.Fatalf("unavailable target zone status=%#v err=%v", store.connection, err)
	}
	if _, ok := client.records["old-zone"]["old-record"]; !ok || client.configureCount != 0 || client.deleteCount != 0 {
		t.Fatalf("inaccessible new zone changed last-known provider state: state=%#v records=%#v", store.connection, client.records)
	}
}

func TestRoutingReconcilerUnauthorizedTokenIsSanitizedAndDoesNotMutateProvider(t *testing.T) {
	workload := "apps.example.com"
	store, client := newRoutingTestStore(&workload), newRoutingFakeClient()
	client.listAccountsErr = fmt.Errorf("%w: token cf-secret-token echoed by provider", ErrUnauthorized)
	var logs bytes.Buffer
	reconciler, err := NewReconciler(store, func(token string) (Client, error) {
		if token != "cf-secret-token" {
			t.Errorf("client token = %q, want stored credential", token)
		}
		return client, nil
	}, time.Minute, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reconciler.Reconcile(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Reconcile() error = %v, want unauthorized", err)
	}
	if strings.Contains(store.lastError, "cf-secret-token") || strings.Contains(err.Error(), "cf-secret-token") || strings.Contains(logs.String(), "cf-secret-token") {
		t.Fatalf("unauthorized token leaked: status=%q error=%q logs=%q", store.lastError, err, logs.String())
	}
	if store.connection.Status != "error" || client.configureCount != 0 || client.createCount != 0 {
		t.Fatalf("unauthorized token caused provider mutation: state=%#v client=%#v", store.connection, client)
	}
}

func TestLongestContainingZoneUsesLongestCanonicalSuffix(t *testing.T) {
	zone, err := longestContainingZone([]Zone{{ID: "broad", Name: "example.net."}, {ID: "specific", Name: "sub.example.net"}}, "apps.sub.example.net")
	if err != nil || zone.ID != "specific" || zone.Name != "sub.example.net" {
		t.Fatalf("longestContainingZone() = %#v, %v", zone, err)
	}
}

func cloneCloudflareConnection(value domain.CloudflareConnection) domain.CloudflareConnection {
	value.WorkloadBaseDomain = cloneString(value.WorkloadBaseDomain)
	if value.LastReconciledAt != nil {
		when := *value.LastReconciledAt
		value.LastReconciledAt = &when
	}
	if value.ConfiguredAt != nil {
		when := *value.ConfiguredAt
		value.ConfiguredAt = &when
	}
	return value
}

func sameOptionalRoutingDomain(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
