package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/platformhostname"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type appRepositoryFixture struct {
	ctx            context.Context
	pool           *pgxpool.Pool
	repo           *Repository
	accountID      uuid.UUID
	organizationID uuid.UUID
	projectOneID   uuid.UUID
	projectTwoID   uuid.UUID
	projectOneName string
	projectTwoName string
	actor          AppActor
}

func newAppRepositoryFixture(t *testing.T) appRepositoryFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID := uuid.Must(uuid.NewV7())
	organizationID := uuid.Must(uuid.NewV7())
	projectOneID := uuid.Must(uuid.NewV7())
	projectTwoID := uuid.Must(uuid.NewV7())
	suffix := strings.ToLower(accountID.String()[:8])
	projectOneName := "apps-primary-" + suffix
	projectTwoName := "apps-secondary-" + suffix
	organizationSlug := "apps-repository-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,'test-hash')`, accountID, fmt.Sprintf("apps-%s@example.test", accountID)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'Apps repository integration',$2)`, organizationID, organizationSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner')`, organizationID, accountID); err != nil {
		t.Fatal(err)
	}
	for _, project := range []struct {
		id   uuid.UUID
		name string
	}{
		{id: projectOneID, name: projectOneName},
		{id: projectTwoID, name: projectTwoName},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,$3)`, project.id, organizationID, project.name); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1 OR actor_account_id=$2`, organizationID, accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, organizationID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, accountID)
	})
	return appRepositoryFixture{
		ctx:            ctx,
		pool:           pool,
		repo:           New(pool),
		accountID:      accountID,
		organizationID: organizationID,
		projectOneID:   projectOneID,
		projectTwoID:   projectTwoID,
		projectOneName: projectOneName,
		projectTwoName: projectTwoName,
		actor:          AppActor{Kind: AppConsoleActor, AccountID: accountID},
	}
}

