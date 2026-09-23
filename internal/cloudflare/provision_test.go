package cloudflare

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

type provisioningClient struct {
	zones            []Zone
	tunnels          []Tunnel
	records          []DNSRecord
	createTunnelCall int
	createRecordCall int
}

func (c *provisioningClient) ListAccounts(context.Context) ([]Account, error) { return nil, nil }

func (c *provisioningClient) ListZones(context.Context, string) ([]Zone, error) {
	return append([]Zone(nil), c.zones...), nil
}

func (*provisioningClient) ListCertificatePacks(context.Context, string) ([]CertificatePack, error) {
	return nil, nil
}

func (*provisioningClient) TotalTLSSettings(context.Context, string) (TotalTLSSettings, error) {
	return TotalTLSSettings{}, nil
}

func (c *provisioningClient) ListTunnels(_ context.Context, _, name string) ([]Tunnel, error) {
	result := make([]Tunnel, 0)
	for _, tunnel := range c.tunnels {
		if tunnel.Name == name {
			result = append(result, tunnel)
		}
	}
	return result, nil
}

func (c *provisioningClient) CreateTunnel(_ context.Context, _, name string) (Tunnel, error) {
	c.createTunnelCall++
	tunnel := Tunnel{ID: "tunnel-1", Name: name}
	c.tunnels = append(c.tunnels, tunnel)
	return tunnel, nil
}

func (*provisioningClient) ConfigureTunnel(context.Context, string, string, []IngressRule) error {
	return nil
}

func (*provisioningClient) TunnelConfiguration(context.Context, string, string) ([]IngressRule, error) {
	return []IngressRule{{Hostname: "app.example.test", Service: "http://proxy:80"}, {Service: "http_status:404"}}, nil
}

func (c *provisioningClient) ListDNSRecords(_ context.Context, _, name string) ([]DNSRecord, error) {
	result := make([]DNSRecord, 0)
	for _, record := range c.records {
		if record.Name == name {
			result = append(result, record)
		}
	}
	return result, nil
}

func (c *provisioningClient) GetDNSRecord(_ context.Context, _, recordID string) (DNSRecord, error) {
	for _, record := range c.records {
		if record.ID == recordID {
			return record, nil
		}
	}
	return DNSRecord{}, ErrResourceNotFound
}

func (c *provisioningClient) CreateDNSRecord(_ context.Context, _ string, record DNSRecord) (DNSRecord, error) {
	c.createRecordCall++
	record.ID = "record-1"
	c.records = append(c.records, record)
	return record, nil
}

func (c *provisioningClient) UpdateDNSRecord(_ context.Context, _, recordID string, record DNSRecord) (DNSRecord, error) {
	for index := range c.records {
		if c.records[index].ID == recordID {
			record.ID = recordID
			c.records[index] = record
			return record, nil
		}
	}
	return DNSRecord{}, errors.New("record not found")
}

func (*provisioningClient) DeleteDNSRecord(context.Context, string, string) error { return nil }

func (*provisioningClient) TunnelStatus(context.Context, string, string) (TunnelStatus, error) {
	return TunnelStatus{Status: "healthy"}, nil
}

func (*provisioningClient) TunnelToken(context.Context, string, string) (string, error) {
	return "tunnel-token", nil
}

type failingUpdateStore struct {
	setupstate.Store
	failOn int
	count  int
}

func (s *failingUpdateStore) Update(ctx context.Context, mutate func(*setupstate.State) error) (setupstate.State, error) {
	s.count++
	if s.failOn > 0 && s.count == s.failOn {
		return setupstate.State{}, errors.New("simulated durable state failure")
	}
	return s.Store.Update(ctx, mutate)
}

func newProvisioningStore(t *testing.T) setupstate.Store {
	t.Helper()
	cipher, err := functionsecret.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := setupstate.NewFileStore(t.TempDir()+"/setup-state.enc", cipher)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), setupstate.NewState()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestProvisionReconcilesTunnelAfterTunnelIDStateFailure(t *testing.T) {
	baseStore := newProvisioningStore(t)
	store := &failingUpdateStore{Store: baseStore, failOn: 2}
	client := &provisioningClient{zones: []Zone{{ID: "zone-1", Name: "example.test"}}}
	request := ProvisionRequest{AccountID: "account-1", ZoneID: "zone-1", Hostname: "app.example.test", Name: "stealth-test"}

	if _, err := Provision(context.Background(), store, client, request); !errors.Is(err, ErrState) {
		t.Fatalf("first provisioning error = %v, want durable state error", err)
	}
	if client.createTunnelCall != 1 {
		t.Fatalf("created tunnels = %d, want 1", client.createTunnelCall)
	}

	store.failOn = 0
	state, err := Provision(context.Background(), store, client, request)
	if err != nil {
		t.Fatal(err)
	}
	if client.createTunnelCall != 1 {
		t.Fatalf("retry created duplicate tunnel; calls = %d", client.createTunnelCall)
	}
	if state.Cloudflare.Binding.TunnelID != "tunnel-1" || state.Cloudflare.Binding.RecordID != "record-1" {
		t.Fatalf("reconciled binding = %#v", state.Cloudflare.Binding)
	}
}

