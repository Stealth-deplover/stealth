package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/ratelimit"
	"github.com/nazxf/stealth-api/internal/repository"
)

type integrationTXTResolver struct {
	mu      sync.RWMutex
	records map[string][]string
}

func (r *integrationTXTResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.records[name]...), nil
}

func (r *integrationTXTResolver) set(name string, records ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[name] = append([]string(nil), records...)
}

func TestSiteDomainWriteScopeAndDNSVerificationIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	resolver := &integrationTXTResolver{records: make(map[string][]string)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		StorageRoot:        t.TempDir(),
		FunctionsSecretKey: bytes.Repeat([]byte{0x2a}, 32),
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		AppSessionTTL:      time.Hour,
		AuthRateLimit:      100,
		AuthRateWindow:     time.Minute,
	}, repository.NewWithDependencies(pool, repository.Dependencies{TXTResolver: resolver}), logger, httpapi.Dependencies{AuthLimiter: ratelimit.NewMemoryLimiter()}))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("site-domain-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1 OR actor_account_id=$2`, registration.Organization.ID, registration.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{
		"name": "site-domain-" + ownerID.String()[:8],
	}, http.StatusCreated, &project)
	projectURL := server.URL + "/v1/projects/" + project.Project.ID
	var site struct {
		Site struct {
			ID string `json:"id"`
		} `json:"site"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/sites", map[string]string{"name": "domain-site"}, http.StatusCreated, &site)

	var created struct {
		Domain struct {
			ID                 string `json:"id"`
			Hostname           string `json:"hostname"`
			Status             string `json:"status"`
			VerificationRecord string `json:"verification_record_name"`
			VerificationValue  string `json:"verification_record_value"`
		} `json:"domain"`
	}
	// UUIDv7 values generated in the same second share their leading time
	// bytes, so using only the first eight characters makes repeated local
	// integration runs collide on the hostname uniqueness constraint.
	rawHostname := "WWW-" + strings.ReplaceAll(ownerID.String(), "-", "") + ".EXAMPLE.TEST."
	expectedHostname := strings.ToLower(strings.TrimSuffix(rawHostname, "."))
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/sites/"+site.Site.ID+"/domains", map[string]string{"hostname": rawHostname}, http.StatusCreated, &created)
	if created.Domain.Hostname != expectedHostname || created.Domain.Status != "pending" || created.Domain.VerificationValue == "" {
		t.Fatalf("unexpected domain challenge: %+v", created.Domain)
	}

	var key struct {
		Secret string `json:"secret"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{
		"name":   "site-domain-write-only",
		"scopes": []string{"sites.write"},
	}, http.StatusCreated, &key)
	resolver.set(created.Domain.VerificationRecord, created.Domain.VerificationValue)
	verifyBody := requestJSONRawWithHeaders(t, newIntegrationClient(t), http.MethodPost, projectURL+"/sites/"+site.Site.ID+"/domains/"+created.Domain.ID+"/verify", nil, http.StatusOK, map[string]string{"X-Stealth-Key": key.Secret})
	if !bytes.Contains(verifyBody, []byte(`"status":"verified"`)) {
		t.Fatalf("write-only key did not verify domain: %s", verifyBody)
	}
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/sites/"+site.Site.ID+"/domains/"+created.Domain.ID, nil, http.StatusForbidden, map[string]string{"X-Stealth-Key": key.Secret})
}