func TestAppsPersistenceGenerationAndPlanLimitIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)

	workload := workloadspec.Default()
	workingDirectory := "/srv/app"
	workload.WorkingDirectory = &workingDirectory
	firstID := uuid.Must(uuid.NewV7())
	first, err := f.repo.CreateApp(f.ctx, firstID, f.projectOneID, f.actor, AppInput{
		Name: "backend", Enabled: true, Workload: workload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.DesiredGeneration != 1 || first.ObservedGeneration != 0 || first.RuntimeStatus != "not_deployed" || first.RuntimeError != nil {
		t.Fatalf("initial App runtime state = %+v", first)
	}
	wantDigest, err := workloadspec.Digest(workload)
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkloadSpecSHA256 != wantDigest {
		t.Fatalf("initial spec digest = %q, want %q", first.WorkloadSpecSHA256, wantDigest)
	}
	var internalFields struct {
		PlatformLabel *string `json:"platform_label"`
	}
	encoded, err := jsonMarshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshal(encoded, &internalFields); err != nil {
		t.Fatal(err)
	}
	if internalFields.PlatformLabel != nil {
		t.Fatal("public App DTO exposed the persisted platform label")
	}

	newName := "backend-service"
	renamed, err := f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Name: &newName})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.DesiredGeneration != 1 || renamed.WorkloadSpecSHA256 != first.WorkloadSpecSHA256 {
		t.Fatalf("rename changed runtime generation or digest: %+v", renamed)
	}
	equivalent, err := workloadspec.Decode([]byte(`{"working_directory":"/srv//app/../app"}`))
	if err != nil {
		t.Fatal(err)
	}
	noOp, err := f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Workload: &equivalent})
	if err != nil {
		t.Fatal(err)
	}
	if noOp.DesiredGeneration != 1 || noOp.WorkloadSpecSHA256 != first.WorkloadSpecSHA256 || !workloadspec.Equal(noOp.Workload, first.Workload) {
		t.Fatalf("semantically equivalent spec created runtime work: %+v", noOp)
	}
	if _, err := f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Workload: &equivalent}); err != nil {
		t.Fatal(err)
	}
	var auditCount int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action='app.update' AND target_id=$2`, f.organizationID, firstID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("no-op workload patch emitted audit event; app.update count=%d, want rename only", auditCount)
	}
	var specLeaked bool
	if err := f.pool.QueryRow(f.ctx, `SELECT metadata ? 'workload' OR metadata ? 'workload_spec' FROM audit_events WHERE organization_id=$1 AND action='app.create' AND target_id=$2`, f.organizationID, firstID).Scan(&specLeaked); err != nil {
		t.Fatal(err)
	}
	if specLeaked {
		t.Fatal("App audit metadata included WorkloadSpec")
	}

	disabled := false
	updated, err := f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredGeneration != 2 || updated.ObservedGeneration != 0 || updated.RuntimeStatus != "not_deployed" {
		t.Fatalf("enabled-state change violated generation truth: %+v", updated)
	}
	portChange := workloadspec.Default()
	portChange.Port = 9090
	updated, err = f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Workload: &portChange})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredGeneration != 3 || updated.ObservedGeneration != 0 || updated.WorkloadSpecSHA256 == first.WorkloadSpecSHA256 {
		t.Fatalf("workload change violated generation or digest semantics: %+v", updated)
	}
	updated, err = f.repo.UpdateApp(f.ctx, f.projectOneID, firstID, f.actor, AppPatch{Workload: &portChange})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredGeneration != 3 {
		t.Fatalf("repeated identical workload incremented generation to %d", updated.DesiredGeneration)
	}

	second, err := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectTwoID, f.actor, AppInput{Name: "frontend", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.Workload.Port != workloadspec.DefaultPort || second.Workload.HealthCheck.Protocol != "tcp" {
		t.Fatalf("repository did not persist shared WorkloadSpec defaults: %+v", second.Workload)
	}

	type createResult struct {
		item domain.App
		err  error
	}
	start := make(chan struct{})
	results := make(chan createResult, 2)
	var writers sync.WaitGroup
	for _, name := range []string{"worker-one", "worker-two"} {
		writers.Add(1)
		go func(name string) {
			defer writers.Done()
			<-start
			item, createErr := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, f.actor, AppInput{Name: name, Enabled: true})
			results <- createResult{item: item, err: createErr}
		}(name)
	}
	close(start)
	writers.Wait()
	close(results)
	succeeded, limitErrors := 0, 0
	for result := range results {
		if result.err == nil {
			succeeded++
		} else if errors.Is(result.err, ErrPlanLimitExceeded) {
			limitErrors++
		} else {
			t.Fatalf("concurrent App create error = %v", result.err)
		}
	}
	if succeeded != 1 || limitErrors != 1 {
		t.Fatalf("concurrent final-slot results: succeeded=%d plan-limited=%d, want 1 each", succeeded, limitErrors)
	}
	if _, err := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, f.actor, AppInput{Name: "fourth-app", Enabled: true}); !errors.Is(err, ErrPlanLimitExceeded) {
		t.Fatalf("free plan fourth App error = %v, want plan limit", err)
	}

	plan, err := f.repo.OrganizationPlan(f.ctx, f.organizationID, f.accountID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Limits.Apps != 3 || plan.Usage.Apps != 3 {
		t.Fatalf("free plan Apps projection = limit %d, usage %d; want 3/3", plan.Limits.Apps, plan.Usage.Apps)
	}
	page, next, _, err := f.repo.ListApps(f.ctx, f.projectOneID, f.actor, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || next == "" {
		t.Fatalf("App first page = %d items, cursor %q; want one item and next cursor", len(page), next)
	}
	cursor := uuid.MustParse(next)
	secondPage, _, _, err := f.repo.ListApps(f.ctx, f.projectOneID, f.actor, 1, &cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage) != 1 || secondPage[0].ID == page[0].ID {
		t.Fatalf("App pagination second page = %+v", secondPage)
	}

	readKeyID := uuid.Must(uuid.NewV7())
	writeKeyID := uuid.Must(uuid.NewV7())
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO project_api_keys (id,project_id,name,prefix,secret_hash,scopes) VALUES ($1,$2,'Apps read','stl_key_readapps',$3,$4),($5,$2,'Apps write','stl_key_writeapp',$3,$6)`, readKeyID, f.projectOneID, bytesOfZeroes(32), []string{"apps.read"}, writeKeyID, []string{"apps.write"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.repo.ListApps(f.ctx, f.projectOneID, AppActor{Kind: AppAPIKeyActor, APIKeyID: readKeyID, APIKeyScopes: []string{"apps.read"}}, 10, nil); err != nil {
		t.Fatalf("apps.read API key could not list Apps: %v", err)
	}
	if _, err := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, AppActor{Kind: AppAPIKeyActor, APIKeyID: readKeyID, APIKeyScopes: []string{"apps.read"}}, AppInput{Name: "read-only", Enabled: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("apps.read API key write error = %v, want forbidden", err)
	}
	writeActor := AppActor{Kind: AppAPIKeyActor, APIKeyID: writeKeyID, APIKeyScopes: []string{"apps.write"}}
	if _, err := f.repo.CreateApp(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, writeActor, AppInput{Name: "write-scope-limited", Enabled: true}); !errors.Is(err, ErrPlanLimitExceeded) {
		t.Fatalf("apps.write API key was not authorized before plan enforcement: %v", err)
	}
	if _, err := f.repo.GetApp(f.ctx, f.projectOneID, firstID, AppActor{Kind: AppConsoleActor, AccountID: uuid.Must(uuid.NewV7())}); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-account App read error = %v, want hidden denial", err)
	}
}

