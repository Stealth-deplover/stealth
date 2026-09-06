package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/nazxf/stealth-api/internal/apikey"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/ratelimit"
	"github.com/nazxf/stealth-api/internal/repository"
)

func TestConsoleIdentityFlowIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repository.New(pool), logger))
	defer server.Close()
	client := &http.Client{Jar: jar}
	uniqueID := uuid.Must(uuid.NewV7())
	email := fmt.Sprintf("integration-%s@example.test", uniqueID.String())
	password := "correct-horse-battery-staple"

	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": email, "password": password}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, registration.Account.ID, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})

	var account struct {
		Account struct {
			Email string `json:"email"`
		} `json:"account"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/account", nil, http.StatusOK, &account)
	if account.Account.Email != email {
		t.Fatalf("got account email %q", account.Account.Email)
	}

	// A second browser can sign in independently. The Console account API
	// exposes only safe session metadata and lets the first browser revoke the
	// other one without invalidating its own current session.
	secondSessionClient := newIntegrationClient(t)
	requestJSON(t, secondSessionClient, http.MethodPost, server.URL+"/v1/sessions/email-password", map[string]string{"email": email, "password": password}, http.StatusNoContent, nil)
	var sessions struct {
		Sessions []struct {
			ID        string `json:"id"`
			IsCurrent bool   `json:"is_current"`
		} `json:"sessions"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/account/sessions", nil, http.StatusOK, &sessions)
	if len(sessions.Sessions) != 2 {
		t.Fatalf("got %d Console sessions, want 2", len(sessions.Sessions))
	}
	var otherSessionID string
	currentCount := 0
	for _, item := range sessions.Sessions {
		if item.IsCurrent {
			currentCount++
		} else {
			otherSessionID = item.ID
		}
	}
	if currentCount != 1 || otherSessionID == "" {
		t.Fatalf("session current markers = %+v", sessions.Sessions)
	}
	requestJSON(t, client, http.MethodDelete, server.URL+"/v1/account/sessions/"+otherSessionID, nil, http.StatusNoContent, nil)
	requestJSON(t, secondSessionClient, http.MethodGet, server.URL+"/v1/account", nil, http.StatusUnauthorized, nil)

	// Re-authenticate the second browser and exercise the bulk action. It
	// preserves the caller's current session and reports the number revoked.
	requestJSON(t, secondSessionClient, http.MethodPost, server.URL+"/v1/sessions/email-password", map[string]string{"email": email, "password": password}, http.StatusNoContent, nil)
	var revoked struct {
		Count int64 `json:"revoked"`
	}
	requestJSON(t, client, http.MethodDelete, server.URL+"/v1/account/sessions", nil, http.StatusOK, &revoked)
	if revoked.Count != 1 {
		t.Fatalf("bulk revoke count = %d, want 1", revoked.Count)
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/account", nil, http.StatusOK, &struct{}{})
	requestJSON(t, secondSessionClient, http.MethodGet, server.URL+"/v1/account", nil, http.StatusUnauthorized, nil)

	var organizations struct {
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/organizations", nil, http.StatusOK, &organizations)
	if len(organizations.Organizations) == 0 || organizations.Organizations[0].ID != registration.Organization.ID {
		t.Fatal("personal organization missing")
	}
	updatedOrganizationName := "Renamed Workspace"
	updatedOrganizationSlug := "renamed-workspace-" + uniqueID.String()[:8]
	var updatedOrganization struct {
		Organization struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"organization"`
	}
	requestJSON(t, client, http.MethodPatch, server.URL+"/v1/organizations/"+registration.Organization.ID, map[string]string{"name": updatedOrganizationName, "slug": updatedOrganizationSlug}, http.StatusOK, &updatedOrganization)
	if updatedOrganization.Organization.ID != registration.Organization.ID || updatedOrganization.Organization.Name != updatedOrganizationName || updatedOrganization.Organization.Slug != updatedOrganizationSlug {
		t.Fatalf("updated organization = %+v", updatedOrganization.Organization)
	}

	var auditEvents struct {
		Events []struct {
			Action     string         `json:"action"`
			ActorEmail *string        `json:"actor_email"`
			Metadata   map[string]any `json:"metadata"`
		} `json:"events"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/organizations/"+registration.Organization.ID+"/audit-events", nil, http.StatusOK, &auditEvents)
	if len(auditEvents.Events) == 0 {
		t.Fatal("organization audit events missing")
	}
	if auditEvents.Events[0].ActorEmail == nil || *auditEvents.Events[0].ActorEmail != email {
		t.Fatalf("audit actor email = %v, want %q", auditEvents.Events[0].ActorEmail, email)
	}
	if auditEvents.Events[0].Action == "" || auditEvents.Events[0].Metadata == nil {
		t.Fatalf("audit event missing durable fields: %+v", auditEvents.Events[0])
	}
	var ownerMemberships struct {
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/organizations/"+registration.Organization.ID+"/memberships", nil, http.StatusOK, &ownerMemberships)
	if !ownerMemberships.CanManage {
		t.Fatal("organization owner should have can_manage capability")
	}

	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	createdName := "integration-project-" + uniqueID.String()[:8]
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": createdName}, http.StatusCreated, &project)
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID, nil, http.StatusOK, &struct{}{})
	var projectAudit struct {
		Events []struct {
			Action   string         `json:"action"`
			Metadata map[string]any `json:"metadata"`
		} `json:"events"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID+"/audit-events", nil, http.StatusOK, &projectAudit)
	if len(projectAudit.Events) == 0 || projectAudit.Events[0].Action != "project.create" {
		t.Fatalf("project audit events = %+v", projectAudit.Events)
	}
	if projectAudit.Events[0].Metadata["project_id"] != project.Project.ID {
		t.Fatalf("project audit metadata = %+v", projectAudit.Events[0].Metadata)
	}

	updatedName := "renamed-project-" + uniqueID.String()[:8]
	var updatedProject struct {
		Project struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"project"`
	}
	requestJSON(t, client, http.MethodPatch, server.URL+"/v1/projects/"+project.Project.ID, map[string]string{"name": updatedName}, http.StatusOK, &updatedProject)
	if updatedProject.Project.ID != project.Project.ID || updatedProject.Project.Name != updatedName {
		t.Fatalf("updated project = %+v", updatedProject.Project)
	}
	var updateAudit struct {
		Events []struct {
			Action   string         `json:"action"`
			Metadata map[string]any `json:"metadata"`
		} `json:"events"`
	}
	requestJSON(t, client, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID+"/audit-events", nil, http.StatusOK, &updateAudit)
	var updateEvent *struct {
		Action   string         `json:"action"`
		Metadata map[string]any `json:"metadata"`
	}
	for index := range updateAudit.Events {
		if updateAudit.Events[index].Action == "project.update" {
			updateEvent = &updateAudit.Events[index]
			break
		}
	}
	if updateEvent == nil {
		t.Fatalf("project update audit events = %+v", updateAudit.Events)
	}
	if updateEvent.Metadata["from"] != createdName || updateEvent.Metadata["to"] != updatedName {
		t.Fatalf("project update audit metadata = %+v", updateEvent.Metadata)
	}

	secondJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	secondClient := &http.Client{Jar: secondJar}
	secondID := uuid.Must(uuid.NewV7())
	var secondRegistration struct {
		Account struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, secondClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": fmt.Sprintf("integration-%s@example.test", secondID.String()), "password": password}, http.StatusCreated, &secondRegistration)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, secondRegistration.Account.ID, secondRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, secondRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, secondRegistration.Account.ID)
	})
	requestJSON(t, secondClient, http.MethodGet, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", nil, http.StatusForbidden, nil)
	requestJSON(t, secondClient, http.MethodGet, server.URL+"/v1/organizations/"+registration.Organization.ID+"/memberships", nil, http.StatusForbidden, nil)
	requestJSON(t, secondClient, http.MethodPatch, server.URL+"/v1/organizations/"+registration.Organization.ID, map[string]string{"name": "not-authorized", "slug": "not-authorized"}, http.StatusForbidden, nil)
	requestJSON(t, secondClient, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID, nil, http.StatusNotFound, nil)
	var addedMembership struct {
		Membership struct {
			AccountID string `json:"account_id"`
			Email     string `json:"email"`
			Role      string `json:"role"`
		} `json:"membership"`
	}
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/memberships", map[string]string{"email": secondRegistration.Account.Email, "role": "viewer"}, http.StatusCreated, &addedMembership)
	if addedMembership.Membership.AccountID != secondRegistration.Account.ID || addedMembership.Membership.Email != secondRegistration.Account.Email || addedMembership.Membership.Role != "viewer" {
		t.Fatalf("added membership = %+v", addedMembership.Membership)
	}
	requestJSON(t, client, http.MethodPatch, server.URL+"/v1/organizations/"+registration.Organization.ID+"/memberships/"+secondRegistration.Account.ID, map[string]string{"role": "developer"}, http.StatusOK, &struct{}{})
	requestJSON(t, client, http.MethodPatch, server.URL+"/v1/organizations/"+registration.Organization.ID+"/memberships/"+secondRegistration.Account.ID, map[string]string{"role": "viewer"}, http.StatusOK, &struct{}{})
	var viewerMemberships struct {
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, secondClient, http.MethodGet, server.URL+"/v1/organizations/"+registration.Organization.ID+"/memberships", nil, http.StatusOK, &viewerMemberships)
	if viewerMemberships.CanManage {
		t.Fatal("viewer should not have can_manage capability")
	}
	requestJSON(t, secondClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "viewer-must-not-write"}, http.StatusForbidden, nil)
	requestJSON(t, secondClient, http.MethodPatch, server.URL+"/v1/projects/"+project.Project.ID, map[string]string{"name": "viewer-must-not-rename"}, http.StatusForbidden, nil)
	requestJSON(t, client, http.MethodDelete, server.URL+"/v1/organizations/"+registration.Organization.ID+"/memberships/"+secondRegistration.Account.ID, nil, http.StatusNoContent, nil)

	requestJSON(t, client, http.MethodDelete, server.URL+"/v1/session", nil, http.StatusNoContent, nil)
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/sessions/email-password", map[string]string{"email": email, "password": password}, http.StatusNoContent, nil)
}

func TestConsolePasswordUpdateIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repository.New(pool), logger))
	defer server.Close()

	client := newIntegrationClient(t)
	otherClient := newIntegrationClient(t)
	identity := uuid.Must(uuid.NewV7())
	email := fmt.Sprintf("password-%s@example.test", identity)
	oldPassword := "correct-horse-battery-staple"
	newPassword := "even-better-password-2026"
	registration := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}{}
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": email, "password": oldPassword}, http.StatusCreated, &registration)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_account_id=$1 OR organization_id=$2`, registration.Account.ID, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})
	requestJSON(t, otherClient, http.MethodPost, server.URL+"/v1/sessions/email-password", map[string]string{"email": email, "password": oldPassword}, http.StatusNoContent, nil)
	requestJSON(t, client, http.MethodPatch, server.URL+"/v1/account/password", map[string]string{"current_password": "wrong-current-password", "password": newPassword}, http.StatusUnauthorized, nil)
	var update struct {
		Revoked int64 `json:"sessions_revoked"`
	}
	requestJSON(t, client, http.MethodPatch, server.URL+"/v1/account/password", map[string]string{"current_password": oldPassword, "password": newPassword}, http.StatusOK, &update)
	if update.Revoked != 1 {
		t.Fatalf("sessions revoked = %d, want 1", update.Revoked)
	}
	requestJSON(t, otherClient, http.MethodGet, server.URL+"/v1/account", nil, http.StatusUnauthorized, nil)
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/sessions/email-password", map[string]string{"email": email, "password": oldPassword}, http.StatusUnauthorized, nil)
	requestJSON(t, client, http.MethodPost, server.URL+"/v1/sessions/email-password", map[string]string{"email": email, "password": newPassword}, http.StatusNoContent, nil)
}

