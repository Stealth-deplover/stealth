package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminMonitorAlertFixture struct {
	pool      *pgxpool.Pool
	repo      *Repository
	accountID uuid.UUID
	httpID    uuid.UUID
	tlsID     uuid.UUID
}

func newAdminMonitorAlertFixture(t *testing.T) adminMonitorAlertFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fixture := adminMonitorAlertFixture{
		pool:      pool,
		repo:      New(pool),
		accountID: uuid.Must(uuid.NewV7()),
		httpID:    uuid.Must(uuid.NewV7()),
		tlsID:     uuid.Must(uuid.NewV7()),
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,'monitor-alert-test-password')`,
		fixture.accountID, "monitor-alert-"+fixture.accountID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_admin')`, fixture.accountID); err != nil {
		t.Fatal(err)
	}
	for _, monitor := range []struct {
		id     uuid.UUID
		kind   string
		target string
	}{
		{fixture.httpID, "http", "https://example.test/health"},
		{fixture.tlsID, "tls", "example.test:443"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO admin_monitors (id,name,kind,target,config_encrypted,created_by_account_id,next_check_at)
			VALUES ($1,$2,$3,$4,'\\x01'::bytea,$5,now())`,
			monitor.id, "AUD-05 "+monitor.kind, monitor.kind, monitor.target, fixture.accountID); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_alert_rules WHERE created_by_account_id=$1`, fixture.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_monitors WHERE created_by_account_id=$1`, fixture.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_events WHERE actor_account_id=$1`, fixture.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM instance_roles WHERE account_id=$1`, fixture.accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM accounts WHERE id=$1`, fixture.accountID)
	})
	return fixture
}

func TestAdminAlertMonitorReferenceValidationIntegration(t *testing.T) {
	fixture := newAdminMonitorAlertFixture(t)
	ctx := context.Background()
	newRule := func(t *testing.T, kind string, condition map[string]any) {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		_, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, id, AdminAlertRuleInput{
			Name: "AUD-05 rule", Kind: kind, Condition: mustJSON(condition), Severity: "warning", Enabled: true,
		})
		if !errors.Is(err, ErrInvalidAdminAlert) {
			t.Fatalf("CreateAdminAlertRule(%s) error = %v, want invalid alert", kind, err)
		}
	}

	newRule(t, "monitor_failure", map[string]any{"monitor_id": uuid.Must(uuid.NewV7()).String()})
	newRule(t, "heartbeat_failure", map[string]any{"monitor_id": fixture.httpID.String()})
	newRule(t, "certificate_expiry", map[string]any{"monitor_id": fixture.httpID.String(), "days": 7})

	validID := uuid.Must(uuid.NewV7())
	if _, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, validID, AdminAlertRuleInput{
		Name: "AUD-05 TLS expiry", Kind: "certificate_expiry", Condition: mustJSON(map[string]any{"monitor_id": fixture.tlsID.String(), "days": 7}), Severity: "warning", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateAdminAlertRule(valid certificate expiry) error = %v", err)
	}
}

func TestDeleteAdminMonitorWithAlertRuleConflictsIntegration(t *testing.T) {
	fixture := newAdminMonitorAlertFixture(t)
	ctx := context.Background()
	ruleID := uuid.Must(uuid.NewV7())
	if _, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, ruleID, AdminAlertRuleInput{
		Name: "AUD-05 monitor failure", Kind: "monitor_failure", Condition: mustJSON(map[string]any{"monitor_id": fixture.httpID.String()}), Severity: "critical", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateAdminAlertRule() error = %v", err)
	}
	if err := fixture.repo.DeleteAdminMonitor(ctx, fixture.accountID, fixture.httpID); !errors.Is(err, ErrAdminMonitorHasRules) {
		t.Fatalf("DeleteAdminMonitor() error = %v, want monitor conflict", err)
	}
	if _, err := fixture.repo.AdminMonitorByID(ctx, fixture.httpID); err != nil {
		t.Fatalf("monitor after rejected deletion: %v", err)
	}
	if err := fixture.repo.DeleteAdminAlertRule(ctx, fixture.accountID, ruleID); err != nil {
		t.Fatalf("DeleteAdminAlertRule() error = %v", err)
	}
	if err := fixture.repo.DeleteAdminMonitor(ctx, fixture.accountID, fixture.httpID); err != nil {
		t.Fatalf("DeleteAdminMonitor() after rule deletion error = %v", err)
	}
}

func mustJSON(value map[string]any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
