package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const cloudflareHTTPTestToken = "cf-http-token-never-serialized"

func TestAdminCloudflareConnectionOwnerBoundaryAndSecretProjectionIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Cloudflare API PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	rootPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rootPool.Close)
	schema := "cloudflare_api_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_, _ = rootPool.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE")
	})
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	type testPrincipal struct {
		id    uuid.UUID
		role  string
		token string
	}
	principals := []testPrincipal{
		{id: uuid.Must(uuid.NewV7()), role: "instance_owner"},
		{id: uuid.Must(uuid.NewV7()), role: "instance_admin"},
		{id: uuid.Must(uuid.NewV7())}, // organization owner only
		{id: uuid.Must(uuid.NewV7())}, // regular account
	}
	for i := range principals {
		principal := &principals[i]
		if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,'test-hash')`, principal.id, principal.id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		if principal.role != "" {
			if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,$2)`, principal.id, principal.role); err != nil {
				t.Fatal(err)
			}
		}
		principal.token, err = createCloudflareAPITestSession(t, ctx, pool, principal.id)
		if err != nil {
			t.Fatal(err)
		}
	}
	organizationID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'Cloudflare API Test','cloudflare-api-test')`, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner')`, organizationID, principals[2].id); err != nil {
		t.Fatal(err)
	}

	cipher, err := functionsecret.New(bytes.Repeat([]byte("k"), functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.NewWithDependencies(pool, repository.Dependencies{CloudflareCipher: cipher})
	factory := func(token string) (cloudflare.Client, error) {
		return cloudflareAdminValidationClient{valid: token == cloudflareHTTPTestToken}, nil
	}
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		PublicAppURL:       "https://Cloud.Example.com/console",
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		StorageRoot:        t.TempDir(),
		StorageMaxFileSize: 1 << 20,
		FunctionsSecretKey: bytes.Repeat([]byte("k"), functionsecret.KeySize),
	}, repo, slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Dependencies{CloudflareFactory: factory}))
	t.Cleanup(server.Close)

	clients := make([]*http.Client, len(principals))
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for i, principal := range principals {
		clients[i] = newIntegrationClient(t)
		clients[i].Jar.SetCookies(serverURL, []*http.Cookie{{Name: "stealth_session", Value: principal.token, Path: "/"}})
	}
	endpoint := server.URL + "/v1/admin/cloudflare"
	var initial struct {
		Configured bool   `json:"configured"`
		Status     string `json:"status"`
	}
	requestJSON(t, clients[0], http.MethodGet, endpoint, nil, http.StatusOK, &initial)
	if initial.Configured || initial.Status != "unconfigured" {
		t.Fatalf("initial Cloudflare status = %+v", initial)
	}
	requestJSON(t, clients[1], http.MethodGet, endpoint, nil, http.StatusOK, &struct{}{})
	requestJSON(t, clients[2], http.MethodGet, endpoint, nil, http.StatusForbidden, nil)
	requestJSON(t, clients[3], http.MethodGet, endpoint, nil, http.StatusForbidden, nil)
	requestJSON(t, newIntegrationClient(t), http.MethodGet, endpoint, nil, http.StatusUnauthorized, nil)

	requestRawJSON(t, clients[1], http.MethodPut, endpoint, `{"api_token":"`+cloudflareHTTPTestToken+`","account_id":"account-1","tunnel_id":"tunnel-1"}`, http.StatusForbidden, nil)
	requestRawJSON(t, clients[2], http.MethodPut, endpoint, `{"api_token":"`+cloudflareHTTPTestToken+`","account_id":"account-1","tunnel_id":"tunnel-1"}`, http.StatusForbidden, nil)
	requestRawJSON(t, clients[3], http.MethodPut, endpoint, `{"api_token":"`+cloudflareHTTPTestToken+`","account_id":"account-1","tunnel_id":"tunnel-1"}`, http.StatusForbidden, nil)

	var successBody []byte
	request := mustCloudflareAPIRequest(t, http.MethodPut, endpoint, `{"api_token":"`+cloudflareHTTPTestToken+`","account_id":"account-1","tunnel_id":"tunnel-1"}`)
	response, err := clients[0].Do(request)
	if err != nil {
		t.Fatal(err)
	}
	successBody, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owner Cloudflare replacement status=%d body=%s", response.StatusCode, successBody)
	}
	for _, secret := range []string{cloudflareHTTPTestToken, "api_token", "api_token_ciphertext", "tunnel_token"} {
		if bytes.Contains(successBody, []byte(secret)) {
			t.Fatalf("Cloudflare response contains %q: %s", secret, successBody)
		}
	}
	var ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT api_token_ciphertext FROM cloudflare_connections WHERE id=TRUE`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(cloudflareHTTPTestToken)) {
		t.Fatal("Cloudflare API token was stored in plaintext")
	}
	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil || string(plaintext) != cloudflareHTTPTestToken {
		t.Fatalf("decrypt saved API token = %q, %v", plaintext, err)
	}

	invalidRequest := mustCloudflareAPIRequest(t, http.MethodPut, endpoint, `{"api_token":"candidate-invalid-token","account_id":"account-1","tunnel_id":"tunnel-1"}`)
	invalidResponse, err := clients[0].Do(invalidRequest)
	if err != nil {
		t.Fatal(err)
	}
	invalidBody, err := io.ReadAll(invalidResponse.Body)
	_ = invalidResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if invalidResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid token replacement status=%d body=%s", invalidResponse.StatusCode, invalidBody)
	}
	if bytes.Contains(invalidBody, []byte("candidate-invalid-token")) || bytes.Contains(invalidBody, []byte(cloudflareHTTPTestToken)) {
		t.Fatalf("invalid-token response leaked credential material: %s", invalidBody)
	}
	var afterFailure []byte
	if err := pool.QueryRow(ctx, `SELECT api_token_ciphertext FROM cloudflare_connections WHERE id=TRUE`).Scan(&afterFailure); err != nil {
		t.Fatal(err)
	}
	decrypted, err := cipher.Decrypt(afterFailure)
	if err != nil || string(decrypted) != cloudflareHTTPTestToken {
		t.Fatalf("invalid replacement changed saved token to %q: %v", decrypted, err)
	}
	var auditMetadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE action='admin.cloudflare.connection.update' ORDER BY created_at DESC LIMIT 1`).Scan(&auditMetadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditMetadata, cloudflareHTTPTestToken) || strings.Contains(auditMetadata, "candidate-invalid-token") {
		t.Fatalf("token appeared in audit metadata: %s", auditMetadata)
	}
}

func createCloudflareAPITestSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID) (string, error) {
	t.Helper()
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		return "", err
	}
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id,account_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, uuid.Must(uuid.NewV7()), accountID, tokenHash, time.Now().UTC().Add(time.Hour))
	return token, err
}

func mustCloudflareAPIRequest(t *testing.T, method, endpoint, body string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	return request
}

type cloudflareAdminValidationClient struct {
	valid bool
}

func (c cloudflareAdminValidationClient) reject() error {
	if !c.valid {
		return cloudflare.ErrUnauthorized
	}
	return nil
}
func (c cloudflareAdminValidationClient) ListAccounts(context.Context) ([]cloudflare.Account, error) {
	if err := c.reject(); err != nil {
		return nil, err
	}
	return []cloudflare.Account{{ID: "account-1", Name: "Acme"}}, nil
}
func (c cloudflareAdminValidationClient) ListZones(context.Context, string) ([]cloudflare.Zone, error) {
	if err := c.reject(); err != nil {
		return nil, err
	}
	return []cloudflare.Zone{{ID: "console-zone", Name: "example.com"}}, nil
}
func (c cloudflareAdminValidationClient) ListCertificatePacks(context.Context, string) ([]cloudflare.CertificatePack, error) {
	if err := c.reject(); err != nil {
		return nil, err
	}
	return nil, nil
}
func (c cloudflareAdminValidationClient) TotalTLSSettings(context.Context, string) (cloudflare.TotalTLSSettings, error) {
	if err := c.reject(); err != nil {
		return cloudflare.TotalTLSSettings{}, err
	}
	return cloudflare.TotalTLSSettings{}, nil
}
func (c cloudflareAdminValidationClient) ListTunnels(context.Context, string, string) ([]cloudflare.Tunnel, error) {
	return nil, nil
}
func (c cloudflareAdminValidationClient) CreateTunnel(context.Context, string, string) (cloudflare.Tunnel, error) {
	return cloudflare.Tunnel{}, errors.New("unexpected tunnel creation")
}
func (c cloudflareAdminValidationClient) ConfigureTunnel(context.Context, string, string, []cloudflare.IngressRule) error {
	return errors.New("unexpected tunnel mutation during validation")
}
func (c cloudflareAdminValidationClient) TunnelConfiguration(context.Context, string, string) ([]cloudflare.IngressRule, error) {
	if err := c.reject(); err != nil {
		return nil, err
	}
	return []cloudflare.IngressRule{{Hostname: "cloud.example.com", Service: "http://proxy:80"}, {Service: "http_status:404"}}, nil
}
func (c cloudflareAdminValidationClient) ListDNSRecords(context.Context, string, string) ([]cloudflare.DNSRecord, error) {
	if err := c.reject(); err != nil {
		return nil, err
	}
	return []cloudflare.DNSRecord{{ID: "console-record", Type: "CNAME", Name: "cloud.example.com", Content: "tunnel-1.cfargotunnel.com", Proxied: true, TTL: 1}}, nil
}
func (c cloudflareAdminValidationClient) GetDNSRecord(context.Context, string, string) (cloudflare.DNSRecord, error) {
	return cloudflare.DNSRecord{}, cloudflare.ErrResourceNotFound
}
func (c cloudflareAdminValidationClient) CreateDNSRecord(context.Context, string, cloudflare.DNSRecord) (cloudflare.DNSRecord, error) {
	return cloudflare.DNSRecord{}, errors.New("unexpected DNS creation during validation")
}
func (c cloudflareAdminValidationClient) UpdateDNSRecord(context.Context, string, string, cloudflare.DNSRecord) (cloudflare.DNSRecord, error) {
	return cloudflare.DNSRecord{}, errors.New("unexpected DNS update during validation")
}
func (c cloudflareAdminValidationClient) DeleteDNSRecord(context.Context, string, string) error {
	return errors.New("unexpected DNS deletion during validation")
}
func (c cloudflareAdminValidationClient) TunnelStatus(_ context.Context, _, tunnelID string) (cloudflare.TunnelStatus, error) {
	if err := c.reject(); err != nil {
		return cloudflare.TunnelStatus{}, err
	}
	return cloudflare.TunnelStatus{ID: tunnelID, Name: "stealth-prod", Status: "healthy"}, nil
}
func (c cloudflareAdminValidationClient) TunnelToken(context.Context, string, string) (string, error) {
	return "unused-tunnel-token", nil
}