func TestProjectApplicationUsersIntegration(t *testing.T) {
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repository.New(pool), logger))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	ownerEmail := fmt.Sprintf("project-users-owner-%s@example.test", ownerID)
	var ownerRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": ownerEmail, "password": "correct-horse-battery-staple"}, http.StatusCreated, &ownerRegistration)

	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{"name": "project-users-" + ownerID.String()[:8]}, http.StatusCreated, &project)

	viewerClient := newIntegrationClient(t)
	viewerID := uuid.Must(uuid.NewV7())
	var viewerRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, viewerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": fmt.Sprintf("project-users-viewer-%s@example.test", viewerID), "password": "correct-horse-battery-staple"}, http.StatusCreated, &viewerRegistration)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE target_id IN (SELECT id FROM project_users WHERE project_id=$1) OR actor_account_id IN ($2,$3) OR organization_id IN ($4,$5)`, project.Project.ID, ownerRegistration.Account.ID, viewerRegistration.Account.ID, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id IN ($1,$2)`, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id IN ($1,$2)`, ownerRegistration.Account.ID, viewerRegistration.Account.ID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id, account_id, role) VALUES ($1, $2, 'viewer')`, ownerRegistration.Organization.ID, viewerRegistration.Account.ID); err != nil {
		t.Fatal(err)
	}
	var otherProject struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, viewerClient, http.MethodPost, server.URL+"/v1/organizations/"+viewerRegistration.Organization.ID+"/projects", map[string]string{"name": "other-tenant-" + viewerID.String()[:8]}, http.StatusCreated, &otherProject)

	firstEmail := "USER@Example.Test"
	firstBody := requestJSONRaw(t, ownerClient, http.MethodPost, server.URL+"/v1/projects/"+project.Project.ID+"/users", map[string]string{"email": firstEmail, "password": "application-user-password-1", "name": "First User"}, http.StatusCreated)
	if bytes.Contains(firstBody, []byte("application-user-password-1")) || bytes.Contains(firstBody, []byte("password_hash")) {
		t.Fatalf("unsafe application-user response: %s", firstBody)
	}
	var firstResponse struct {
		User struct {
			ID            string  `json:"id"`
			Email         string  `json:"email"`
			Name          *string `json:"name"`
			Status        string  `json:"status"`
			EmailVerified bool    `json:"email_verified"`
		} `json:"user"`
	}
	if err := json.Unmarshal(firstBody, &firstResponse); err != nil {
		t.Fatal(err)
	}
	if firstResponse.User.Email != "user@example.test" || firstResponse.User.Name == nil || *firstResponse.User.Name != "First User" || firstResponse.User.Status != "active" || firstResponse.User.EmailVerified {
		t.Fatalf("unexpected first user response: %s", firstBody)
	}
	var storedHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM project_users WHERE id=$1`, firstResponse.User.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(storedHash, "$argon2id$v=19$") || storedHash == "application-user-password-1" {
		t.Fatalf("stored application-user password is not an Argon2id hash: %q", storedHash)
	}

	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/projects/"+project.Project.ID+"/users", map[string]string{"email": "user@example.test", "password": "application-user-password-2"}, http.StatusConflict, nil)
	secondBody := requestJSONRaw(t, ownerClient, http.MethodPost, server.URL+"/v1/projects/"+project.Project.ID+"/users", map[string]string{"email": "second@example.test", "password": "application-user-password-2"}, http.StatusCreated)
	var secondResponse struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(secondBody, &secondResponse); err != nil {
		t.Fatal(err)
	}

	var firstPage struct {
		Users []struct {
			ID string `json:"id"`
		} `json:"users"`
		Pagination struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"pagination"`
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID+"/users?limit=1", nil, http.StatusOK, &firstPage)
	if len(firstPage.Users) != 1 || firstPage.Pagination.NextCursor == nil || !firstPage.CanManage {
		t.Fatalf("expected one user and a cursor, got %+v", firstPage)
	}
	var secondPage struct {
		Users []struct {
			ID string `json:"id"`
		} `json:"users"`
		Pagination struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"pagination"`
	}
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID+"/users?limit=1&cursor="+*firstPage.Pagination.NextCursor, nil, http.StatusOK, &secondPage)
	if len(secondPage.Users) != 1 || secondPage.Pagination.NextCursor != nil || secondPage.Users[0].ID == firstPage.Users[0].ID {
		t.Fatalf("pagination did not advance: first=%+v second=%+v", firstPage, secondPage)
	}

	var getResponse struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID+"/users/"+firstResponse.User.ID, nil, http.StatusOK, &getResponse)
	if getResponse.User.ID != firstResponse.User.ID {
		t.Fatalf("got user %q, wanted %q", getResponse.User.ID, firstResponse.User.ID)
	}

	var blockedResponse struct {
		User struct {
			Status string `json:"status"`
		} `json:"user"`
	}
	requestJSON(t, ownerClient, http.MethodPatch, server.URL+"/v1/projects/"+project.Project.ID+"/users/"+firstResponse.User.ID+"/status", map[string]string{"status": "blocked"}, http.StatusOK, &blockedResponse)
	if blockedResponse.User.Status != "blocked" {
		t.Fatal("user was not blocked")
	}
	requestJSON(t, ownerClient, http.MethodPatch, server.URL+"/v1/projects/"+project.Project.ID+"/users/"+firstResponse.User.ID+"/status", map[string]string{"status": "blocked"}, http.StatusOK, &blockedResponse)
	var createAuditCount, statusAuditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='project_user.create'`, firstResponse.User.ID).Scan(&createAuditCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='project_user.status_change'`, firstResponse.User.ID).Scan(&statusAuditCount); err != nil {
		t.Fatal(err)
	}
	if createAuditCount != 1 || statusAuditCount != 1 {
		t.Fatalf("unexpected audit counts: create=%d status=%d", createAuditCount, statusAuditCount)
	}

	var viewerPage struct {
		Users []struct {
			ID string `json:"id"`
		} `json:"users"`
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, viewerClient, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID+"/users", nil, http.StatusOK, &viewerPage)
	if len(viewerPage.Users) != 2 || viewerPage.CanManage {
		t.Fatalf("viewer could not list project users: %+v", viewerPage)
	}
	requestJSON(t, viewerClient, http.MethodPost, server.URL+"/v1/projects/"+project.Project.ID+"/users", map[string]string{"email": "viewer-write@example.test", "password": "application-user-password-3"}, http.StatusForbidden, nil)
	requestJSON(t, viewerClient, http.MethodPatch, server.URL+"/v1/projects/"+project.Project.ID+"/users/"+firstResponse.User.ID+"/status", map[string]string{"status": "active"}, http.StatusForbidden, nil)

	requestJSON(t, viewerClient, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID+"/users/"+firstResponse.User.ID, nil, http.StatusOK, &getResponse)
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/projects/"+otherProject.Project.ID+"/users", nil, http.StatusNotFound, nil)
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/projects/"+otherProject.Project.ID+"/users", map[string]string{"email": "cross-tenant@example.test", "password": "application-user-password-4"}, http.StatusNotFound, nil)
	requestJSON(t, ownerClient, http.MethodPatch, server.URL+"/v1/projects/"+otherProject.Project.ID+"/users/"+firstResponse.User.ID+"/status", map[string]string{"status": "active"}, http.StatusNotFound, nil)

	// Owners can permanently remove an application identity. The operation
	// revokes its sessions/tokens through the database cascade.
	requestJSON(t, ownerClient, http.MethodDelete, server.URL+"/v1/projects/"+project.Project.ID+"/users/"+secondResponse.User.ID, nil, http.StatusNoContent, nil)
	requestJSON(t, ownerClient, http.MethodGet, server.URL+"/v1/projects/"+project.Project.ID+"/users/"+secondResponse.User.ID, nil, http.StatusNotFound, nil)
	requestJSON(t, ownerClient, http.MethodDelete, server.URL+"/v1/projects/"+project.Project.ID+"/users/"+secondResponse.User.ID, nil, http.StatusNotFound, nil)
}

func TestProjectApplicationAuthSessionsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	limiter := integrationLimiter(t, ctx)
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		SessionCookieName: "stealth_session",
		SessionTTL:        time.Hour,
		AppSessionTTL:     2 * time.Hour,
		AuthRateLimit:     100,
		AuthRateWindow:    time.Minute,
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: limiter}))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	ownerEmail := fmt.Sprintf("auth-owner-%s@example.test", ownerID)
	var ownerRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": ownerEmail, "password": "correct-horse-battery-staple"}, http.StatusCreated, &ownerRegistration)

	var firstProject struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{"name": "auth-sessions-" + ownerID.String()[:8]}, http.StatusCreated, &firstProject)
	var secondProject struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{"name": "auth-isolation-" + ownerID.String()[:8]}, http.StatusCreated, &secondProject)

	viewerClient := newIntegrationClient(t)
	viewerID := uuid.Must(uuid.NewV7())
	var viewerRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, viewerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": fmt.Sprintf("auth-viewer-%s@example.test", viewerID), "password": "correct-horse-battery-staple"}, http.StatusCreated, &viewerRegistration)
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id, account_id, role) VALUES ($1, $2, 'viewer')`, ownerRegistration.Organization.ID, viewerRegistration.Account.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id IN ($1,$2) OR actor_account_id IN ($3,$4)`, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID, ownerRegistration.Account.ID, viewerRegistration.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id IN ($1,$2)`, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id IN ($1,$2)`, ownerRegistration.Account.ID, viewerRegistration.Account.ID)
	})

	firstProjectURL := server.URL + "/v1/projects/" + firstProject.Project.ID
	secondProjectURL := server.URL + "/v1/projects/" + secondProject.Project.ID
	var settings struct {
		Settings struct {
			ProjectID           string `json:"project_id"`
			RegistrationEnabled bool   `json:"registration_enabled"`
		} `json:"settings"`
		CanManage bool `json:"can_manage"`
	}
	requestJSON(t, ownerClient, http.MethodGet, firstProjectURL+"/auth/settings", nil, http.StatusOK, &settings)
	if settings.Settings.RegistrationEnabled || !settings.CanManage {
		t.Fatalf("unexpected default settings: %+v", settings)
	}
	requestJSON(t, ownerClient, http.MethodPost, firstProjectURL+"/account/registrations", map[string]string{"email": "disabled@example.test", "password": "correct-horse-battery-staple"}, http.StatusForbidden, nil)
	requestJSON(t, ownerClient, http.MethodPatch, firstProjectURL+"/auth/settings", map[string]bool{"registration_enabled": true}, http.StatusOK, &settings)
	if !settings.Settings.RegistrationEnabled || !settings.CanManage {
		t.Fatalf("registration setting was not enabled: %+v", settings)
	}
	requestJSON(t, ownerClient, http.MethodPatch, firstProjectURL+"/auth/settings", map[string]bool{"registration_enabled": true}, http.StatusOK, &settings)
	var settingsAuditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action='project_auth.settings_update' AND target_id=$2`, ownerRegistration.Organization.ID, firstProject.Project.ID).Scan(&settingsAuditCount); err != nil {
		t.Fatal(err)
	}
	if settingsAuditCount != 1 {
		t.Fatalf("idempotent setting update wrote %d audit events, want 1", settingsAuditCount)
	}

	requestJSON(t, viewerClient, http.MethodGet, firstProjectURL+"/auth/settings", nil, http.StatusOK, &settings)
	if settings.CanManage {
		t.Fatal("viewer received can_manage=true")
	}
	requestJSON(t, viewerClient, http.MethodPatch, firstProjectURL+"/auth/settings", map[string]bool{"registration_enabled": false}, http.StatusForbidden, nil)

	appClient := newIntegrationClient(t)
	registrationEmail := "New.User@Example.Test"
	registrationPassword := "application-correct-password"
	registrationBody := requestJSONRaw(t, appClient, http.MethodPost, firstProjectURL+"/account/registrations", map[string]string{"email": registrationEmail, "password": registrationPassword, "name": "New User"}, http.StatusCreated)
	if bytes.Contains(registrationBody, []byte(registrationPassword)) || bytes.Contains(registrationBody, []byte("password_hash")) || bytes.Contains(registrationBody, []byte("token")) {
		t.Fatalf("unsafe registration response: %s", registrationBody)
	}
	var registrationResponse struct {
		Account struct {
			ID    string  `json:"id"`
			Email string  `json:"email"`
			Name  *string `json:"name"`
		} `json:"account"`
	}
	if err := json.Unmarshal(registrationBody, &registrationResponse); err != nil {
		t.Fatal(err)
	}
	if registrationResponse.Account.Email != "new.user@example.test" || registrationResponse.Account.Name == nil || *registrationResponse.Account.Name != "New User" {
		t.Fatalf("unexpected registration DTO: %s", registrationBody)
	}
	appURL, err := url.Parse(firstProjectURL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	appCookies := appClient.Jar.Cookies(appURL)
	if len(appCookies) != 1 || appCookies[0].Name != "stealth_app_"+strings.ReplaceAll(firstProject.Project.ID, "-", "") || appCookies[0].Path != "" {
		// CookieJar normalizes the path away from returned request cookies; the
		// server-side Set-Cookie is checked below for the exact scoped path.
		if len(appCookies) != 1 || appCookies[0].Name != "stealth_app_"+strings.ReplaceAll(firstProject.Project.ID, "-", "") {
			t.Fatalf("unexpected project cookies: %+v", appCookies)
		}
	}
	requestJSON(t, appClient, http.MethodGet, firstProjectURL+"/account", nil, http.StatusOK, &registrationResponse)
	var sessionCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM project_user_sessions WHERE project_id=$1 AND project_user_id=$2`, firstProject.Project.ID, registrationResponse.Account.ID).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 {
		t.Fatalf("registration created %d sessions, want 1", sessionCount)
	}
	var registrationAuditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='project_user.create' AND actor_account_id IS NULL`, registrationResponse.Account.ID).Scan(&registrationAuditCount); err != nil {
		t.Fatal(err)
	}
	if registrationAuditCount != 1 {
		t.Fatalf("registration wrote %d create audit events, want 1", registrationAuditCount)
	}
	requestJSON(t, appClient, http.MethodDelete, firstProjectURL+"/session", nil, http.StatusNoContent, nil)
	requestJSON(t, appClient, http.MethodGet, firstProjectURL+"/account", nil, http.StatusUnauthorized, nil)

	duplicateClient := newIntegrationClient(t)
	requestJSON(t, duplicateClient, http.MethodPost, firstProjectURL+"/account/registrations", map[string]string{"email": "new.user@example.test", "password": registrationPassword}, http.StatusConflict, nil)

	wrongClient := newIntegrationClient(t)
	wrongBody := requestJSONRaw(t, wrongClient, http.MethodPost, firstProjectURL+"/sessions/email-password", map[string]string{"email": "new.user@example.test", "password": "wrong-password-123"}, http.StatusUnauthorized)
	unknownBody := requestJSONRaw(t, wrongClient, http.MethodPost, firstProjectURL+"/sessions/email-password", map[string]string{"email": "unknown@example.test", "password": "wrong-password-123"}, http.StatusUnauthorized)
	if !bytes.Equal(wrongBody, unknownBody) {
		t.Fatalf("wrong and unknown credentials differ: wrong=%s unknown=%s", wrongBody, unknownBody)
	}
	overlongBody := requestJSONRaw(t, wrongClient, http.MethodPost, firstProjectURL+"/sessions/email-password", map[string]string{"email": "new.user@example.test", "password": strings.Repeat("x", 257)}, http.StatusUnauthorized)
	if !bytes.Equal(wrongBody, overlongBody) {
		t.Fatalf("overlong credentials differ: wrong=%s overlong=%s", wrongBody, overlongBody)
	}
	requestJSON(t, wrongClient, http.MethodPost, firstProjectURL+"/sessions/email-password", map[string]string{"email": "new.user@example.test", "password": registrationPassword}, http.StatusNoContent, nil)
	requestJSON(t, wrongClient, http.MethodGet, firstProjectURL+"/account", nil, http.StatusOK, &registrationResponse)

	requestJSON(t, ownerClient, http.MethodPatch, firstProjectURL+"/users/"+registrationResponse.Account.ID+"/status", map[string]string{"status": "blocked"}, http.StatusOK, nil)
	requestJSON(t, wrongClient, http.MethodGet, firstProjectURL+"/account", nil, http.StatusUnauthorized, nil)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM project_user_sessions WHERE project_user_id=$1`, registrationResponse.Account.ID).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatalf("blocking left %d project sessions", sessionCount)
	}
	requestJSON(t, ownerClient, http.MethodPatch, firstProjectURL+"/users/"+registrationResponse.Account.ID+"/status", map[string]string{"status": "blocked"}, http.StatusOK, nil)
	requestJSON(t, wrongClient, http.MethodPost, firstProjectURL+"/sessions/email-password", map[string]string{"email": "new.user@example.test", "password": registrationPassword}, http.StatusUnauthorized, nil)
	var statusAuditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='project_user.status_change'`, registrationResponse.Account.ID).Scan(&statusAuditCount); err != nil {
		t.Fatal(err)
	}
	if statusAuditCount != 1 {
		t.Fatalf("idempotent block wrote %d status audits, want 1", statusAuditCount)
	}
	requestJSON(t, ownerClient, http.MethodPatch, firstProjectURL+"/users/"+registrationResponse.Account.ID+"/status", map[string]string{"status": "active"}, http.StatusOK, nil)
	requestJSON(t, wrongClient, http.MethodGet, firstProjectURL+"/account", nil, http.StatusUnauthorized, nil)
	requestJSON(t, wrongClient, http.MethodPost, firstProjectURL+"/sessions/email-password", map[string]string{"email": "new.user@example.test", "password": registrationPassword}, http.StatusNoContent, nil)
	requestJSON(t, wrongClient, http.MethodGet, firstProjectURL+"/account", nil, http.StatusOK, &registrationResponse)

	// A project session cookie is scoped and namespaced by project. Even if a
	// caller manually copies it to another project, the project-bound lookup
	// cannot authenticate it.
	appCookies = wrongClient.Jar.Cookies(appURL)
	if len(appCookies) != 1 {
		t.Fatalf("expected one active app cookie, got %+v", appCookies)
	}
	crossRequest, err := http.NewRequest(http.MethodGet, secondProjectURL+"/account", nil)
	if err != nil {
		t.Fatal(err)
	}
	crossRequest.Header.Set("Cookie", appCookies[0].Name+"="+appCookies[0].Value)
	crossResponse, err := server.Client().Do(crossRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer crossResponse.Body.Close()
	if crossResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-project cookie returned status %d, want 401", crossResponse.StatusCode)
	}
}

