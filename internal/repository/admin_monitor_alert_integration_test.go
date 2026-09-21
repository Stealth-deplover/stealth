package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminMonitorAlertFixture struct {
	pool        *pgxpool.Pool
	repo        *Repository
	accountID   uuid.UUID
	httpID      uuid.UUID
	tlsID       uuid.UUID
	heartbeatID uuid.UUID
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
		pool:        pool,
		accountID:   uuid.Must(uuid.NewV7()),
		httpID:      uuid.Must(uuid.NewV7()),
		tlsID:       uuid.Must(uuid.NewV7()),
		heartbeatID: uuid.Must(uuid.NewV7()),
	}
	cipher, err := functionsecret.New([]byte(strings.Repeat("m", functionsecret.KeySize)))
	if err != nil {
		t.Fatal(err)
	}
	fixture.repo = NewWithDependencies(pool, Dependencies{AdminCipher: cipher})
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
		{fixture.heartbeatID, "heartbeat", "nightly-backup"},
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

func TestUpdateAdminMonitorPreservesAlertRuleCompatibilityIntegration(t *testing.T) {
	fixture := newAdminMonitorAlertFixture(t)
	ctx := context.Background()

	heartbeatRuleID := uuid.Must(uuid.NewV7())
	if _, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, heartbeatRuleID, AdminAlertRuleInput{
		Name: "AUD-05 heartbeat failure", Kind: "heartbeat_failure",
		Condition: mustJSON(map[string]any{"monitor_id": fixture.heartbeatID.String()}), Severity: "critical", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateAdminAlertRule(heartbeat_failure) error = %v", err)
	}
	if _, err := fixture.repo.UpdateAdminMonitor(ctx, fixture.accountID, fixture.heartbeatID, adminMonitorUpdateInput("http", "https://example.test/heartbeat")); !errors.Is(err, ErrAdminMonitorRuleConflict) {
		t.Fatalf("heartbeat monitor kind update error = %v, want ErrAdminMonitorRuleConflict", err)
	}
	monitor, err := fixture.repo.AdminMonitorByID(ctx, fixture.heartbeatID)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != "heartbeat" {
		t.Fatalf("heartbeat monitor kind after rejected update = %q, want heartbeat", monitor.Kind)
	}

	tlsRuleID := uuid.Must(uuid.NewV7())
	if _, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, tlsRuleID, AdminAlertRuleInput{
		Name: "AUD-05 certificate expiry", Kind: "certificate_expiry",
		Condition: mustJSON(map[string]any{"monitor_id": fixture.tlsID.String(), "days": 7}), Severity: "warning", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateAdminAlertRule(certificate_expiry) error = %v", err)
	}
	if _, err := fixture.repo.UpdateAdminMonitor(ctx, fixture.accountID, fixture.tlsID, adminMonitorUpdateInput("http", "https://example.test/tls")); !errors.Is(err, ErrAdminMonitorRuleConflict) {
		t.Fatalf("TLS monitor kind update error = %v, want ErrAdminMonitorRuleConflict", err)
	}
	monitor, err = fixture.repo.AdminMonitorByID(ctx, fixture.tlsID)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != "tls" {
		t.Fatalf("TLS monitor kind after rejected update = %q, want tls", monitor.Kind)
	}

	monitorFailureRuleID := uuid.Must(uuid.NewV7())
	if _, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, monitorFailureRuleID, AdminAlertRuleInput{
		Name: "AUD-05 monitor failure", Kind: "monitor_failure",
		Condition: mustJSON(map[string]any{"monitor_id": fixture.httpID.String()}), Severity: "warning", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateAdminAlertRule(monitor_failure) error = %v", err)
	}
	if _, err := fixture.repo.UpdateAdminMonitor(ctx, fixture.accountID, fixture.httpID, adminMonitorUpdateInput("tls", "example.test:443")); err != nil {
		t.Fatalf("compatible monitor_failure kind update error = %v", err)
	}
	monitor, err = fixture.repo.AdminMonitorByID(ctx, fixture.httpID)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != "tls" {
		t.Fatalf("compatible monitor kind after update = %q, want tls", monitor.Kind)
	}

	if err := fixture.repo.DeleteAdminAlertRule(ctx, fixture.accountID, heartbeatRuleID); err != nil {
		t.Fatalf("DeleteAdminAlertRule(heartbeat_failure) error = %v", err)
	}
	if _, err := fixture.repo.UpdateAdminMonitor(ctx, fixture.accountID, fixture.heartbeatID, adminMonitorUpdateInput("http", "https://example.test/heartbeat")); err != nil {
		t.Fatalf("monitor kind update after dependent rule removal error = %v", err)
	}
	monitor, err = fixture.repo.AdminMonitorByID(ctx, fixture.heartbeatID)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != "http" {
		t.Fatalf("monitor kind after dependent rule removal = %q, want http", monitor.Kind)
	}
}

