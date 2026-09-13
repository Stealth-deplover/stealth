package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIClientUsesNarrowCloudflareOperations(t *testing.T) {
	const token = "cf-api-token-never-returned"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/accounts":
			_, _ = io.WriteString(w, `{"success":true,"result":[{"id":"account-1","name":"Acme"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			if r.URL.Query().Get("account.id") != "account-1" {
				t.Errorf("zone account query = %q", r.URL.Query().Get("account.id"))
			}
			_, _ = io.WriteString(w, `{"success":true,"result":[{"id":"zone-1","name":"example.test","status":"active"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/account-1/cfd_tunnel":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("tunnel request: %v", err)
			}
			if body["config_src"] != "cloudflare" || body["name"] != "stealth-prod" {
				t.Errorf("tunnel request body = %#v", body)
			}
			_, _ = io.WriteString(w, `{"success":true,"result":{"id":"tunnel-1","name":"stealth-prod"}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/accounts/account-1/cfd_tunnel/tunnel-1/configurations":
			var body struct {
				Config struct {
					Ingress []IngressRule `json:"ingress"`
				} `json:"config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("configuration request: %v", err)
			}
			if len(body.Config.Ingress) != 2 || body.Config.Ingress[0].Service != "http://proxy:80" {
				t.Errorf("configuration body = %#v", body)
			}
			_, _ = io.WriteString(w, `{"success":true,"result":{}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-1/dns_records":
			var body DNSRecord
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("DNS request: %v", err)
			}
			if body.Type != "CNAME" || !body.Proxied || body.TTL != 1 {
				t.Errorf("DNS body = %#v", body)
			}
			_, _ = io.WriteString(w, `{"success":true,"result":{"id":"record-1","type":"CNAME","name":"app.example.test","content":"tunnel-1.cfargotunnel.com","proxied":true,"ttl":1}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/account-1/cfd_tunnel/tunnel-1":
			_, _ = io.WriteString(w, `{"success":true,"result":{"id":"tunnel-1","status":"healthy","connections":[{"id":"connection-1"},{"id":"connection-2"}]}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/account-1/cfd_tunnel/tunnel-1/token":
			_, _ = io.WriteString(w, `{"success":true,"result":"tunnel-token"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(token, server.URL+"/", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	accounts, err := client.ListAccounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].ID != "account-1" {
		t.Fatalf("ListAccounts() = %#v, %v", accounts, err)
	}
	zones, err := client.ListZones(ctx, "account-1")
	if err != nil || len(zones) != 1 || zones[0].ID != "zone-1" {
		t.Fatalf("ListZones() = %#v, %v", zones, err)
	}
	tunnel, err := client.CreateTunnel(ctx, "account-1", "stealth-prod")
	if err != nil || tunnel.ID != "tunnel-1" {
		t.Fatalf("CreateTunnel() = %#v, %v", tunnel, err)
	}
	if err := client.ConfigureTunnel(ctx, "account-1", "tunnel-1", []IngressRule{{Hostname: "app.example.test", Service: "http://proxy:80"}, {Service: "http_status:404"}}); err != nil {
		t.Fatal(err)
	}
	record, err := client.CreateDNSRecord(ctx, "zone-1", DNSRecord{Type: "cname", Name: "app.example.test", Content: "tunnel-1.cfargotunnel.com", Proxied: true})
	if err != nil || record.ID != "record-1" {
		t.Fatalf("CreateDNSRecord() = %#v, %v", record, err)
	}
	status, err := client.TunnelStatus(ctx, "account-1", "tunnel-1")
	if err != nil || status.Connections != 2 || !StatusIsHealthy(status) {
		t.Fatalf("TunnelStatus() = %#v, %v", status, err)
	}
	tunnelToken, err := client.TunnelToken(ctx, "account-1", "tunnel-1")
	if err != nil || tunnelToken != "tunnel-token" {
		t.Fatalf("TunnelToken() = %q, %v", tunnelToken, err)
	}
}

func TestAPIClientDoesNotExposeTokenInProviderErrors(t *testing.T) {
	const token = "secret-cloudflare-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":9109,"message":"invalid credentials"}]}`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client, err := NewClient(token, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListAccounts(context.Background())
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("provider error = %v", err)
	}
}

func TestTunnelStatusAcceptsIntegerConnectionCount(t *testing.T) {
	var status TunnelStatus
	if err := json.Unmarshal([]byte(`{"status":"degraded","connections":3}`), &status); err != nil {
		t.Fatal(err)
	}
	if status.Connections != 3 || StatusIsHealthy(status) {
		t.Fatalf("status = %#v", status)
	}
}

func TestAPIClientRejectsPathInjection(t *testing.T) {
	client, err := NewClient("token", "https://api.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListZones(context.Background(), "account/../../evil"); err == nil {
		t.Fatal("path-injected account ID was accepted")
	}
	if _, err := client.CreateDNSRecord(context.Background(), "zone", DNSRecord{Type: "A", Name: "x", Content: "y"}); err == nil {
		t.Fatal("non-CNAME record was accepted")
	}
}