func TestProjectAPIKeysIntegration(t *testing.T) {
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		SessionCookieName: "stealth_session",
		SessionTTL:        time.Hour,
		AppSessionTTL:     2 * time.Hour,
		AuthRateLimit:     100,
		AuthRateWindow:    time.Minute,
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: integrationLimiter(t, ctx)}))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	var ownerRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("api-key-owner-%s@example.test", ownerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &ownerRegistration)

	var projectResponse struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{
		"name": "api-keys-" + ownerID.String()[:8],
	}, http.StatusCreated, &projectResponse)
	projectURL := server.URL + "/v1/projects/" + projectResponse.Project.ID
	keyHeaders := func(secret string) map[string]string { return map[string]string{"X-Stealth-Key": secret} }

	viewerClient := newIntegrationClient(t)
	viewerID := uuid.Must(uuid.NewV7())
	var viewerRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, viewerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("api-key-viewer-%s@example.test", viewerID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &viewerRegistration)
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'viewer')`, ownerRegistration.Organization.ID, viewerRegistration.Account.ID); err != nil {
		t.Fatal(err)
	}

	outsiderClient := newIntegrationClient(t)
	outsiderID := uuid.Must(uuid.NewV7())
	var outsiderRegistration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, outsiderClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email":    fmt.Sprintf("api-key-outsider-%s@example.test", outsiderID),
		"password": "correct-horse-battery-staple",
	}, http.StatusCreated, &outsiderRegistration)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id IN ($1,$2,$3) OR actor_account_id IN ($4,$5,$6)`, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID, outsiderRegistration.Organization.ID, ownerRegistration.Account.ID, viewerRegistration.Account.ID, outsiderRegistration.Account.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id IN ($1,$2,$3)`, ownerRegistration.Organization.ID, viewerRegistration.Organization.ID, outsiderRegistration.Organization.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id IN ($1,$2,$3)`, ownerRegistration.Account.ID, viewerRegistration.Account.ID, outsiderRegistration.Account.ID)
	})

	type apiKeyDTO struct {
		ID         string     `json:"id"`
		ProjectID  string     `json:"project_id"`
		Name       string     `json:"name"`
		Prefix     string     `json:"prefix"`
		Scopes     []string   `json:"scopes"`
		ExpiresAt  *time.Time `json:"expires_at"`
		RevokedAt  *time.Time `json:"revoked_at"`
		LastUsedAt *time.Time `json:"last_used_at"`
	}
	type apiKeyEnvelope struct {
		Key    apiKeyDTO `json:"key"`
		Secret string    `json:"secret"`
	}

	expiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	firstBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{
		"name":       "Primary server key",
		"scopes":     []string{"users.write", "users.read", "users.write"},
		"expires_at": expiresAt,
	}, http.StatusCreated)
	if !bytes.Contains(firstBody, []byte(`"secret":"stl_key_`)) || bytes.Contains(firstBody, []byte("secret_hash")) {
		t.Fatalf("unexpected create response: %s", firstBody)
	}
	var firstKey apiKeyEnvelope
	if err := json.Unmarshal(firstBody, &firstKey); err != nil {
		t.Fatal(err)
	}
	if firstKey.Secret == "" || len(firstKey.Key.Scopes) != 2 || firstKey.Key.Scopes[0] != "users.read" || firstKey.Key.Scopes[1] != "users.write" {
		t.Fatalf("scopes/secret were not normalized: %+v", firstKey)
	}
	if firstKey.Key.Prefix == "" || !strings.HasPrefix(firstKey.Secret, firstKey.Key.Prefix) {
		t.Fatalf("secret prefix was not safe: %+v", firstKey)
	}
	var storedSecretHash []byte
	if err := pool.QueryRow(ctx, `SELECT secret_hash FROM project_api_keys WHERE id=$1`, firstKey.Key.ID).Scan(&storedSecretHash); err != nil {
		t.Fatal(err)
	}
	if len(storedSecretHash) != 32 || string(storedSecretHash) == firstKey.Secret || !bytes.Equal(storedSecretHash, apikey.HashSecret(firstKey.Secret)) {
		t.Fatalf("stored API key secret is not a SHA-256 digest: %x", storedSecretHash)
	}

	secondBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{
		"name":   "Read-only server key",
		"scopes": []string{"users.read"},
	}, http.StatusCreated)
	var secondKey apiKeyEnvelope
	if err := json.Unmarshal(secondBody, &secondKey); err != nil {
		t.Fatal(err)
	}

	listBody := requestJSONRaw(t, ownerClient, http.MethodGet, projectURL+"/api-keys?limit=1", nil, http.StatusOK)
	if bytes.Contains(listBody, []byte(firstKey.Secret)) || bytes.Contains(listBody, []byte(secondKey.Secret)) || bytes.Contains(listBody, []byte("secret_hash")) {
		t.Fatalf("list response leaked secret material: %s", listBody)
	}
	var firstPage struct {
		Keys       []apiKeyDTO `json:"keys"`
		Pagination struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"pagination"`
		CanManage bool `json:"can_manage"`
	}
	if err := json.Unmarshal(listBody, &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Keys) != 1 || firstPage.Pagination.NextCursor == nil || !firstPage.CanManage {
		t.Fatalf("unexpected first API key page: %+v", firstPage)
	}
	secondPageBody := requestJSONRaw(t, ownerClient, http.MethodGet, projectURL+"/api-keys?limit=1&cursor="+*firstPage.Pagination.NextCursor, nil, http.StatusOK)
	var secondPage struct {
		Keys       []apiKeyDTO `json:"keys"`
		Pagination struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(secondPageBody, &secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Keys) != 1 || secondPage.Pagination.NextCursor != nil || secondPage.Keys[0].ID == firstPage.Keys[0].ID {
		t.Fatalf("API key pagination did not advance: first=%+v second=%+v", firstPage, secondPage)
	}
	getBody := requestJSONRaw(t, ownerClient, http.MethodGet, projectURL+"/api-keys/"+firstKey.Key.ID, nil, http.StatusOK)
	if bytes.Contains(getBody, []byte(firstKey.Secret)) || bytes.Contains(getBody, []byte("secret_hash")) {
		t.Fatalf("get response leaked secret material: %s", getBody)
	}

	viewerListBody := requestJSONRaw(t, viewerClient, http.MethodGet, projectURL+"/api-keys", nil, http.StatusOK)
	if bytes.Contains(viewerListBody, []byte(firstKey.Secret)) || !bytes.Contains(viewerListBody, []byte(`"can_manage":false`)) {
		t.Fatalf("viewer API key list was not read-only: %s", viewerListBody)
	}
	requestJSON(t, viewerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "viewer key", "scopes": []string{"users.read"}}, http.StatusForbidden, nil)
	requestJSON(t, viewerClient, http.MethodDelete, projectURL+"/api-keys/"+firstKey.Key.ID, nil, http.StatusForbidden, nil)
	requestJSON(t, outsiderClient, http.MethodGet, projectURL+"/api-keys", nil, http.StatusNotFound, nil)
	requestJSON(t, outsiderClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "outsider key", "scopes": []string{"users.read"}}, http.StatusNotFound, nil)

	keyClient := newIntegrationClient(t)
	usersBody := requestJSONRawWithHeaders(t, keyClient, http.MethodGet, projectURL+"/users", nil, http.StatusOK, keyHeaders(firstKey.Secret))
	if bytes.Contains(usersBody, []byte(firstKey.Secret)) || bytes.Contains(usersBody, []byte("password_hash")) || !bytes.Contains(usersBody, []byte(`"can_manage":true`)) {
		t.Fatalf("API key read response was unsafe: %s", usersBody)
	}
	createdByKeyBody := requestJSONRawWithHeaders(t, keyClient, http.MethodPost, projectURL+"/users", map[string]string{
		"email":    fmt.Sprintf("created-by-key-%s@example.test", ownerID),
		"password": "application-user-password-1",
		"name":     "Created by key",
	}, http.StatusCreated, keyHeaders(firstKey.Secret))
	if bytes.Contains(createdByKeyBody, []byte("application-user-password-1")) || bytes.Contains(createdByKeyBody, []byte("password_hash")) || bytes.Contains(createdByKeyBody, []byte(firstKey.Secret)) {
		t.Fatalf("API key user create response was unsafe: %s", createdByKeyBody)
	}
	var createdByKey struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(createdByKeyBody, &createdByKey); err != nil {
		t.Fatal(err)
	}
	requestJSONWithHeaders(t, keyClient, http.MethodPatch, projectURL+"/users/"+createdByKey.User.ID+"/status", map[string]string{"status": "blocked"}, http.StatusOK, keyHeaders(firstKey.Secret))
	requestJSONWithHeaders(t, keyClient, http.MethodPatch, projectURL+"/users/"+createdByKey.User.ID+"/status", map[string]string{"status": "blocked"}, http.StatusOK, keyHeaders(firstKey.Secret))
	var keyStatusAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='project_user.status_change' AND target_id=$1 AND metadata->>'actor'='api_key'`, createdByKey.User.ID).Scan(&keyStatusAudits); err != nil {
		t.Fatal(err)
	}
	if keyStatusAudits != 1 {
		t.Fatalf("idempotent API-key status wrote %d audits, want 1", keyStatusAudits)
	}
	var actorKeyID string
	if err := pool.QueryRow(ctx, `SELECT metadata->>'api_key_id' FROM audit_events WHERE action='project_user.create' AND target_id=$1`, createdByKey.User.ID).Scan(&actorKeyID); err != nil {
		t.Fatal(err)
	}
	if actorKeyID != firstKey.Key.ID {
		t.Fatalf("API-key create audit actor = %q, want %q", actorKeyID, firstKey.Key.ID)
	}

	writeOnlyBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "Write-only server key", "scopes": []string{"users.write"}}, http.StatusCreated)
	var writeOnlyKey apiKeyEnvelope
	if err := json.Unmarshal(writeOnlyBody, &writeOnlyKey); err != nil {
		t.Fatal(err)
	}
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/users", nil, http.StatusForbidden, keyHeaders(writeOnlyKey.Secret))
	requestJSONWithHeaders(t, keyClient, http.MethodPost, projectURL+"/users", map[string]string{"email": fmt.Sprintf("write-only-%s@example.test", ownerID), "password": "application-user-password-2"}, http.StatusCreated, keyHeaders(writeOnlyKey.Secret))
	requestJSONWithHeaders(t, keyClient, http.MethodPost, projectURL+"/users", map[string]string{"email": fmt.Sprintf("read-only-write-%s@example.test", ownerID), "password": "application-user-password-2"}, http.StatusForbidden, keyHeaders(secondKey.Secret))
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/users", nil, http.StatusOK, keyHeaders(secondKey.Secret))
	requestJSONWithHeaders(t, keyClient, http.MethodPatch, projectURL+"/users/"+createdByKey.User.ID+"/status", map[string]string{"status": "active"}, http.StatusForbidden, keyHeaders(secondKey.Secret))
	limitedServer := httptest.NewServer(httpapi.NewWithDependencies(config.Config{
		SessionCookieName: "stealth_session",
		SessionTTL:        time.Hour,
		AppSessionTTL:     time.Hour,
		AuthRateLimit:     1,
		AuthRateWindow:    time.Minute,
	}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: ratelimit.NewMemoryLimiter()}))
	defer limitedServer.Close()
	limitedClient := newIntegrationClient(t)
	limitedProjectURL := limitedServer.URL + "/v1/projects/" + projectResponse.Project.ID
	requestJSONWithHeaders(t, limitedClient, http.MethodGet, limitedProjectURL+"/users", nil, http.StatusOK, keyHeaders(secondKey.Secret))
	requestJSONWithHeaders(t, limitedClient, http.MethodGet, limitedProjectURL+"/users", nil, http.StatusOK, keyHeaders(secondKey.Secret))
	requestJSONWithHeaders(t, limitedClient, http.MethodGet, limitedProjectURL+"/users", nil, http.StatusUnauthorized, keyHeaders("stl_key_invalid"))
	requestJSONWithHeaders(t, limitedClient, http.MethodGet, limitedProjectURL+"/users", nil, http.StatusTooManyRequests, keyHeaders("stl_key_invalid"))

	unknownSecret, _, _, err := apikey.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	unknownBody := requestJSONRawWithHeaders(t, keyClient, http.MethodGet, projectURL+"/users", nil, http.StatusUnauthorized, keyHeaders(unknownSecret))
	malformedBody := requestJSONRawWithHeaders(t, keyClient, http.MethodGet, projectURL+"/users", nil, http.StatusUnauthorized, keyHeaders("stl_key_invalid"))
	if !bytes.Equal(unknownBody, malformedBody) {
		t.Fatalf("invalid API key classes differed: unknown=%s malformed=%s", unknownBody, malformedBody)
	}
	requestJSONWithHeaders(t, keyClient, http.MethodGet, server.URL+"/v1/projects/"+uuid.Must(uuid.NewV7()).String()+"/users", nil, http.StatusUnauthorized, keyHeaders(firstKey.Secret))

	var lastUsedBefore, lastUsedAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT last_used_at FROM project_api_keys WHERE id=$1`, secondKey.Key.ID).Scan(&lastUsedBefore); err != nil {
		t.Fatal(err)
	}
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/users", nil, http.StatusOK, keyHeaders(secondKey.Secret))
	if err := pool.QueryRow(ctx, `SELECT last_used_at FROM project_api_keys WHERE id=$1`, secondKey.Key.ID).Scan(&lastUsedAfter); err != nil {
		t.Fatal(err)
	}
	if lastUsedBefore.IsZero() || !lastUsedBefore.Equal(lastUsedAfter) {
		t.Fatalf("API key usage timestamp changed too often: before=%v after=%v", lastUsedBefore, lastUsedAfter)
	}

	requestJSON(t, ownerClient, http.MethodDelete, projectURL+"/api-keys/"+firstKey.Key.ID, nil, http.StatusNoContent, nil)
	requestJSON(t, ownerClient, http.MethodDelete, projectURL+"/api-keys/"+firstKey.Key.ID, nil, http.StatusNoContent, nil)
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/users", nil, http.StatusUnauthorized, keyHeaders(firstKey.Secret))
	var revokeAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='project_api_key.revoke' AND target_id=$1`, firstKey.Key.ID).Scan(&revokeAudits); err != nil {
		t.Fatal(err)
	}
	if revokeAudits != 1 {
		t.Fatalf("idempotent API-key revoke wrote %d audits, want 1", revokeAudits)
	}

	expiredSecret, expiredPrefix, expiredHash, err := apikey.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	expiredID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO project_api_keys (id,project_id,name,prefix,secret_hash,scopes,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, expiredID, projectResponse.Project.ID, "Expired integration key", expiredPrefix, expiredHash, []string{"users.read"}, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/users", nil, http.StatusUnauthorized, keyHeaders(expiredSecret))

	appCookieClient := newIntegrationClient(t)
	appRequest, err := http.NewRequest(http.MethodGet, projectURL+"/users", nil)
	if err != nil {
		t.Fatal(err)
	}
	appRequest.Header.Set("Cookie", "stealth_app_"+strings.ReplaceAll(projectResponse.Project.ID, "-", "")+"=not-a-console-session")
	appResponse, err := appCookieClient.Do(appRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer appResponse.Body.Close()
	if appResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("application cookie authenticated management route with status %d", appResponse.StatusCode)
	}
}

