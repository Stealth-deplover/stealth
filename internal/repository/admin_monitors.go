package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	adminMonitorMaxLimit  = 100
	adminMonitorMaxConfig = 64 << 10
)

var (
	ErrInvalidAdminMonitor = errors.New("invalid admin monitor")
	ErrNoAdminMonitor      = errors.New("no admin monitor available")
)

const adminMonitorProjection = `
	m.id::text,m.name,m.kind,m.target,m.interval_seconds,m.timeout_ms,m.enabled,
	m.public_config,m.status,m.last_checked_at,m.last_success_at,m.last_failure_at,
	m.last_latency_ms,m.last_status_code,m.last_error,m.last_heartbeat_at,m.next_check_at,
	m.config_encrypted,m.heartbeat_token_hash,m.created_by_account_id::text,m.created_at,m.updated_at`

// AdminMonitorInput is the already-validated control-plane representation.
// PublicConfig contains only fields safe to return to an owner; the encrypted
// config is consumed only by the trusted monitor worker.
type AdminMonitorInput struct {
	Name               string
	Kind               string
	Target             string
	IntervalSeconds    int
	TimeoutMS          int
	Enabled            bool
	PublicConfig       json.RawMessage
	SecretConfig       []byte
	HeartbeatTokenHash []byte
}

type AdminMonitorJob struct {
	ID              uuid.UUID
	Name            string
	Kind            string
	Target          string
	IntervalSeconds int
	TimeoutMS       int
	PublicConfig    json.RawMessage
	EncryptedConfig []byte
	LastHeartbeatAt *time.Time
}

type AdminMonitorCheckInput struct {
	Success    bool
	LatencyMS  int64
	StatusCode *int
	Error      string
	Details    json.RawMessage
}

