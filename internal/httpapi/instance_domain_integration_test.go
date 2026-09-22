package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/httpapi"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	instanceDomainAuditAction     = "admin.domain_settings.update"
	instanceDomainAuditTargetType = "instance_domain_settings"
)

func TestInstanceDomainSettingsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run instance domain integration tests")
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
	testStartedAt := time.Now().UTC()

	var ownerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT account_id FROM instance_roles WHERE role='instance_owner'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE instance_domain_settings SET workload_base_domain=NULL,updated_at=now() WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}

	ownerToken, ownerTokenHash, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	ownerSessionID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO sessions (id,account_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, ownerSessionID, ownerID, ownerTokenHash, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httpapi.New(config.Config{
		PublicAppURL:       "https://Cloud.Example.com/console",
		SessionCookieName:  "stealth_session",
		SessionTTL:         time.Hour,
		StorageRoot:        t.TempDir(),
		StorageMaxFileSize: 1 << 20,
		FunctionsSecretKey: bytes.Repeat([]byte("k"), 32),
	}, repository.New(pool), logger)
	testServer := httptest.NewServer(server)
	t.Cleanup(testServer.Close)

	ownerClient := newIntegrationClient(t)
	serverURL, err := url.Parse(testServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	ownerClient.Jar.SetCookies(serverURL, []*http.Cookie{{Name: "stealth_session", Value: ownerToken, Path: "/"}})

	adminClient := newIntegrationClient(t)
	admin := registerAdminTestAccount(t, adminClient, testServer.URL, "domain-admin")
	if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_admin')`, admin.accountID); err != nil {
		t.Fatal(err)
	}
	organizationOwnerClient := newIntegrationClient(t)
	organizationOwner := registerAdminTestAccount(t, organizationOwnerClient, testServer.URL, "domain-org-owner")
	regularClient := newIntegrationClient(t)
	regular := registerAdminTestAccount(t, regularClient, testServer.URL, "domain-regular")

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `UPDATE instance_roles SET role='instance_owner' WHERE account_id=$1`, ownerID)
		_, _ = pool.Exec(cleanupCtx, `UPDATE instance_domain_settings SET workload_base_domain=NULL,updated_at=now() WHERE id=TRUE`)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM sessions WHERE id=$1`, ownerSessionID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_realtime_events WHERE event_name=$1 AND target_type=$2 AND occurred_at >= $3`, instanceDomainAuditAction, instanceDomainAuditTargetType, testStartedAt)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id=$1 AND action=$2 AND target_type=$3 AND created_at >= $4`, ownerID, instanceDomainAuditAction, instanceDomainAuditTargetType, testStartedAt)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id IN ($1,$2,$3) OR organization_id IN ($4,$5,$6)`, admin.accountID, organizationOwner.accountID, regular.accountID, admin.organizationID, organizationOwner.organizationID, regular.organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM instance_roles WHERE account_id=$1`, admin.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id IN ($1,$2,$3)`, admin.organizationID, organizationOwner.organizationID, regular.organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id IN ($1,$2,$3)`, admin.accountID, organizationOwner.accountID, regular.accountID)
	})

	settingsURL := testServer.URL + "/v1/admin/domain-settings"
	var settings instanceDomainSettingsResponse
	requestJSON(t, ownerClient, http.MethodGet, settingsURL, nil, http.StatusOK, &settings)
	if settings.InstanceHostname != "cloud.example.com" || settings.WorkloadBaseDomain != nil {
		t.Fatalf("initial settings = %+v, want canonical Console hostname and unset workload domain", settings)
	}
	requestJSON(t, adminClient, http.MethodGet, settingsURL, nil, http.StatusOK, &settings)
	requestJSON(t, organizationOwnerClient, http.MethodGet, settingsURL, nil, http.StatusForbidden, nil)
	requestJSON(t, regularClient, http.MethodGet, settingsURL, nil, http.StatusForbidden, nil)
	requestJSON(t, newIntegrationClient(t), http.MethodGet, settingsURL, nil, http.StatusUnauthorized, nil)

	ownerAuditEvents := queryInstanceDomainAuditEvents(t, ctx, pool, ownerID)
	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"例え.テスト"}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "xn--r8jz45g.xn--zckzah")
	assertStoredWorkloadDomain(t, ctx, pool, "xn--r8jz45g.xn--zckzah")
	ownerAuditEvents = assertNewInstanceDomainAuditEvent(t, ctx, pool, ownerID, ownerAuditEvents, nil, stringPointer("xn--r8jz45g.xn--zckzah"), false)

	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":" Apps.Example.COM. "}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "apps.example.com")
	assertStoredWorkloadDomain(t, ctx, pool, "apps.example.com")
	ownerAuditEvents = assertNewInstanceDomainAuditEvent(t, ctx, pool, ownerID, ownerAuditEvents, stringPointer("xn--r8jz45g.xn--zckzah"), stringPointer("apps.example.com"), false)

	adminAuditEvents := queryInstanceDomainAuditEvents(t, ctx, pool, uuid.MustParse(admin.accountID))
	for _, client := range []*http.Client{adminClient, organizationOwnerClient, regularClient} {
		requestRawJSON(t, client, http.MethodPatch, settingsURL, `{"workload_base_domain":"blocked.example.com"}`, http.StatusForbidden, nil)
	}
	if got := queryInstanceDomainAuditEvents(t, ctx, pool, uuid.MustParse(admin.accountID)); len(got) != len(adminAuditEvents) {
		t.Fatalf("instance admin domain audit events = %d, want %d", len(got), len(adminAuditEvents))
	}
	requestRawJSON(t, newIntegrationClient(t), http.MethodPatch, settingsURL, `{"workload_base_domain":"blocked.example.com"}`, http.StatusUnauthorized, nil)

	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"deploy.example.co.uk"}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "deploy.example.co.uk")
	assertStoredWorkloadDomain(t, ctx, pool, "deploy.example.co.uk")
	ownerAuditEvents = assertNewInstanceDomainAuditEvent(t, ctx, pool, ownerID, ownerAuditEvents, stringPointer("apps.example.com"), stringPointer("deploy.example.co.uk"), false)

	ownerAuditEventsBeforeInvalid := ownerAuditEvents
	for _, value := range []string{
		"127.0.0.1",
		"https://apps.example.com",
		"apps.example.com:443",
		"co.uk",
		"cloud.example.com",
		"example.com",
		"xn--.com",
		"apps.example..com",
	} {
		requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"`+value+`"}`, http.StatusUnprocessableEntity, nil)
	}
	if got := queryInstanceDomainAuditEvents(t, ctx, pool, ownerID); len(got) != len(ownerAuditEventsBeforeInvalid) {
		t.Fatalf("invalid updates changed instance domain audit count from %d to %d", len(ownerAuditEventsBeforeInvalid), len(got))
	}
	assertStoredWorkloadDomain(t, ctx, pool, "deploy.example.co.uk")

	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{}`, http.StatusBadRequest, nil)
	ownerAuditEventsBeforeClear := ownerAuditEvents
	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":null}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "")
	assertStoredWorkloadDomain(t, ctx, pool, "")
	ownerAuditEvents = assertNewInstanceDomainAuditEvent(t, ctx, pool, ownerID, ownerAuditEvents, stringPointer("deploy.example.co.uk"), nil, true)
	if len(ownerAuditEvents) != len(ownerAuditEventsBeforeClear)+1 {
		t.Fatalf("clear audit event count = %d, want %d", len(ownerAuditEvents), len(ownerAuditEventsBeforeClear)+1)
	}

	if _, err := pool.Exec(ctx, `UPDATE instance_roles SET role='instance_admin' WHERE account_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"apps.example.com"}`, http.StatusForbidden, nil)
	if _, err := pool.Exec(ctx, `UPDATE instance_roles SET role='instance_owner' WHERE account_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	requestRawJSON(t, ownerClient, http.MethodPatch, settingsURL, `{"workload_base_domain":"apps.example.com"}`, http.StatusOK, &settings)
	assertDomainSettings(t, settings, "cloud.example.com", "apps.example.com")
}