func integrationLimiter(t *testing.T, ctx context.Context) ratelimit.Limiter {
	t.Helper()
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		return ratelimit.NewMemoryLimiter()
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	return ratelimit.NewRedisLimiter(client)
}

func newIntegrationClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func requestJSON(t *testing.T, client *http.Client, method, url string, payload any, expectedStatus int, target any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		contents, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s: expected %d, got %d: %s", method, url, expectedStatus, response.StatusCode, contents)
	}
	if target != nil && response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
}

func requestJSONRaw(t *testing.T, client *http.Client, method, url string, payload any, expectedStatus int) []byte {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	contents, _ := io.ReadAll(response.Body)
	if response.StatusCode != expectedStatus {
		t.Fatalf("%s %s: expected %d, got %d: %s", method, url, expectedStatus, response.StatusCode, contents)
	}
	return contents
}

func requestJSONWithHeaders(t *testing.T, client *http.Client, method, url string, payload any, expectedStatus int, headers map[string]string) {
	t.Helper()
	_ = requestJSONRawWithHeaders(t, client, method, url, payload, expectedStatus, headers)
}

func requestJSONRawWithHeaders(t *testing.T, client *http.Client, method, url string, payload any, expectedStatus int, headers map[string]string) []byte {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	contents, _ := io.ReadAll(response.Body)
	if response.StatusCode != expectedStatus {
		t.Fatalf("%s %s: expected %d, got %d: %s", method, url, expectedStatus, response.StatusCode, contents)
	}
	return contents
}