func TestAppsSharedHostnameNamespaceConcurrencyAndSiteRouteIsolationIntegration(t *testing.T) {
	f := newAppRepositoryFixture(t)
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organization_plans (organization_id,plan_key) VALUES ($1,'enterprise') ON CONFLICT (organization_id) DO UPDATE SET plan_key='enterprise'`, f.organizationID); err != nil {
		t.Fatal(err)
	}

	var previousWorkloadDomain *string
	if err := f.pool.QueryRow(f.ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&previousWorkloadDomain); err != nil {
		t.Fatal(err)
	}
	workloadDomain := "apps.example.com"
	if _, err := f.pool.Exec(f.ctx, `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, workloadDomain); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		var restore any
		if previousWorkloadDomain != nil {
			restore = *previousWorkloadDomain
		}
		_, _ = f.pool.Exec(cleanupCtx, `UPDATE instance_domain_settings SET workload_base_domain=$1,updated_at=now() WHERE id=TRUE`, restore)
	})

	siteActor := SiteActor{Kind: SiteConsoleActor, AccountID: f.accountID}
	site, err := f.repo.CreateSite(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, siteActor, SiteInput{
		Name: "backend", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	siteRoutesBefore, err := f.repo.ListPlatformRoutes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	appID := uuid.Must(uuid.NewV7())
	app, err := f.repo.CreateApp(f.ctx, appID, f.projectOneID, f.actor, AppInput{Name: "backend", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if app.PlatformHostname == nil || *app.PlatformHostname == "backend.apps.example.com" {
		t.Fatalf("Site/App same-name claim did not allocate unique reserved App hostname: %v", app.PlatformHostname)
	}
	siteRoutesAfter, err := f.repo.ListPlatformRoutes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(siteRoutesAfter, siteRoutesBefore) {
		t.Fatalf("creating App changed static Site route snapshot: before=%+v after=%+v", siteRoutesBefore, siteRoutesAfter)
	}
	if site.PlatformHostname == nil || *site.PlatformHostname != "backend.apps.example.com" {
		t.Fatalf("Site hostname changed after App claim: %v", site.PlatformHostname)
	}

	duplicateSiteID := uuid.Must(uuid.NewV7())
	duplicateSiteCandidate := platformhostname.Candidates("backend", duplicateSiteID)[1]
	if _, err := f.repo.CreateSite(f.ctx, duplicateSiteID, f.projectOneID, siteActor, SiteInput{
		Name: "backend", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Site create error = %v, want conflict", err)
	}
	var failedSiteClaim int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM platform_hostname_claims WHERE label=$1`, duplicateSiteCandidate).Scan(&failedSiteClaim); err != nil {
		t.Fatal(err)
	}
	if failedSiteClaim != 0 {
		t.Fatalf("failed Site insert left a hostname claim for %q", duplicateSiteCandidate)
	}
	duplicateAppID := uuid.Must(uuid.NewV7())
	duplicateAppCandidate := platformhostname.AppCandidates("backend", duplicateAppID)[1]
	if _, err := f.repo.CreateApp(f.ctx, duplicateAppID, f.projectOneID, f.actor, AppInput{Name: "backend", Enabled: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate App create error = %v, want conflict", err)
	}
	var failedAppClaim int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM platform_hostname_claims WHERE label=$1`, duplicateAppCandidate).Scan(&failedAppClaim); err != nil {
		t.Fatal(err)
	}
	if failedAppClaim != 0 {
		t.Fatalf("failed App insert left a hostname claim for %q", duplicateAppCandidate)
	}

	appLabel := ""
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_apps WHERE id=$1`, appID).Scan(&appLabel); err != nil {
		t.Fatal(err)
	}
	newName := "backend-service"
	renamed, err := f.repo.UpdateApp(f.ctx, f.projectOneID, appID, f.actor, AppPatch{Name: &newName})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.PlatformHostname == nil || *renamed.PlatformHostname != *app.PlatformHostname {
		t.Fatalf("App rename changed hostname from %q to %v", *app.PlatformHostname, renamed.PlatformHostname)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM platform_hostname_claims WHERE resource_type='app' AND resource_id=$1`, appID).Scan(&newName); err != nil {
		t.Fatal(err)
	}
	if newName != appLabel {
		t.Fatalf("App rename changed persisted claim from %q to %q", appLabel, newName)
	}

	layout, err := f.repo.ReplaceProjectServiceLayout(f.ctx, f.projectOneID, f.accountID, []ProjectServiceLayoutInput{{ResourceType: "app", ResourceID: appID, X: 12, Y: 34}})
	if err != nil {
		t.Fatal(err)
	}
	if len(layout) != 1 || layout[0].ResourceType != "app" {
		t.Fatalf("App missing from service layout: %+v", layout)
	}
	if err := f.repo.DeleteApp(f.ctx, f.projectOneID, appID, f.actor); err != nil {
		t.Fatal(err)
	}
	var remainingClaim, remainingLayout int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM platform_hostname_claims WHERE label=$1`, appLabel).Scan(&remainingClaim); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM project_service_layouts WHERE project_id=$1 AND resource_type='app' AND resource_id=$2`, f.projectOneID, appID).Scan(&remainingLayout); err != nil {
		t.Fatal(err)
	}
	if remainingClaim != 0 || remainingLayout != 0 {
		t.Fatalf("App deletion left claim/layout rows: %d/%d", remainingClaim, remainingLayout)
	}
	reuse, err := f.repo.CreateSite(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, siteActor, SiteInput{
		Name: appLabel, Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("Site could not claim label after App deletion: %v", err)
	}
	var reusedLabel string
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, uuid.MustParse(reuse.ID)).Scan(&reusedLabel); err != nil {
		t.Fatal(err)
	}
	if reusedLabel != appLabel {
		t.Fatalf("released App label was not reusable by Site: got %q want %q", reusedLabel, appLabel)
	}

	type raceResult struct {
		label string
		err   error
	}
	start := make(chan struct{})
	results := make(chan raceResult, 2)
	crossOrganizationID := uuid.Must(uuid.NewV7())
	crossProjectID := uuid.Must(uuid.NewV7())
	crossSlug := "apps-race-" + crossOrganizationID.String()[:8]
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)`, crossOrganizationID, "Apps namespace race", crossSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner')`, crossOrganizationID, f.accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,$3)`, crossProjectID, crossOrganizationID, crossSlug+"-project"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = f.pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE organization_id=$1`, crossOrganizationID)
		_, _ = f.pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, crossOrganizationID)
	})
	var writers sync.WaitGroup
	siteID := uuid.Must(uuid.NewV7())
	appRaceID := uuid.Must(uuid.NewV7())
	writers.Add(2)
	go func() {
		defer writers.Done()
		<-start
		created, createErr := f.repo.CreateSite(f.ctx, siteID, f.projectOneID, siteActor, SiteInput{Name: "racing", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20})
		if createErr != nil {
			results <- raceResult{err: createErr}
			return
		}
		var label string
		createErr = f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, siteID).Scan(&label)
		results <- raceResult{label: label, err: createErr}
		_ = created
	}()
	go func() {
		defer writers.Done()
		<-start
		created, createErr := f.repo.CreateApp(f.ctx, appRaceID, crossProjectID, f.actor, AppInput{Name: "racing", Enabled: true})
		if createErr != nil {
			results <- raceResult{err: createErr}
			return
		}
		var label string
		createErr = f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_apps WHERE id=$1`, appRaceID).Scan(&label)
		results <- raceResult{label: label, err: createErr}
		_ = created
	}()
	close(start)
	writers.Wait()
	close(results)
	labels := make([]string, 0, 2)
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Site/App allocation failed: %v", result.err)
		}
		labels = append(labels, result.label)
	}
	if len(labels) != 2 || labels[0] == labels[1] {
		t.Fatalf("concurrent Site/App claim labels = %v, want two distinct labels", labels)
	}
	var duplicateLabels int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM platform_hostname_claims WHERE label IN ($1,$2)`, labels[0], labels[1]).Scan(&duplicateLabels); err != nil {
		t.Fatal(err)
	}
	if duplicateLabels != 2 {
		t.Fatalf("concurrent Site/App global claims = %d, want 2", duplicateLabels)
	}

	extraAppID := uuid.Must(uuid.NewV7())
	if _, err := f.repo.CreateApp(f.ctx, extraAppID, f.projectTwoID, f.actor, AppInput{Name: "cascade-app", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ReplaceProjectServiceLayout(f.ctx, f.projectTwoID, f.accountID, []ProjectServiceLayoutInput{{ResourceType: "app", ResourceID: extraAppID, X: 3, Y: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DeleteProject(f.ctx, f.projectTwoID, f.accountID, f.projectTwoName); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM project_apps WHERE project_id=$1`,
		`SELECT count(*) FROM platform_hostname_claims WHERE project_id=$1`,
		`SELECT count(*) FROM project_service_layouts WHERE project_id=$1`,
	} {
		var count int
		if err := f.pool.QueryRow(f.ctx, query, f.projectTwoID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("project deletion left %d rows for query %q", count, query)
		}
	}

	releaseSite, err := f.repo.CreateSite(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, siteActor, SiteInput{
		Name: "release-claim", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseLabel string
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, uuid.MustParse(releaseSite.ID)).Scan(&releaseLabel); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DeleteSite(f.ctx, f.projectOneID, uuid.MustParse(releaseSite.ID), siteActor); err != nil {
		t.Fatal(err)
	}
	recreated, err := f.repo.CreateSite(f.ctx, uuid.Must(uuid.NewV7()), f.projectOneID, siteActor, SiteInput{
		Name: "release-claim", Enabled: true, Status: "active", ArtifactQuotaBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var recreatedLabel string
	if err := f.pool.QueryRow(f.ctx, `SELECT platform_label FROM project_sites WHERE id=$1`, uuid.MustParse(recreated.ID)).Scan(&recreatedLabel); err != nil {
		t.Fatal(err)
	}
	if recreatedLabel != releaseLabel {
		t.Fatalf("Site delete did not release stable platform claim: old=%q new=%q", releaseLabel, recreatedLabel)
	}
	if len(siteRoutesAfter) == 0 {
		t.Fatal("route snapshot test did not include the created static Site")
	}
}

func bytesOfZeroes(length int) []byte { return make([]byte, length) }

func jsonMarshal(value any) ([]byte, error) { return json.Marshal(value) }

func jsonUnmarshal(value []byte, target any) error { return json.Unmarshal(value, target) }
