package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlatformSiteAllocationAndRouteSnapshotIntegration(t *testing.T) {
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
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	testStartedAt := time.Now().UTC()

	accountID := uuid.Must(uuid.NewV7())
	organizationID := uuid.Must(uuid.NewV7())
	organizationTwoID := uuid.Must(uuid.NewV7())
	projectOneID := uuid.Must(uuid.NewV7())
	projectTwoID := uuid.Must(uuid.NewV7())
	projectThreeID := uuid.Must(uuid.NewV7())
	organizationSlug := "platform-sites-" + strings.ToLower(accountID.String()[:8])
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,'test-hash')`, accountID, fmt.Sprintf("platform-sites-%s@example.test", accountID.String())); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)`, organizationID, "Platform Sites Test", organizationSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)`, organizationTwoID, "Platform Sites Test Two", organizationSlug+"-two"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner'),($3,$2,'owner')`, organizationID, accountID, organizationTwoID); err != nil {
		t.Fatal(err)
	}
	for _, project := range []struct {
		id             uuid.UUID
		name           string
		organizationID uuid.UUID
	}{
		{id: projectOneID, name: "platform-one", organizationID: organizationID},
		{id: projectTwoID, name: "platform-two", organizationID: organizationID},
		{id: projectThreeID, name: "platform-three", organizationID: organizationTwoID},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,$3)`, project.id, project.organizationID, project.name); err != nil {
			t.Fatal(err)
		}
	}
	var ownerID uuid.UUID
	var previousWorkloadDomain *string
	if err := pool.QueryRow(ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&previousWorkloadDomain); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		restoredDomain := any(nil)
		if previousWorkloadDomain != nil {
			restoredDomain = *previousWorkloadDomain
		}
		_, _ = pool.Exec(cleanupCtx, `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, restoredDomain)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id=$1 AND action='admin.domain_settings.update' AND target_type='instance_domain_settings' AND created_at >= $2`, ownerID, testStartedAt)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id IN ($1,$2)`, organizationID, organizationTwoID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, accountID)
	})

	repo := New(pool)
	first, err := repo.CreateSite(ctx, uuid.Must(uuid.NewV7()), projectOneID, SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SiteInput{Name: "portfolio", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateSite(ctx, uuid.Must(uuid.NewV7()), projectTwoID, SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SiteInput{Name: "portfolio", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	third, err := repo.CreateSite(ctx, uuid.Must(uuid.NewV7()), projectThreeID, SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SiteInput{Name: "portfolio", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if first.PlatformHostname != nil || second.PlatformHostname != nil || third.PlatformHostname != nil {
		t.Fatal("platform hostname was exposed before workload domain configuration")
	}
	var labels []string
	rows, err := pool.Query(ctx, `SELECT platform_label FROM project_sites WHERE id IN ($1,$2,$3) ORDER BY platform_label`, first.ID, second.ID, third.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		labels = append(labels, label)
	}
	rows.Close()
	if len(labels) != 3 || labels[0] != "portfolio" || labels[1] == labels[2] {
		t.Fatalf("global labels = %#v, want portfolio plus deterministic cross-project/cross-organization collisions", labels)
	}

	reserved, err := repo.CreateSite(ctx, uuid.Must(uuid.NewV7()), projectOneID, SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SiteInput{Name: "api", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var reservedLabel string
	if err := pool.QueryRow(ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, reserved.ID).Scan(&reservedLabel); err != nil {
		t.Fatal(err)
	}
	if reservedLabel == "api" || len(reservedLabel) > 63 {
		t.Fatalf("reserved Site label = %q", reservedLabel)
	}
	longName := strings.Repeat("a", 63)
	longSite, err := repo.CreateSite(ctx, uuid.Must(uuid.NewV7()), projectOneID, SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SiteInput{Name: longName, Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var longLabel string
	if err := pool.QueryRow(ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, longSite.ID).Scan(&longLabel); err != nil {
		t.Fatal(err)
	}
	if len(longLabel) > 63 || longLabel == "" {
		t.Fatalf("maximum-length Site label = %q", longLabel)
	}

	ownerID, err = instanceOwnerID(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", stringPtr("apps.example.com")); err != nil {
		t.Fatal(err)
	}
	first, err = repo.GetSite(ctx, projectOneID, mustPlatformUUID(first.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID})
	if err != nil {
		t.Fatal(err)
	}
	if first.PlatformHostname == nil || *first.PlatformHostname != "portfolio.apps.example.com" {
		t.Fatalf("derived platform hostname = %v", first.PlatformHostname)
	}
	routes, err := repo.ListPlatformRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 5 {
		t.Fatalf("desired platform route count = %d, want 5", len(routes))
	}
	oldHostname := *first.PlatformHostname
	newName := "renamed-portfolio"
	first, err = repo.UpdateSite(ctx, projectOneID, mustPlatformUUID(first.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SitePatch{Name: &newName})
	if err != nil {
		t.Fatal(err)
	}
	if first.PlatformHostname == nil || *first.PlatformHostname != oldHostname {
		t.Fatalf("Site rename changed platform hostname to %v", first.PlatformHostname)
	}
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", stringPtr("deploy.example.net")); err != nil {
		t.Fatal(err)
	}
	first, err = repo.GetSite(ctx, projectOneID, mustPlatformUUID(first.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID})
	if err != nil {
		t.Fatal(err)
	}
	if first.PlatformHostname == nil || *first.PlatformHostname != "portfolio.deploy.example.net" {
		t.Fatalf("workload-domain change altered stable platform label: %v", first.PlatformHostname)
	}
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", stringPtr("apps.example.com")); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.CreateSiteDomain(ctx, uuid.Must(uuid.NewV7()), projectOneID, mustPlatformUUID(first.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SiteDomainInput{Hostname: "foo.apps.example.com"}); err != ErrSiteDomainPlatformConflict {
		t.Fatalf("platform custom-domain collision error = %v, want %v", err, ErrSiteDomainPlatformConflict)
	}
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", nil); err != nil {
		t.Fatal(err)
	}
	domain, err := repo.CreateSiteDomain(ctx, uuid.Must(uuid.NewV7()), projectOneID, mustPlatformUUID(first.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SiteDomainInput{Hostname: "foo.apps.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", stringPtr("apps.example.com")); err != ErrInstanceDomainConflict {
		t.Fatalf("existing custom-domain conflict error = %v, want %v", err, ErrInstanceDomainConflict)
	}
	if err := repo.DeleteSiteDomain(ctx, projectOneID, mustPlatformUUID(first.ID), mustPlatformUUID(domain.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", stringPtr("apps.example.com")); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.UpdateSite(ctx, projectTwoID, mustPlatformUUID(second.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SitePatch{Enabled: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	routes, err = repo.ListPlatformRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 4 {
		t.Fatalf("disabled Site route count = %d, want 4", len(routes))
	}
	if _, err := repo.UpdateSite(ctx, projectTwoID, mustPlatformUUID(second.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SitePatch{Enabled: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateInstanceDomainSettings(ctx, ownerID, "cloud.example.com", nil); err != nil {
		t.Fatal(err)
	}
	first, err = repo.GetSite(ctx, projectOneID, mustPlatformUUID(first.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID})
	if err != nil {
		t.Fatal(err)
	}
	if first.PlatformHostname != nil {
		t.Fatalf("cleared workload domain still exposed %q", *first.PlatformHostname)
	}

	// The unique index is authoritative even if a caller bypasses the
	// allocator, while the application allocator remains deterministic.
	if _, err := pool.Exec(ctx, `UPDATE project_sites SET platform_label=$2 WHERE id=$1`, reserved.ID, labels[0]); err == nil {
		t.Fatal("database accepted a duplicate global platform label")
	}
	if _, err := repo.DeleteSite(ctx, projectTwoID, mustPlatformUUID(second.ID), SiteActor{Kind: SiteConsoleActor, AccountID: accountID}); err != nil {
		t.Fatal(err)
	}
	routes, err = repo.ListPlatformRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 4 {
		t.Fatalf("deleted Site route count = %d, want 4", len(routes))
	}

	var concurrent sync.WaitGroup
	concurrent.Add(2)
	results := make(chan error, 2)
	for _, projectID := range []uuid.UUID{projectOneID, projectTwoID} {
		go func(projectID uuid.UUID) {
			defer concurrent.Done()
			_, createErr := repo.CreateSite(context.Background(), uuid.Must(uuid.NewV7()), projectID, SiteActor{Kind: SiteConsoleActor, AccountID: accountID}, SiteInput{Name: "concurrent", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20})
			results <- createErr
		}(projectID)
	}
	concurrent.Wait()
	close(results)
	for createErr := range results {
		if createErr != nil {
			t.Fatal(createErr)
		}
	}
	var concurrentLabels int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT platform_label) FROM project_sites WHERE name='concurrent'`).Scan(&concurrentLabels); err != nil {
		t.Fatal(err)
	}
	if concurrentLabels != 2 {
		t.Fatalf("concurrent allocation labels = %d, want 2", concurrentLabels)
	}
}

func instanceOwnerID(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `SELECT account_id FROM instance_roles WHERE role='instance_owner' ORDER BY account_id LIMIT 1`).Scan(&id)
	return id, err
}

func mustPlatformUUID(value string) uuid.UUID {
	return uuid.MustParse(value)
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}