func TestAdminMonitorAlertLockOrderingIntegration(t *testing.T) {
	fixture := newAdminMonitorAlertFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ruleID := uuid.Must(uuid.NewV7())
	if _, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, ruleID, AdminAlertRuleInput{
		Name: "lock ordering", Kind: "monitor_failure",
		Condition: mustJSON(map[string]any{"monitor_id": fixture.httpID.String()}), Severity: "warning", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	txA, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback(ctx)
	var monitorKind string
	if err := txA.QueryRow(ctx, `SELECT kind FROM admin_monitors WHERE id=$1 FOR UPDATE`, fixture.httpID).Scan(&monitorKind); err != nil {
		t.Fatal(err)
	}

	started, done, err := startAdminAlertRuleUpdateTx(ctx, fixture, ruleID, AdminAlertRulePatch{Name: stringPointer("updated while monitor is locked")})
	if err != nil {
		t.Fatal(err)
	}
	pid := waitForAdminAlertRuleUpdateStart(t, ctx, started, done)
	if err := waitForPostgresLockWait(ctx, fixture.pool, pid); err != nil {
		t.Fatal(err)
	}

	if err := validateAdminMonitorKindChangeTx(ctx, txA, fixture.httpID, "tls"); err != nil {
		failOnPostgresDeadlock(t, err)
		t.Fatalf("monitor-side dependent-rule lock error = %v", err)
	}
	if _, err := txA.Exec(ctx, `UPDATE admin_monitors SET kind='tls' WHERE id=$1`, fixture.httpID); err != nil {
		t.Fatal(err)
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := awaitAdminAlertRuleUpdate(t, ctx, done); err != nil {
		failOnPostgresDeadlock(t, err)
		t.Fatalf("alert-rule update after monitor commit = %v", err)
	}

	monitor, err := fixture.repo.AdminMonitorByID(ctx, fixture.httpID)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := fixture.repo.AdminAlertRuleByID(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	if !monitorAlertRuleCompatible(rule.Kind, monitor.Kind) {
		t.Fatalf("final monitor/rule relationship is incompatible: rule=%s monitor=%s", rule.Kind, monitor.Kind)
	}
}

func TestAdminMonitorWorkerAndRuleUpdateConcurrencyIntegration(t *testing.T) {
	fixture := newAdminMonitorAlertFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ruleID := uuid.Must(uuid.NewV7())
	if _, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, ruleID, AdminAlertRuleInput{
		Name: "worker concurrency", Kind: "monitor_failure",
		Condition: mustJSON(map[string]any{"monitor_id": fixture.httpID.String()}), Severity: "warning", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	txA, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback(ctx)
	if err := txA.QueryRow(ctx, `SELECT status FROM admin_monitors WHERE id=$1 FOR UPDATE`, fixture.httpID).Scan(new(string)); err != nil {
		t.Fatal(err)
	}

	started, done, err := startAdminAlertRuleUpdateTx(ctx, fixture, ruleID, AdminAlertRulePatch{Name: stringPointer("updated during monitor evaluation")})
	if err != nil {
		t.Fatal(err)
	}
	pid := waitForAdminAlertRuleUpdateStart(t, ctx, started, done)
	if err := waitForPostgresLockWait(ctx, fixture.pool, pid); err != nil {
		t.Fatal(err)
	}

	if err := evaluateAdminMonitorAlertsTx(ctx, txA, fixture.httpID, AdminMonitorCheckInput{
		Success: false, LatencyMS: 12, Details: json.RawMessage(`{}`), Error: "synthetic worker failure",
	}); err != nil {
		failOnPostgresDeadlock(t, err)
		t.Fatalf("monitor evaluator error = %v", err)
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := awaitAdminAlertRuleUpdate(t, ctx, done); err != nil {
		failOnPostgresDeadlock(t, err)
		t.Fatalf("alert-rule update after monitor evaluation = %v", err)
	}

	rule, err := fixture.repo.AdminAlertRuleByID(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := fixture.repo.AdminMonitorByID(ctx, fixture.httpID)
	if err != nil {
		t.Fatal(err)
	}
	if !monitorAlertRuleCompatible(rule.Kind, monitor.Kind) {
		t.Fatalf("final worker/rule relationship is incompatible: rule=%s monitor=%s", rule.Kind, monitor.Kind)
	}
	var historyCount int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM admin_alert_events WHERE rule_id_snapshot=$1`, ruleID).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount == 0 {
		t.Fatal("monitor evaluation did not preserve an alert history event")
	}
}

func TestAdminMonitorRuleCompatibilityConcurrentMutationIntegration(t *testing.T) {
	fixture := newAdminMonitorAlertFixture(t)
	for _, test := range []struct {
		name         string
		monitorID    uuid.UUID
		ruleKind     string
		condition    map[string]any
		expectedKind string
	}{
		{name: "heartbeat", monitorID: fixture.heartbeatID, ruleKind: "heartbeat_failure", condition: map[string]any{"monitor_id": fixture.heartbeatID.String()}, expectedKind: "heartbeat"},
		{name: "certificate expiry", monitorID: fixture.tlsID, ruleKind: "certificate_expiry", condition: map[string]any{"monitor_id": fixture.tlsID.String(), "days": 7}, expectedKind: "tls"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ruleID := uuid.Must(uuid.NewV7())
			if _, err := fixture.repo.CreateAdminAlertRule(ctx, fixture.accountID, ruleID, AdminAlertRuleInput{
				Name: "concurrent compatibility", Kind: test.ruleKind,
				Condition: mustJSON(test.condition), Severity: "warning", Enabled: true,
			}); err != nil {
				t.Fatal(err)
			}

			txA, err := fixture.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer txA.Rollback(ctx)
			if err := txA.QueryRow(ctx, `SELECT kind FROM admin_monitors WHERE id=$1 FOR UPDATE`, test.monitorID).Scan(new(string)); err != nil {
				t.Fatal(err)
			}

			txC, err := fixture.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer txC.Rollback(ctx)
			if err := txC.QueryRow(ctx, `SELECT kind FROM admin_monitors WHERE id=$1 FOR UPDATE`, fixture.httpID).Scan(new(string)); err != nil {
				t.Fatal(err)
			}

			started, done, err := startAdminAlertRuleUpdateTx(ctx, fixture, ruleID, AdminAlertRulePatch{
				Condition: mustJSON(map[string]any{
					"monitor_id": fixture.httpID.String(),
					"days":       7,
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			pid := waitForAdminAlertRuleUpdateStart(t, ctx, started, done)
			if err := waitForPostgresLockWait(ctx, fixture.pool, pid); err != nil {
				t.Fatal(err)
			}

			if err := validateAdminMonitorKindChangeTx(ctx, txA, test.monitorID, "http"); !errors.Is(err, ErrAdminMonitorRuleConflict) {
				failOnPostgresDeadlock(t, err)
				t.Fatalf("monitor kind change error = %v, want compatibility conflict", err)
			}
			if err := txA.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := txC.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := awaitAdminAlertRuleUpdate(t, ctx, done); !errors.Is(err, ErrInvalidAdminAlert) {
				failOnPostgresDeadlock(t, err)
				t.Fatalf("incompatible retarget error = %v, want invalid alert", err)
			}

			monitor, err := fixture.repo.AdminMonitorByID(ctx, test.monitorID)
			if err != nil {
				t.Fatal(err)
			}
			rule, err := fixture.repo.AdminAlertRuleByID(ctx, ruleID)
			if err != nil {
				t.Fatal(err)
			}
			if monitor.Kind != test.expectedKind {
				t.Fatalf("monitor kind after rejected concurrent mutation = %q", monitor.Kind)
			}
			if !monitorAlertRuleCompatible(rule.Kind, monitor.Kind) {
				t.Fatalf("final concurrent relationship is incompatible: rule=%s monitor=%s", rule.Kind, monitor.Kind)
			}
		})
	}
}

func startAdminAlertRuleUpdateTx(ctx context.Context, fixture adminMonitorAlertFixture, ruleID uuid.UUID, patch AdminAlertRulePatch) (<-chan int32, <-chan error, error) {
	normalized, err := normalizeAdminAlertRulePatch(patch)
	if err != nil {
		return nil, nil, err
	}
	started := make(chan int32, 1)
	done := make(chan error, 1)
	go func() {
		tx, beginErr := fixture.pool.Begin(ctx)
		if beginErr != nil {
			done <- beginErr
			return
		}
		defer tx.Rollback(ctx)
		var pid int32
		if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			done <- err
			return
		}
		started <- pid
		_, updateErr := updateAdminAlertRuleTx(ctx, tx, fixture.accountID, ruleID, normalized)
		if updateErr == nil {
			updateErr = tx.Commit(ctx)
		}
		done <- updateErr
	}()
	return started, done, nil
}

func waitForAdminAlertRuleUpdateStart(t *testing.T, ctx context.Context, started <-chan int32, done <-chan error) int32 {
	t.Helper()
	select {
	case pid := <-started:
		return pid
	case err := <-done:
		t.Fatalf("alert-rule update finished before lock orchestration: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return 0
}

func awaitAdminAlertRuleUpdate(t *testing.T, ctx context.Context, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return ctx.Err()
	}
}

func waitForPostgresLockWait(ctx context.Context, pool *pgxpool.Pool, pid int32) error {
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := pool.QueryRow(deadline, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting)
		if err != nil {
			return err
		}
		if waiting {
			return nil
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("postgres backend %d did not wait for a row lock: %w", pid, deadline.Err())
		case <-ticker.C:
		}
	}
}

func failOnPostgresDeadlock(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
		t.Fatalf("PostgreSQL deadlock detected (SQLSTATE 40P01): %v", err)
	}
}

func stringPointer(value string) *string {
	return &value
}

func adminMonitorUpdateInput(kind, target string) AdminMonitorInput {
	return AdminMonitorInput{
		Name: "AUD-05 updated monitor", Kind: kind, Target: target,
		IntervalSeconds: 60, TimeoutMS: 1000, Enabled: true,
		PublicConfig: []byte(`{}`), SecretConfig: []byte(`{}`),
	}
}

func mustJSON(value map[string]any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