func TestProjectDatabasesCoreIntegration(t *testing.T) {
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.NewWithDependencies(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour, AppSessionTTL: time.Hour, StorageRoot: t.TempDir(), StorageMaxFileSize: 50 << 20}, repository.New(pool), logger, httpapi.Dependencies{AuthLimiter: integrationLimiter(t, ctx)}))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	ownerRegistration := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": "database-owner-" + ownerID.String() + "@example.test", "password": "correct-horse-battery-staple"}, http.StatusCreated, &ownerRegistration)
	projectResponse := struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+ownerRegistration.Organization.ID+"/projects", map[string]string{"name": "database-core-" + ownerID.String()[:8]}, http.StatusCreated, &projectResponse)
	projectURL := server.URL + "/v1/projects/" + projectResponse.Project.ID
	keyHeaders := func(secret string) map[string]string { return map[string]string{"X-Stealth-Key": secret} }
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, ownerRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, ownerRegistration.Account.ID)
	})

	viewerClient := newIntegrationClient(t)
	viewerID := uuid.Must(uuid.NewV7())
	viewerRegistration := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}{}
	viewerOrg := struct {
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}{}
	requestJSON(t, viewerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": "database-viewer-" + viewerID.String() + "@example.test", "password": "correct-horse-battery-staple"}, http.StatusCreated, &struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}{})
	// Fetch the viewer's own account/organization without depending on a
	// second response shape in the helper.
	viewerOwn := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}{}
	requestJSON(t, viewerClient, http.MethodGet, server.URL+"/v1/account", nil, http.StatusOK, &viewerOwn)
	viewerRegistration.Account.ID = viewerOwn.Account.ID
	viewerOrg.Organization.ID = ""
	// The organization id is read from the viewer's organizations collection.
	viewerOrganizations := struct {
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}{}
	requestJSON(t, viewerClient, http.MethodGet, server.URL+"/v1/organizations", nil, http.StatusOK, &viewerOrganizations)
	if len(viewerOrganizations.Organizations) != 1 {
		t.Fatalf("viewer organizations = %#v", viewerOrganizations.Organizations)
	}
	viewerOrg.Organization.ID = viewerOrganizations.Organizations[0].ID
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'viewer')`, ownerRegistration.Organization.ID, viewerRegistration.Account.ID); err != nil {
		t.Fatal(err)
	}

	databaseResponse := struct {
		Database struct {
			ID string `json:"id"`
		} `json:"database"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/databases", map[string]string{"name": "Primary data"}, http.StatusCreated, &databaseResponse)
	databaseURLPath := projectURL + "/databases/" + databaseResponse.Database.ID
	requestJSON(t, viewerClient, http.MethodGet, projectURL+"/databases", nil, http.StatusOK, &struct{}{})
	requestJSON(t, viewerClient, http.MethodPost, projectURL+"/databases", map[string]string{"name": "Viewer write"}, http.StatusForbidden, nil)

	tableResponse := struct {
		Table struct {
			ID string `json:"id"`
		} `json:"table"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, databaseURLPath+"/tables", map[string]any{"name": "Events", "row_security": true, "create_permissions": []string{}, "read_permissions": []string{}, "update_permissions": []string{}, "delete_permissions": []string{}}, http.StatusCreated, &tableResponse)
	tableURL := databaseURLPath + "/tables/" + tableResponse.Table.ID
	requestJSON(t, viewerClient, http.MethodGet, databaseURLPath+"/tables", nil, http.StatusOK, &struct{}{})
	requestJSON(t, viewerClient, http.MethodPost, databaseURLPath+"/tables", map[string]any{"name": "Nope"}, http.StatusForbidden, nil)

	columnResponse := struct {
		Column struct {
			ID string `json:"id"`
		} `json:"column"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/columns", map[string]any{"key": "title", "type": "text", "required": true}, http.StatusCreated, &columnResponse)
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/columns", map[string]any{"key": "count", "type": "integer", "required": true, "default": 2}, http.StatusCreated, &struct{}{})
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/indexes", map[string]any{"name": "title unique", "type": "unique", "column_keys": []string{"title"}, "directions": []string{"asc"}}, http.StatusCreated, &struct{}{})

	rowBody := map[string]any{"data": map[string]any{"title": "first"}}
	firstRow := struct {
		Row struct {
			ID   string         `json:"id"`
			Data map[string]any `json:"data"`
		} `json:"row"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/rows", rowBody, http.StatusCreated, &firstRow)
	if firstRow.Row.Data["count"] != float64(2) {
		t.Fatalf("default count = %#v", firstRow.Row.Data["count"])
	}
	duplicateBody := map[string]any{"data": map[string]any{"title": "first"}}
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/rows", duplicateBody, http.StatusConflict, nil)
	var exportedRows struct {
		Rows []struct {
			Data map[string]any `json:"data"`
		} `json:"rows"`
		Count int `json:"count"`
	}
	requestJSON(t, ownerClient, http.MethodGet, tableURL+"/export?format=json&limit=10", nil, http.StatusOK, &exportedRows)
	if exportedRows.Count != 1 || len(exportedRows.Rows) != 1 || exportedRows.Rows[0].Data["title"] != "first" {
		t.Fatalf("JSON export = %#v", exportedRows)
	}
	csvExport := requestJSONRaw(t, ownerClient, http.MethodGet, tableURL+"/export?format=csv&limit=10", nil, http.StatusOK)
	if !bytes.Contains(csvExport, []byte("id,project_id,table_id,created_at,updated_at,title,count")) || !bytes.Contains(csvExport, []byte("first")) {
		t.Fatalf("CSV export = %s", csvExport)
	}
	var importedRows struct {
		Rows []struct {
			Data map[string]any `json:"data"`
		} `json:"rows"`
		Count int `json:"count"`
	}
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/rows/import", map[string]any{"rows": []any{map[string]any{"data": map[string]any{"title": "imported"}}}}, http.StatusCreated, &importedRows)
	if importedRows.Count != 1 || len(importedRows.Rows) != 1 || importedRows.Rows[0].Data["title"] != "imported" {
		t.Fatalf("bulk import = %#v", importedRows)
	}
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/rows/import", map[string]any{"rows": []any{map[string]any{"data": map[string]any{"title": "atomic"}}, map[string]any{"data": map[string]any{"title": "imported"}}}}, http.StatusConflict, nil)
	requestJSON(t, ownerClient, http.MethodGet, tableURL+"/rows?search=first&search_column=title", nil, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/indexes", map[string]any{"name": "title fulltext", "type": "fulltext", "column_keys": []string{"title"}}, http.StatusCreated, &struct{}{})
	var searchPage struct {
		Rows []struct {
			Data map[string]any `json:"data"`
		} `json:"rows"`
	}
	requestJSON(t, ownerClient, http.MethodGet, tableURL+"/rows?search=first&search_column=title", nil, http.StatusOK, &searchPage)
	if len(searchPage.Rows) != 1 || searchPage.Rows[0].Data["title"] != "first" {
		t.Fatalf("full-text search = %#v", searchPage)
	}
	// Relationships are durable metadata, but they also enforce row writes.
	// A source text column stores the target row UUID; target deletion is
	// restricted until the source row is removed.
	relSourceTable := struct {
		Table struct {
			ID string `json:"id"`
		} `json:"table"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, databaseURLPath+"/tables", map[string]any{"name": "Event links", "row_security": true, "create_permissions": []string{}, "read_permissions": []string{}, "update_permissions": []string{}, "delete_permissions": []string{}}, http.StatusCreated, &relSourceTable)
	relSourceURL := databaseURLPath + "/tables/" + relSourceTable.Table.ID
	requestJSON(t, ownerClient, http.MethodPost, relSourceURL+"/columns", map[string]any{"key": "event_id", "type": "text"}, http.StatusCreated, &struct{}{})
	relationshipTarget := struct {
		Row struct {
			ID string `json:"id"`
		} `json:"row"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/rows", map[string]any{"data": map[string]any{"title": "relationship target"}}, http.StatusCreated, &relationshipTarget)
	relationship := struct {
		Relationship struct {
			ID string `json:"id"`
		} `json:"relationship"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, databaseURLPath+"/relationships", map[string]any{"source_table_id": relSourceTable.Table.ID, "source_column_key": "event_id", "target_table_id": tableResponse.Table.ID}, http.StatusCreated, &relationship)
	requestJSON(t, ownerClient, http.MethodGet, databaseURLPath+"/relationships/"+relationship.Relationship.ID, nil, http.StatusOK, &struct{}{})
	requestJSON(t, ownerClient, http.MethodPost, relSourceURL+"/rows", map[string]any{"data": map[string]any{"event_id": "not-a-row-id"}}, http.StatusUnprocessableEntity, nil)
	linkedRow := struct {
		Row struct {
			ID string `json:"id"`
		} `json:"row"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, relSourceURL+"/rows", map[string]any{"data": map[string]any{"event_id": relationshipTarget.Row.ID}}, http.StatusCreated, &linkedRow)
	requestJSON(t, ownerClient, http.MethodPatch, relSourceURL+"/rows/"+linkedRow.Row.ID, map[string]any{"data": map[string]any{"event_id": "not-a-row-id"}}, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodDelete, tableURL+"/rows/"+relationshipTarget.Row.ID, nil, http.StatusConflict, nil)
	requestJSON(t, ownerClient, http.MethodDelete, relSourceURL+"/rows/"+linkedRow.Row.ID, nil, http.StatusNoContent, nil)
	requestJSON(t, ownerClient, http.MethodDelete, tableURL+"/rows/"+relationshipTarget.Row.ID, nil, http.StatusNoContent, nil)
	transactionRowID := uuid.Must(uuid.NewV7()).String()
	var transactionResult struct {
		Rows []struct {
			ID string `json:"id"`
		} `json:"rows"`
		DeletedIDs []string `json:"deleted_ids"`
		Count      int      `json:"count"`
	}
	requestJSON(t, ownerClient, http.MethodPost, relSourceURL+"/rows/transaction", map[string]any{"operations": []any{
		map[string]any{"action": "create", "id": transactionRowID, "data": map[string]any{"event_id": firstRow.Row.ID}},
		map[string]any{"action": "update", "id": transactionRowID, "data": map[string]any{"event_id": firstRow.Row.ID}},
	}}, http.StatusOK, &transactionResult)
	if transactionResult.Count != 2 || len(transactionResult.Rows) != 2 {
		t.Fatalf("transaction result = %#v", transactionResult)
	}
	requestJSON(t, ownerClient, http.MethodPost, relSourceURL+"/rows/transaction", map[string]any{"operations": []any{
		map[string]any{"action": "create", "id": uuid.Must(uuid.NewV7()).String(), "data": map[string]any{"event_id": firstRow.Row.ID}},
		map[string]any{"action": "delete", "id": uuid.Must(uuid.NewV7()).String()},
	}}, http.StatusNotFound, nil)
	requestJSON(t, ownerClient, http.MethodGet, relSourceURL+"/rows/"+transactionRowID, nil, http.StatusOK, &struct{}{})
	requestJSON(t, ownerClient, http.MethodPost, relSourceURL+"/rows/transaction", map[string]any{"operations": []any{map[string]any{"action": "delete", "id": transactionRowID}}}, http.StatusOK, &transactionResult)
	if transactionResult.Count != 1 || len(transactionResult.DeletedIDs) != 1 {
		t.Fatalf("delete transaction result = %#v", transactionResult)
	}
	// Logical backups include schema, indexes, relationships, and rows. The
	// restore replaces the whole database under one transaction, so a table
	// created after the snapshot must disappear when restore succeeds.
	var backupResponse struct {
		Backup struct {
			ID        string `json:"id"`
			SizeBytes int64  `json:"size_bytes"`
			Checksum  string `json:"checksum_sha256"`
		} `json:"backup"`
	}
	requestJSON(t, ownerClient, http.MethodPost, databaseURLPath+"/backups?max_rows=1000", nil, http.StatusCreated, &backupResponse)
	if backupResponse.Backup.ID == "" || backupResponse.Backup.SizeBytes < 1 || len(backupResponse.Backup.Checksum) != 64 {
		t.Fatalf("invalid backup metadata = %#v", backupResponse.Backup)
	}
	backupPath := databaseURLPath + "/backups/" + backupResponse.Backup.ID
	backupPayload := requestJSONRaw(t, ownerClient, http.MethodGet, backupPath+"/download", nil, http.StatusOK)
	var backupSnapshot map[string]any
	if err := json.Unmarshal(backupPayload, &backupSnapshot); err != nil || backupSnapshot["version"] == nil {
		t.Fatalf("invalid downloaded backup: err=%v payload=%s", err, backupPayload)
	}
	requestJSON(t, ownerClient, http.MethodPost, databaseURLPath+"/tables", map[string]any{"name": "Restore me"}, http.StatusCreated, &struct{}{})
	var restoreResponse struct {
		Result struct {
			Tables        int `json:"tables"`
			Columns       int `json:"columns"`
			Indexes       int `json:"indexes"`
			Rows          int `json:"rows"`
			Relationships int `json:"relationships"`
		} `json:"result"`
	}
	requestJSON(t, ownerClient, http.MethodPost, backupPath+"/restore", nil, http.StatusOK, &restoreResponse)
	if restoreResponse.Result.Tables < 2 || restoreResponse.Result.Columns < 2 || restoreResponse.Result.Rows < 1 {
		t.Fatalf("unexpected restore result = %#v", restoreResponse.Result)
	}
	requestJSON(t, ownerClient, http.MethodGet, databaseURLPath+"/tables", nil, http.StatusOK, &struct{}{})
	requestJSON(t, ownerClient, http.MethodGet, databaseURLPath+"/tables/00000000-0000-7000-8000-000000000000", nil, http.StatusNotFound, nil)
	requestJSON(t, ownerClient, http.MethodDelete, backupPath, nil, http.StatusNoContent, nil)
	requestJSON(t, ownerClient, http.MethodGet, backupPath, nil, http.StatusNotFound, nil)
	requestJSON(t, ownerClient, http.MethodGet, tableURL+"/rows?filter.count=2", nil, http.StatusUnprocessableEntity, nil)
	requestJSON(t, ownerClient, http.MethodPost, tableURL+"/indexes", map[string]any{"name": "count key", "type": "key", "column_keys": []string{"count"}}, http.StatusCreated, &struct{}{})
	var rowsPage struct {
		Rows []struct {
			ID string `json:"id"`
		} `json:"rows"`
		Pagination struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"pagination"`
	}
	requestJSON(t, ownerClient, http.MethodGet, tableURL+"/rows?filter.count=2&limit=1", nil, http.StatusOK, &rowsPage)
	if len(rowsPage.Rows) != 1 {
		t.Fatalf("filtered rows = %#v", rowsPage.Rows)
	}
	requestJSON(t, ownerClient, http.MethodGet, tableURL+"/rows/"+firstRow.Row.ID, nil, http.StatusOK, &struct{}{})

	readKey := struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		Secret string `json:"secret"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "database read", "scopes": []string{"databases.read"}}, http.StatusCreated, &readKey)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/databases", nil, http.StatusOK, keyHeaders(readKey.Secret))
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, projectURL+"/databases", map[string]string{"name": "read cannot write"}, http.StatusForbidden, keyHeaders(readKey.Secret))
	writeKey := struct {
		Secret string `json:"secret"`
	}{}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "database write", "scopes": []string{"databases.write"}}, http.StatusCreated, &writeKey)
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodPost, projectURL+"/databases", map[string]string{"name": "write database"}, http.StatusCreated, keyHeaders(writeKey.Secret))
	requestJSONWithHeaders(t, newIntegrationClient(t), http.MethodGet, projectURL+"/databases", nil, http.StatusForbidden, keyHeaders(writeKey.Secret))

	// Public row grants: table read is empty, so an anonymous caller can only
	// see the row because its row grant explicitly contains any.
	requestJSON(t, ownerClient, http.MethodPatch, tableURL, map[string]any{"row_security": true, "create_permissions": []string{"any"}, "read_permissions": []string{}, "update_permissions": []string{}, "delete_permissions": []string{}}, http.StatusOK, &struct{}{})
	anonymousClient := newIntegrationClient(t)
	publicRow := struct {
		Row struct {
			ID string `json:"id"`
		} `json:"row"`
	}{}
	requestJSON(t, anonymousClient, http.MethodPost, tableURL+"/rows", map[string]any{"data": map[string]any{"title": "public"}, "read_permissions": []string{"any"}, "update_permissions": []string{"any"}, "delete_permissions": []string{"any"}}, http.StatusCreated, &publicRow)
	requestJSON(t, anonymousClient, http.MethodGet, tableURL+"/rows/"+publicRow.Row.ID, nil, http.StatusOK, &struct{}{})
	requestJSON(t, anonymousClient, http.MethodPatch, tableURL+"/rows/"+publicRow.Row.ID, map[string]any{"data": map[string]any{"title": "public-updated"}}, http.StatusOK, &struct{}{})

	// Registration creates an application cookie. The user grant is useful on
	// a table with an empty table-read grant, and the cookie takes precedence
	// over the Console cookie when both are attached to a request.
	requestJSON(t, ownerClient, http.MethodPatch, projectURL+"/auth/settings", map[string]any{"registration_enabled": true}, http.StatusOK, &struct{}{})
	appClient := newIntegrationClient(t)
	appRegistration := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}{}
	requestJSON(t, appClient, http.MethodPost, projectURL+"/account/registrations", map[string]string{"email": "database-app-" + ownerID.String() + "@example.test", "password": "application-password-1"}, http.StatusCreated, &appRegistration)
	requestJSON(t, appClient, http.MethodPost, tableURL+"/rows", map[string]any{"data": map[string]any{"title": "private"}}, http.StatusCreated, &struct{}{})
	requestJSON(t, anonymousClient, http.MethodGet, tableURL+"/rows?limit=100", nil, http.StatusOK, &struct{}{})

	// Disable row security: the table's users grant is sufficient even when a
	// row has no read grant.
	requestJSON(t, ownerClient, http.MethodPatch, tableURL, map[string]any{"row_security": false, "create_permissions": []string{"any"}, "read_permissions": []string{"users"}, "update_permissions": []string{"users"}, "delete_permissions": []string{"users"}}, http.StatusOK, &struct{}{})
	requestJSON(t, appClient, http.MethodGet, tableURL+"/rows", nil, http.StatusOK, &struct{}{})

	// Blocking the application user revokes its session and prevents further
	// data access; the Console user remains isolated from app credentials.
	requestJSON(t, ownerClient, http.MethodPatch, projectURL+"/users/"+appRegistration.Account.ID+"/status", map[string]string{"status": "blocked"}, http.StatusOK, &struct{}{})
	requestJSON(t, appClient, http.MethodGet, tableURL+"/rows", nil, http.StatusUnauthorized, nil)
	var auditMetadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE action='database_row.create' AND target_id=$1 ORDER BY created_at DESC LIMIT 1`, firstRow.Row.ID).Scan(&auditMetadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditMetadata, "first") {
		t.Fatalf("row value leaked into audit metadata: %s", auditMetadata)
	}

	// An account in another organization cannot discover this project's data.
	outsider := newIntegrationClient(t)
	outsiderRegistration := struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}{}
	requestJSON(t, outsider, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{"email": "database-outsider-" + ownerID.String() + "@example.test", "password": "correct-horse-battery-staple"}, http.StatusCreated, &outsiderRegistration)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, outsiderRegistration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, outsiderRegistration.Account.ID)
	})
	requestJSON(t, outsider, http.MethodGet, tableURL+"/rows", nil, http.StatusNotFound, nil)
}
