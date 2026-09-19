package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInstanceAdminRoutesUseInstanceRoleBoundary(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run instance-admin authorization tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		StorageRoot:        t.TempDir(),
		StorageMaxFileSize: 1 << 20,
		FunctionsSecretKey: bytes.Repeat([]byte("k"), 32),
	}, repository.New(pool), logger))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	owner := registerAdminTestAccount(t, ownerClient, server.URL, "owner")
	if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id, role) VALUES ($1, 'instance_admin')`, owner.accountID); err != nil {
		t.Fatal(err)
	}

	memberClient := newIntegrationClient(t)
	member := registerAdminTestAccount(t, memberClient, server.URL, "member")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id IN ($1,$2) OR organization_id IN ($3,$4)`, owner.accountID, member.accountID, owner.organizationID, member.organizationID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id IN ($1,$2)`, owner.organizationID, member.organizationID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id IN ($1,$2)`, owner.accountID, member.accountID)
	})
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+owner.organizationID+"/memberships", map[string]string{
		"email": member.email,
		"role":  "viewer",
	}, http.StatusCreated, &struct{}{})

	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/admin/overview", nil, http.StatusOK, &struct{}{})
	requestJSON(t, memberClient, http.MethodGet, server.URL+"/v1/admin/overview", nil, http.StatusForbidden, nil)
	requestJSON(t, newIntegrationClient(t), http.MethodGet, server.URL+"/v1/admin/overview", nil, http.StatusUnauthorized, nil)
}

type adminTestAccount struct {
	accountID      string
	organizationID string
	email          string
}

func registerAdminTestAccount(t *testing.T, client *http.Client, baseURL, label string) adminTestAccount {
	t.Helper()
	uniqueID := uuid.Must(uuid.NewV7())
	account := adminTestAccount{
		accountID:      "",
		organizationID: "",
		email:          "admin-boundary-" + label + "-" + uniqueID.String() + "@example.test",
	}
	var response struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, client, http.MethodPost, baseURL+"/v1/account/registrations", map[string]string{
		"email":    account.email,
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &response)
	account.accountID = response.Account.ID
	account.organizationID = response.Organization.ID
	return account
}
