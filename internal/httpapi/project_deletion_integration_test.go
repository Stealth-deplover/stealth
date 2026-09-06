package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/repository"
)

func TestProjectDeletionIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	storageRoot := t.TempDir()
	secret := []byte("01234567890123456789012345678901")
	server := httpapi.New(config.Config{
		SessionCookieName:          "stealth_session",
		SessionTTL:                 time.Hour,
		StorageRoot:                storageRoot,
		StorageMaxFileSize:         1 << 20,
		StorageDefaultQuotaBytes:   4 << 20,
		FunctionsMaxArtifactSize:   1 << 20,
		FunctionsDefaultQuotaBytes: 4 << 20,
		FunctionsSecretKey:         secret,
	}, repository.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	client := newIntegrationClient(t)
	identity := uuid.Must(uuid.NewV7())
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	email := fmt.Sprintf("project-delete-%s@example.test", identity)
	requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/account/registrations", map[string]string{
		"email": email, "password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)

	projectName := "delete-project-" + identity.String()[:8]
	var project struct {
		Project struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"project"`
	}
	requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{
		"name": projectName,
	}, http.StatusCreated, &project)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, registration.Account.ID, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	// Seed each local artifact namespace. The API must remove only this UUID
	// namespace after the database transaction commits.
	for _, namespace := range []string{"", "functions", "sites", "site-archives"} {
		path := filepath.Join(storageRoot, namespace, project.Project.ID, "sentinel")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	projectURL := httpServer.URL + "/v1/projects/" + project.Project.ID
	requestJSON(t, client, http.MethodDelete, projectURL, map[string]string{"confirm_name": " " + projectName + " "}, http.StatusUnprocessableEntity, nil)
	requestJSON(t, client, http.MethodGet, projectURL, nil, http.StatusOK, &struct{}{})
	requestJSON(t, client, http.MethodDelete, projectURL, map[string]string{"confirm_name": projectName}, http.StatusNoContent, nil)
	requestJSON(t, client, http.MethodGet, projectURL, nil, http.StatusNotFound, nil)

	var projectRows, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE id=$1`, project.Project.ID).Scan(&projectRows); err != nil {
		t.Fatal(err)
	}
	if projectRows != 0 {
		t.Fatalf("deleted project rows = %d, want 0", projectRows)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='project.delete'`, project.Project.ID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if auditRows != 1 {
		t.Fatalf("project delete audit rows = %d, want 1", auditRows)
	}
	for _, namespace := range []string{"", "functions", "sites", "site-archives"} {
		path := filepath.Join(storageRoot, namespace, project.Project.ID)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("artifact namespace %q still exists: %v", namespace, err)
		}
	}
}