func (r *Repository) ListAdminMonitors(ctx context.Context, limit int) ([]domain.AdminMonitor, error) {
	if r == nil || r.pool == nil {
		return nil, ErrNotFound
	}
	if limit < 1 || limit > adminMonitorMaxLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidAdminMonitor, adminMonitorMaxLimit)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+adminMonitorProjection+`
		FROM admin_monitors m
		ORDER BY m.updated_at DESC,m.id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AdminMonitor, 0, limit)
	for rows.Next() {
		item, err := scanAdminMonitor(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) AdminMonitorByID(ctx context.Context, id uuid.UUID) (domain.AdminMonitor, error) {
	if id == uuid.Nil || r == nil || r.pool == nil {
		return domain.AdminMonitor{}, ErrNotFound
	}
	item, err := scanAdminMonitor(r.pool.QueryRow(ctx, `
		SELECT `+adminMonitorProjection+` FROM admin_monitors m WHERE m.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminMonitor{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateAdminMonitor(ctx context.Context, accountID, id uuid.UUID, input AdminMonitorInput) (domain.AdminMonitor, error) {
	if err := validateAdminMonitorInput(id, input); err != nil {
		return domain.AdminMonitor{}, err
	}
	if r.adminCipher == nil {
		return domain.AdminMonitor{}, errors.New("admin monitor secret encryption is unavailable")
	}
	encryptedConfig, err := r.adminCipher.Encrypt(input.SecretConfig)
	if err != nil {
		return domain.AdminMonitor{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminMonitor{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminMonitor{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO admin_monitors
		(id,name,kind,target,interval_seconds,timeout_ms,enabled,public_config,config_encrypted,heartbeat_token_hash,next_check_at,created_by_account_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now(),$11)`,
		id, input.Name, input.Kind, input.Target, input.IntervalSeconds, input.TimeoutMS,
		input.Enabled, input.PublicConfig, encryptedConfig, nullableHeartbeatHash(input.HeartbeatTokenHash), accountID)
	if err != nil {
		return domain.AdminMonitor{}, mapError(err)
	}
	item, err := scanAdminMonitor(tx.QueryRow(ctx, `SELECT `+adminMonitorProjection+` FROM admin_monitors m WHERE m.id=$1`, id))
	if err != nil {
		return domain.AdminMonitor{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.monitor.create", "admin_monitor", id, map[string]any{"kind": input.Kind, "target": input.Target}); err != nil {
		return domain.AdminMonitor{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminMonitor{}, err
	}
	return item, nil
}

func (r *Repository) UpdateAdminMonitor(ctx context.Context, accountID, id uuid.UUID, input AdminMonitorInput) (domain.AdminMonitor, error) {
	if err := validateAdminMonitorInput(id, input); err != nil {
		return domain.AdminMonitor{}, err
	}
	if r.adminCipher == nil {
		return domain.AdminMonitor{}, errors.New("admin monitor secret encryption is unavailable")
	}
	encryptedConfig, err := r.adminCipher.Encrypt(input.SecretConfig)
	if err != nil {
		return domain.AdminMonitor{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminMonitor{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminMonitor{}, err
	}
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM admin_monitors WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AdminMonitor{}, ErrNotFound
		}
		return domain.AdminMonitor{}, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE admin_monitors
		SET name=$2,kind=$3,target=$4,interval_seconds=$5,timeout_ms=$6,enabled=$7,
		    public_config=$8,config_encrypted=$9,heartbeat_token_hash=$10,
		    status=CASE WHEN $7 THEN CASE WHEN status='paused' THEN 'unknown' ELSE status END ELSE 'paused' END,
		    next_check_at=CASE WHEN $7 THEN LEAST(next_check_at,now()) ELSE next_check_at END,
		    updated_at=now()
		WHERE id=$1`, id, input.Name, input.Kind, input.Target, input.IntervalSeconds, input.TimeoutMS,
		input.Enabled, input.PublicConfig, encryptedConfig, nullableHeartbeatHash(input.HeartbeatTokenHash))
	if err != nil {
		return domain.AdminMonitor{}, mapError(err)
	}
	item, err := scanAdminMonitor(tx.QueryRow(ctx, `SELECT `+adminMonitorProjection+` FROM admin_monitors m WHERE m.id=$1`, id))
	if err != nil {
		return domain.AdminMonitor{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.monitor.update", "admin_monitor", id, map[string]any{"kind": input.Kind, "target": input.Target}); err != nil {
		return domain.AdminMonitor{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminMonitor{}, err
	}
	return item, nil
}

func (r *Repository) DeleteAdminMonitor(ctx context.Context, accountID, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return err
	}
	var monitorKind string
	if err := tx.QueryRow(ctx, `SELECT kind FROM admin_monitors WHERE id=$1 FOR UPDATE`, id).Scan(&monitorKind); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var hasAlertRules bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM admin_alert_rules
			WHERE kind IN ('monitor_failure','heartbeat_failure','certificate_expiry')
			  AND condition->>'monitor_id'=$1
		)`, id.String()).Scan(&hasAlertRules); err != nil {
		return err
	}
	if hasAlertRules {
		return ErrAdminMonitorHasRules
	}
	result, err := tx.Exec(ctx, `DELETE FROM admin_monitors WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.monitor.delete", "admin_monitor", id, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClaimNextAdminMonitor leases one due monitor. SKIP LOCKED prevents two
// workers from running the same network probe concurrently; a stale lease is
// recovered by the next worker process after leaseAge.
func (r *Repository) ClaimNextAdminMonitor(ctx context.Context, workerID string, leaseAge time.Duration) (AdminMonitorJob, error) {
	if r == nil || r.pool == nil || !validFunctionWorkerID(workerID) || leaseAge <= 0 {
		return AdminMonitorJob{}, ErrInvalidAdminMonitor
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AdminMonitorJob{}, err
	}
	defer tx.Rollback(ctx)
	var job AdminMonitorJob
	err = tx.QueryRow(ctx, `
		SELECT id,name,kind,target,interval_seconds,timeout_ms,public_config,config_encrypted,last_heartbeat_at
		FROM admin_monitors
		WHERE enabled AND next_check_at<=now()
		  AND (leased_at IS NULL OR leased_at < now() - ($1::double precision * interval '1 second'))
		ORDER BY next_check_at,id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, leaseAge.Seconds()).Scan(
		&job.ID, &job.Name, &job.Kind, &job.Target, &job.IntervalSeconds, &job.TimeoutMS,
		&job.PublicConfig, &job.EncryptedConfig, &job.LastHeartbeatAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminMonitorJob{}, ErrNoAdminMonitor
	}
	if err != nil {
		return AdminMonitorJob{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE admin_monitors SET leased_at=now(),worker_id=$2,updated_at=now() WHERE id=$1`, job.ID, workerID); err != nil {
		return AdminMonitorJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminMonitorJob{}, err
	}
	return job, nil
}

func (r *Repository) RequeueStaleAdminMonitors(ctx context.Context, leaseAge time.Duration) (int64, error) {
	if leaseAge <= 0 {
		return 0, ErrInvalidAdminMonitor
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE admin_monitors
		SET leased_at=NULL,worker_id=NULL,next_check_at=LEAST(next_check_at,now()),updated_at=now()
		WHERE enabled AND leased_at IS NOT NULL
		  AND leased_at < now() - ($1::double precision * interval '1 second')`, leaseAge.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// RecordAdminHeartbeat accepts only the hash of the caller-provided token.
// The token itself is never persisted, logged, or included in the audit
// ledger. Updating the heartbeat also brings the next check forward so a
// healthy external job is observed promptly.
func (r *Repository) RecordAdminHeartbeat(ctx context.Context, monitorID uuid.UUID, tokenHash []byte) error {
	if r == nil || r.pool == nil || monitorID == uuid.Nil || len(tokenHash) != 32 {
		return ErrInvalidAdminMonitor
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE admin_monitors
		SET last_heartbeat_at=now(),next_check_at=LEAST(next_check_at,now()),updated_at=now()
		WHERE id=$1 AND kind='heartbeat' AND enabled AND heartbeat_token_hash=$2`, monitorID, tokenHash)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) CompleteAdminMonitorCheck(ctx context.Context, monitorID uuid.UUID, workerID string, input AdminMonitorCheckInput) error {
	if monitorID == uuid.Nil || !validFunctionWorkerID(workerID) || input.LatencyMS < 0 || (input.StatusCode != nil && (*input.StatusCode < 100 || *input.StatusCode > 599)) {
		return ErrInvalidAdminMonitor
	}
	if len(input.Details) == 0 {
		input.Details = json.RawMessage(`{}`)
	}
	if !json.Valid(input.Details) || len(input.Details) > 16<<10 {
		return ErrInvalidAdminMonitor
	}
	errorMessage := normalizeAdminMonitorError(input.Error)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM admin_monitors WHERE id=$1 AND worker_id=$2 FOR UPDATE`, monitorID, workerID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoAdminMonitor
	}
	if err != nil {
		return err
	}
	newStatus := "failing"
	if input.Success {
		newStatus = "healthy"
	}
	var statusCode any
	if input.StatusCode != nil {
		statusCode = *input.StatusCode
	}
	var lastError any
	if errorMessage != "" {
		lastError = errorMessage
	}
	_, err = tx.Exec(ctx, `
		UPDATE admin_monitors
		SET status=$2,last_checked_at=now(),
		    last_success_at=CASE WHEN $3 THEN now() ELSE last_success_at END,
		    last_failure_at=CASE WHEN $3 THEN last_failure_at ELSE now() END,
		    last_latency_ms=$4,last_status_code=$5,last_error=$6,
		    next_check_at=now()+($7::double precision * interval '1 second'),
		    leased_at=NULL,worker_id=NULL,updated_at=now()
		WHERE id=$1 AND worker_id=$8`, monitorID, newStatus, input.Success, input.LatencyMS, statusCode, lastError, maxInt(inputInterval(ctx, tx, monitorID), 5), workerID)
	if err != nil {
		return err
	}
	checkID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_monitor_checks (id,monitor_id,success,latency_ms,status_code,error,details) VALUES ($1,$2,$3,$4,$5,$6,$7)`, checkID, monitorID, input.Success, input.LatencyMS, statusCode, lastError, input.Details)
	if err != nil {
		return err
	}
	if err := evaluateAdminMonitorAlertsTx(ctx, tx, monitorID, input); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListAdminMonitorChecks(ctx context.Context, monitorID uuid.UUID, limit int) ([]domain.AdminMonitorCheck, error) {
	if monitorID == uuid.Nil || limit < 1 || limit > adminMonitorMaxLimit {
		return nil, ErrInvalidAdminMonitor
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text,monitor_id::text,checked_at,success,latency_ms,status_code,error,details
		FROM admin_monitor_checks WHERE monitor_id=$1
		ORDER BY checked_at DESC,id DESC LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AdminMonitorCheck, 0, limit)
	for rows.Next() {
		var item domain.AdminMonitorCheck
		var details []byte
		if err := rows.Scan(&item.ID, &item.MonitorID, &item.CheckedAt, &item.Success, &item.LatencyMS, &item.StatusCode, &item.Error, &details); err != nil {
			return nil, err
		}
		if json.Valid(details) {
			_ = json.Unmarshal(details, &item.Details)
		}
		if item.Details == nil {
			item.Details = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanAdminMonitor(row interface{ Scan(...any) error }) (domain.AdminMonitor, error) {
	var item domain.AdminMonitor
	var publicConfig, encrypted []byte
	var heartbeatHash []byte
	if err := row.Scan(&item.ID, &item.Name, &item.Kind, &item.Target, &item.IntervalSeconds, &item.TimeoutMS, &item.Enabled, &publicConfig, &item.Status, &item.LastCheckedAt, &item.LastSuccessAt, &item.LastFailureAt, &item.LastLatencyMS, &item.LastStatusCode, &item.LastError, &item.LastHeartbeatAt, &item.NextCheckAt, &encrypted, &heartbeatHash, &item.CreatedByAccountID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.AdminMonitor{}, err
	}
	if len(publicConfig) > 0 && json.Valid(publicConfig) {
		if err := json.Unmarshal(publicConfig, &item.PublicConfig); err != nil {
			return domain.AdminMonitor{}, err
		}
	}
	if item.PublicConfig == nil {
		item.PublicConfig = map[string]any{}
	}
	item.SecretConfigured = len(encrypted) > 0
	return item, nil
}

func nullableHeartbeatHash(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func validateAdminMonitorInput(id uuid.UUID, input AdminMonitorInput) error {
	if id == uuid.Nil || !validAdminMonitorText(input.Name, 1, 120) {
		return ErrInvalidAdminMonitor
	}
	// Synthetic browser checks need a dedicated, sandboxed Playwright runner.
	// Do not persist them as if the ordinary network worker could execute them.
	if input.Kind != "http" && input.Kind != "tcp" && input.Kind != "dns" && input.Kind != "tls" && input.Kind != "heartbeat" {
		return ErrInvalidAdminMonitor
	}
	if strings.TrimSpace(input.Target) == "" || utf8.RuneCountInString(input.Target) > 2048 || strings.ContainsAny(input.Target, "\x00\r\n") || input.IntervalSeconds < 5 || input.IntervalSeconds > 86400 || input.TimeoutMS < 100 || input.TimeoutMS > 120000 {
		return ErrInvalidAdminMonitor
	}
	if len(input.SecretConfig) == 0 || len(input.SecretConfig) > adminMonitorMaxConfig || len(input.PublicConfig) > adminMonitorMaxConfig || !json.Valid(input.PublicConfig) || !json.Valid(input.SecretConfig) {
		return ErrInvalidAdminMonitor
	}
	return nil
}

func validAdminMonitorText(value string, minimum, maximum int) bool {
	value = strings.TrimSpace(value)
	return utf8.RuneCountInString(value) >= minimum && utf8.RuneCountInString(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func normalizeAdminMonitorError(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\x00' || r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, value))
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}

// inputInterval keeps the monitor schedule authoritative in the row rather
// than trusting a worker-provided interval. It is read under the same row lock
// held by CompleteAdminMonitorCheck.
func inputInterval(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID) int {
	var interval int
	if err := tx.QueryRow(ctx, `SELECT interval_seconds FROM admin_monitors WHERE id=$1`, monitorID).Scan(&interval); err != nil || interval < 5 {
		return 5
	}
	return interval
}

func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}

func requireInstanceAdminTx(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) error {
	if accountID == uuid.Nil {
		return ErrForbidden
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_roles WHERE account_id=$1 AND role IN ('instance_owner','instance_admin'))`, accountID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func writeInstanceAuditTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID, action, targetType string, target uuid.UUID, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events (id,organization_id,actor_account_id,action,target_type,target_id,metadata) VALUES ($1,NULL,$2,$3,$4,$5,$6)`, id, actor, action, targetType, target, encoded)
	return err
}