func TestProvisionReconcilesDNSAfterFinalStateFailure(t *testing.T) {
	baseStore := newProvisioningStore(t)
	store := &failingUpdateStore{Store: baseStore, failOn: 4}
	client := &provisioningClient{zones: []Zone{{ID: "zone-1", Name: "example.test"}}}
	request := ProvisionRequest{AccountID: "account-1", ZoneID: "zone-1", Hostname: "app.example.test", Name: "stealth-test"}

	if _, err := Provision(context.Background(), store, client, request); !errors.Is(err, ErrState) {
		t.Fatalf("first provisioning error = %v, want durable state error", err)
	}
	if client.createRecordCall != 1 {
		t.Fatalf("created DNS records = %d, want 1", client.createRecordCall)
	}

	store.failOn = 0
	if _, err := Provision(context.Background(), store, client, request); err != nil {
		t.Fatal(err)
	}
	if client.createRecordCall != 1 {
		t.Fatalf("retry created duplicate DNS record; calls = %d", client.createRecordCall)
	}
}

func TestProvisionRejectsPreexistingTunnelNameOnFirstAttempt(t *testing.T) {
	store := newProvisioningStore(t)
	client := &provisioningClient{
		zones:   []Zone{{ID: "zone-1", Name: "example.test"}},
		tunnels: []Tunnel{{ID: "existing", Name: "stealth-test"}},
	}
	_, err := Provision(context.Background(), store, client, ProvisionRequest{AccountID: "account-1", ZoneID: "zone-1", Hostname: "app.example.test", Name: "stealth-test"})
	if !errors.Is(err, ErrConflict) || client.createTunnelCall != 0 {
		t.Fatalf("preexisting tunnel result = %v, create calls = %d", err, client.createTunnelCall)
	}
}

func TestProvisionRejectsExistingNonCNAMEAtDashboardHostname(t *testing.T) {
	store := newProvisioningStore(t)
	client := &provisioningClient{
		zones:   []Zone{{ID: "zone-1", Name: "example.test"}},
		records: []DNSRecord{{ID: "record-a", Type: "A", Name: "app.example.test", Content: "192.0.2.10"}},
	}
	_, err := Provision(context.Background(), store, client, ProvisionRequest{AccountID: "account-1", ZoneID: "zone-1", Hostname: "app.example.test", Name: "stealth-test"})
	if !errors.Is(err, ErrConflict) || client.createRecordCall != 0 {
		t.Fatalf("existing non-CNAME result = %v, create calls = %d", err, client.createRecordCall)
	}
}

func TestProvisionRejectsCloudflareBindingSelectorChanges(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		zoneID    string
		hostname  string
		field     string
	}{
		{name: "account", accountID: "account-b", zoneID: "zone-a", hostname: "app.example.test", field: "Cloudflare account"},
		{name: "zone", accountID: "account-a", zoneID: "zone-b", hostname: "app.example.test", field: "Cloudflare zone"},
		{name: "hostname", accountID: "account-a", zoneID: "zone-a", hostname: "new.example.test", field: "production hostname"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newProvisioningStore(t)
			state, err := store.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			state.Draft.Hostname = "app.example.test"
			state.Cloudflare.Binding = setupstate.CloudflareBinding{
				AccountID: "account-a", ZoneID: "zone-a", Hostname: "app.example.test",
				TunnelName: "stealth-test", TunnelID: "tunnel-a", RecordID: "record-a",
			}
			if err := store.Save(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			client := &provisioningClient{zones: []Zone{{ID: "zone-a", Name: "example.test"}, {ID: "zone-b", Name: "example.test"}}}
			_, err = Provision(context.Background(), store, client, ProvisionRequest{
				AccountID: test.accountID,
				ZoneID:    test.zoneID,
				Hostname:  test.hostname,
				Name:      "stealth-test",
			})
			var bindingConflict *setupstate.CloudflareBindingConflict
			if !errors.Is(err, ErrConflict) || !errors.As(err, &bindingConflict) || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("binding change error = %v", err)
			}
			if client.createTunnelCall != 0 || client.createRecordCall != 0 {
				t.Fatalf("binding change caused provider writes: tunnel=%d record=%d", client.createTunnelCall, client.createRecordCall)
			}
		})
	}
}