type instanceDomainAuditEvent struct {
	ID           uuid.UUID
	ActorAccount uuid.UUID
	Action       string
	TargetType   string
	TargetID     *uuid.UUID
	Metadata     json.RawMessage
}

type instanceDomainAuditMetadata struct {
	PreviousWorkloadBaseDomain *string `json:"previous_workload_base_domain"`
	WorkloadBaseDomain         *string `json:"workload_base_domain"`
	Cleared                    bool    `json:"cleared"`
}

func queryInstanceDomainAuditEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, actorID uuid.UUID) []instanceDomainAuditEvent {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT id,actor_account_id,action,target_type,target_id,metadata
		FROM audit_events
		WHERE actor_account_id=$1 AND action=$2 AND target_type=$3
		ORDER BY created_at,id`, actorID, instanceDomainAuditAction, instanceDomainAuditTargetType)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	events := make([]instanceDomainAuditEvent, 0)
	for rows.Next() {
		var event instanceDomainAuditEvent
		var metadata []byte
		if err := rows.Scan(&event.ID, &event.ActorAccount, &event.Action, &event.TargetType, &event.TargetID, &metadata); err != nil {
			t.Fatal(err)
		}
		event.Metadata = json.RawMessage(metadata)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func assertNewInstanceDomainAuditEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, actorID uuid.UUID, previous []instanceDomainAuditEvent, wantPrevious, wantWorkload *string, wantCleared bool) []instanceDomainAuditEvent {
	t.Helper()
	events := queryInstanceDomainAuditEvents(t, ctx, pool, actorID)
	if len(events) != len(previous)+1 {
		t.Fatalf("instance domain audit events = %d, want %d", len(events), len(previous)+1)
	}
	event := events[len(events)-1]
	if event.ActorAccount != actorID {
		t.Fatalf("audit actor = %s, want %s", event.ActorAccount, actorID)
	}
	if event.Action != instanceDomainAuditAction {
		t.Fatalf("audit action = %q, want %q", event.Action, instanceDomainAuditAction)
	}
	if event.TargetType != instanceDomainAuditTargetType {
		t.Fatalf("audit target type = %q, want %q", event.TargetType, instanceDomainAuditTargetType)
	}
	if event.TargetID == nil || *event.TargetID != uuid.Nil {
		t.Fatalf("audit target id = %v, want uuid.Nil", event.TargetID)
	}
	var metadata instanceDomainAuditMetadata
	if err := json.Unmarshal(event.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if !sameOptionalString(metadata.PreviousWorkloadBaseDomain, wantPrevious) || !sameOptionalString(metadata.WorkloadBaseDomain, wantWorkload) || metadata.Cleared != wantCleared {
		t.Fatalf("audit metadata = %s, want previous=%v workload=%v cleared=%v", event.Metadata, wantPrevious, wantWorkload, wantCleared)
	}
	return events
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func stringPointer(value string) *string {
	return &value
}

type instanceDomainSettingsResponse struct {
	InstanceHostname   string  `json:"instance_hostname"`
	WorkloadBaseDomain *string `json:"workload_base_domain"`
}

func assertDomainSettings(t *testing.T, settings instanceDomainSettingsResponse, instanceHostname, workloadBaseDomain string) {
	t.Helper()
	if settings.InstanceHostname != instanceHostname {
		t.Fatalf("instance hostname = %q, want %q", settings.InstanceHostname, instanceHostname)
	}
	if workloadBaseDomain == "" {
		if settings.WorkloadBaseDomain != nil {
			t.Fatalf("workload base domain = %q, want null", *settings.WorkloadBaseDomain)
		}
		return
	}
	if settings.WorkloadBaseDomain == nil || *settings.WorkloadBaseDomain != workloadBaseDomain {
		t.Fatalf("workload base domain = %v, want %q", settings.WorkloadBaseDomain, workloadBaseDomain)
	}
}

func assertStoredWorkloadDomain(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want string) {
	t.Helper()
	var stored *string
	if err := pool.QueryRow(ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if want == "" {
		if stored != nil {
			t.Fatalf("stored workload base domain = %q, want null", *stored)
		}
		return
	}
	if stored == nil || *stored != want {
		t.Fatalf("stored workload base domain = %v, want %q", stored, want)
	}
}

func requestRawJSON(t *testing.T, client *http.Client, method, endpoint, body string, expectedStatus int, target any) {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		contents, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s: expected %d, got %d: %s", method, endpoint, expectedStatus, response.StatusCode, contents)
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
}
