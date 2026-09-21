package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	adminAlertMaxLimit       = 100
	adminAlertMaxCondition   = 32 << 10
	adminIncidentMaxLimit    = 100
	adminIncidentMaxServices = 32
	adminDashboardMaxLimit   = 100
	adminDashboardMaxDef     = 256 << 10
)

var (
	ErrInvalidAdminAlert     = errors.New("invalid admin alert")
	ErrInvalidAdminIncident  = errors.New("invalid admin incident")
	ErrInvalidAdminDashboard = errors.New("invalid admin dashboard")
	ErrInvalidAdminStatus    = errors.New("invalid admin status page")
)

type AdminAlertRuleInput struct {
	Name       string
	Kind       string
	Condition  json.RawMessage
	Severity   string
	ForSeconds int
	Enabled    bool
}

type AdminAlertRulePatch struct {
	Name       *string
	Kind       *string
	Condition  json.RawMessage
	Severity   *string
	ForSeconds *int
	Enabled    *bool
}

type AdminIncidentInput struct {
	Title    string
	Severity string
	Status   string
	Services []string
	Message  string
}

type AdminIncidentPatch struct {
	Title    *string
	Severity *string
	Status   *string
	Services *[]string
	Message  *string
}

type AdminIncidentEventInput struct {
	Kind    string
	Message string
}

type AdminDashboardInput struct {
	Name        string
	Description string
	Definition  json.RawMessage
}

type AdminStatusPageInput struct {
	Name               string
	Description        string
	IsPublic           bool
	Components         json.RawMessage
	PublishedIncidents []string
}

const adminAlertRuleProjection = `
	r.id::text,r.name,r.kind,r.condition,r.severity,r.for_seconds,r.enabled,r.state,
	r.pending_since,r.last_evaluated_at,r.last_value,r.last_error,
	r.created_by_account_id::text,r.created_at,r.updated_at`

func (r *Repository) ListAdminAlertRules(ctx context.Context, limit int) ([]domain.AdminAlertRule, error) {
	if limit < 1 || limit > adminAlertMaxLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidAdminAlert, adminAlertMaxLimit)
	}
	rows, err := r.pool.Query(ctx, `SELECT `+adminAlertRuleProjection+` FROM admin_alert_rules r ORDER BY r.updated_at DESC,r.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AdminAlertRule, 0, limit)
	for rows.Next() {
		item, scanErr := scanAdminAlertRule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) AdminAlertRuleByID(ctx context.Context, id uuid.UUID) (domain.AdminAlertRule, error) {
	if id == uuid.Nil {
		return domain.AdminAlertRule{}, ErrNotFound
	}
	item, err := scanAdminAlertRule(r.pool.QueryRow(ctx, `SELECT `+adminAlertRuleProjection+` FROM admin_alert_rules r WHERE r.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminAlertRule{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateAdminAlertRule(ctx context.Context, accountID, id uuid.UUID, input AdminAlertRuleInput) (domain.AdminAlertRule, error) {
	normalized, err := normalizeAdminAlertRuleInput(input)
	if err != nil {
		return domain.AdminAlertRule{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminAlertRule{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminAlertRule{}, err
	}
	if err := validateAdminAlertMonitorReferenceTx(ctx, tx, normalized.Kind, normalized.Condition); err != nil {
		return domain.AdminAlertRule{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO admin_alert_rules (id,name,kind,condition,severity,for_seconds,enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, normalized.Name, normalized.Kind, normalized.Condition, normalized.Severity, normalized.ForSeconds, normalized.Enabled); err != nil {
		return domain.AdminAlertRule{}, mapError(err)
	}
	item, err := scanAdminAlertRule(tx.QueryRow(ctx, `SELECT `+adminAlertRuleProjection+` FROM admin_alert_rules r WHERE r.id=$1`, id))
	if err != nil {
		return domain.AdminAlertRule{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.alert.create", "admin_alert_rule", id, map[string]any{"kind": normalized.Kind, "severity": normalized.Severity}); err != nil {
		return domain.AdminAlertRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminAlertRule{}, err
	}
	return item, nil
}

func (r *Repository) UpdateAdminAlertRule(ctx context.Context, accountID, id uuid.UUID, patch AdminAlertRulePatch) (domain.AdminAlertRule, error) {
	normalized, err := normalizeAdminAlertRulePatch(patch)
	if err != nil {
		return domain.AdminAlertRule{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminAlertRule{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminAlertRule{}, err
	}
	var current domain.AdminAlertRule
	current, err = scanAdminAlertRule(tx.QueryRow(ctx, `SELECT `+adminAlertRuleProjection+` FROM admin_alert_rules r WHERE r.id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminAlertRule{}, ErrNotFound
	}
	if err != nil {
		return domain.AdminAlertRule{}, err
	}
	name := current.Name
	if normalized.Name != nil {
		name = *normalized.Name
	}
	kind := current.Kind
	condition, err := json.Marshal(current.Condition)
	if err != nil {
		return domain.AdminAlertRule{}, ErrInvalidAdminAlert
	}
	if normalized.Kind != nil {
		kind = *normalized.Kind
	}
	if normalized.Condition != nil {
		condition = append(json.RawMessage(nil), normalized.Condition...)
	}
	if err := validateAdminAlertCondition(kind, condition); err != nil {
		return domain.AdminAlertRule{}, err
	}
	if err := validateAdminAlertMonitorReferenceTx(ctx, tx, kind, condition); err != nil {
		return domain.AdminAlertRule{}, err
	}
	severity := current.Severity
	if normalized.Severity != nil {
		severity = *normalized.Severity
	}
	forSeconds := current.ForSeconds
	if normalized.ForSeconds != nil {
		forSeconds = *normalized.ForSeconds
	}
	enabled := current.Enabled
	if normalized.Enabled != nil {
		enabled = *normalized.Enabled
	}
	_, err = tx.Exec(ctx, `
		UPDATE admin_alert_rules
		SET name=$2,kind=$3,condition=$4,severity=$5,for_seconds=$6,enabled=$7,
		    state=CASE WHEN $7 THEN CASE WHEN state='muted' THEN 'normal' ELSE state END ELSE 'muted' END,
		    pending_since=CASE WHEN $7 THEN pending_since ELSE NULL END,updated_at=now()
		WHERE id=$1`, id, name, kind, condition, severity, forSeconds, enabled)
	if err != nil {
		return domain.AdminAlertRule{}, mapError(err)
	}
	item, err := scanAdminAlertRule(tx.QueryRow(ctx, `SELECT `+adminAlertRuleProjection+` FROM admin_alert_rules r WHERE r.id=$1`, id))
	if err != nil {
		return domain.AdminAlertRule{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.alert.update", "admin_alert_rule", id, map[string]any{"kind": kind, "enabled": enabled}); err != nil {
		return domain.AdminAlertRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminAlertRule{}, err
	}
	return item, nil
}

func (r *Repository) DeleteAdminAlertRule(ctx context.Context, accountID, id uuid.UUID) error {
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
	var name, kind, severity string
	if err := tx.QueryRow(ctx, `
		SELECT name,kind,severity
		FROM admin_alert_rules
		WHERE id=$1
		FOR UPDATE`, id).Scan(&name, &kind, &severity); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM admin_alert_rules WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.alert.delete", "admin_alert_rule", id, map[string]any{
		"name":     name,
		"kind":     kind,
		"severity": severity,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListAdminAlertEvents(ctx context.Context, ruleID uuid.UUID, limit int) ([]domain.AdminAlertEvent, error) {
	page, err := r.QueryAdminAlertEvents(ctx, AdminAlertEventQuery{RuleID: &ruleID, Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (r *Repository) ListRecentAdminAlertEvents(ctx context.Context, limit int) ([]domain.AdminAlertEvent, error) {
	page, err := r.QueryAdminAlertEvents(ctx, AdminAlertEventQuery{Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func scanAdminAlertRule(row interface{ Scan(...any) error }) (domain.AdminAlertRule, error) {
	var item domain.AdminAlertRule
	var condition []byte
	if err := row.Scan(&item.ID, &item.Name, &item.Kind, &condition, &item.Severity, &item.ForSeconds, &item.Enabled, &item.State, &item.PendingSince, &item.LastEvaluatedAt, &item.LastValue, &item.LastError, &item.CreatedByAccountID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.AdminAlertRule{}, err
	}
	if !json.Valid(condition) || json.Unmarshal(condition, &item.Condition) != nil {
		return domain.AdminAlertRule{}, ErrInvalidAdminAlert
	}
	if item.Condition == nil {
		item.Condition = map[string]any{}
	}
	return item, nil
}

func normalizeAdminAlertRuleInput(input AdminAlertRuleInput) (AdminAlertRuleInput, error) {
	name, err := normalizeAdminControlText(input.Name, 1, 120)
	if err != nil {
		return AdminAlertRuleInput{}, fmt.Errorf("%w: name is invalid", ErrInvalidAdminAlert)
	}
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	if !validAdminAlertKind(kind) {
		return AdminAlertRuleInput{}, fmt.Errorf("%w: alert kind is unsupported", ErrInvalidAdminAlert)
	}
	severity, err := normalizeAdminSeverity(input.Severity)
	if err != nil {
		return AdminAlertRuleInput{}, fmt.Errorf("%w: severity is invalid", ErrInvalidAdminAlert)
	}
	if input.ForSeconds < 0 || input.ForSeconds > 86400 {
		return AdminAlertRuleInput{}, fmt.Errorf("%w: for_seconds is invalid", ErrInvalidAdminAlert)
	}
	if err := validateAdminAlertCondition(kind, input.Condition); err != nil {
		return AdminAlertRuleInput{}, err
	}
	return AdminAlertRuleInput{Name: name, Kind: kind, Condition: append(json.RawMessage(nil), input.Condition...), Severity: severity, ForSeconds: input.ForSeconds, Enabled: input.Enabled}, nil
}

func normalizeAdminAlertRulePatch(patch AdminAlertRulePatch) (AdminAlertRulePatch, error) {
	if patch.Name == nil && patch.Kind == nil && patch.Condition == nil && patch.Severity == nil && patch.ForSeconds == nil && patch.Enabled == nil {
		return AdminAlertRulePatch{}, fmt.Errorf("%w: at least one field is required", ErrInvalidAdminAlert)
	}
	if patch.Name != nil {
		value, err := normalizeAdminControlText(*patch.Name, 1, 120)
		if err != nil {
			return AdminAlertRulePatch{}, ErrInvalidAdminAlert
		}
		patch.Name = &value
	}
	if patch.Kind != nil {
		value := strings.ToLower(strings.TrimSpace(*patch.Kind))
		if !validAdminAlertKind(value) {
			return AdminAlertRulePatch{}, ErrInvalidAdminAlert
		}
		patch.Kind = &value
	}
	if patch.Condition != nil && len(patch.Condition) == 0 {
		return AdminAlertRulePatch{}, ErrInvalidAdminAlert
	}
	if patch.Severity != nil {
		value, err := normalizeAdminSeverity(*patch.Severity)
		if err != nil {
			return AdminAlertRulePatch{}, ErrInvalidAdminAlert
		}
		patch.Severity = &value
	}
	if patch.ForSeconds != nil && (*patch.ForSeconds < 0 || *patch.ForSeconds > 86400) {
		return AdminAlertRulePatch{}, ErrInvalidAdminAlert
	}
	return patch, nil
}

func validateAdminAlertCondition(kind string, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > adminAlertMaxCondition || !json.Valid(raw) {
		return fmt.Errorf("%w: condition must be a bounded JSON object", ErrInvalidAdminAlert)
	}
	var condition map[string]any
	if err := json.Unmarshal(raw, &condition); err != nil || condition == nil {
		return fmt.Errorf("%w: condition must be a JSON object", ErrInvalidAdminAlert)
	}
	if _, forbidden := condition["sql"]; forbidden {
		return fmt.Errorf("%w: arbitrary SQL is not accepted", ErrInvalidAdminAlert)
	}
	switch kind {
	case "metric_threshold", "error_rate", "latency", "log_match", "service_health", "disk_pressure":
		if !conditionHasNumber(condition, "threshold") || !conditionHasString(condition, "operator", "gt", "gte", "lt", "lte") {
			return fmt.Errorf("%w: telemetry alerts require operator and numeric threshold", ErrInvalidAdminAlert)
		}
		if err := validateTelemetryAlertCondition(kind, condition); err != nil {
			return err
		}
	case "monitor_failure", "heartbeat_failure":
		if !conditionHasUUID(condition, "monitor_id") {
			return fmt.Errorf("%w: monitor alerts require monitor_id", ErrInvalidAdminAlert)
		}
	case "certificate_expiry":
		if !conditionHasUUID(condition, "monitor_id") || !positiveCertificateThresholds(condition) {
			return fmt.Errorf("%w: certificate alerts require monitor_id and days", ErrInvalidAdminAlert)
		}
	default:
		return fmt.Errorf("%w: alert kind is unsupported", ErrInvalidAdminAlert)
	}
	return nil
}

func validAdminAlertKind(value string) bool {
	switch value {
	case "metric_threshold", "error_rate", "latency", "log_match", "service_health", "disk_pressure", "monitor_failure", "heartbeat_failure", "certificate_expiry":
		return true
	default:
		return false
	}
}

func validateTelemetryAlertCondition(kind string, condition map[string]any) error {
	if value, ok := condition["window_seconds"]; ok {
		window, valid := value.(float64)
		if !valid || window < 30 || window > 24*60*60 || math.Trunc(window) != window {
			return fmt.Errorf("%w: telemetry alert window_seconds is invalid", ErrInvalidAdminAlert)
		}
	}
	if value, ok := condition["service"]; ok && (!isString(value) || !validAdminControlText(strings.TrimSpace(value.(string)), 1, 128)) {
		return fmt.Errorf("%w: service filter is invalid", ErrInvalidAdminAlert)
	}
	switch kind {
	case "metric_threshold":
		if !conditionHasBoundedString(condition, "metric", 256) {
			return fmt.Errorf("%w: metric threshold alerts require metric", ErrInvalidAdminAlert)
		}
		if value, ok := condition["aggregation"]; ok && (!isString(value) || !conditionHasString(condition, "aggregation", "avg", "min", "max", "sum", "latest")) {
			return fmt.Errorf("%w: metric aggregation is invalid", ErrInvalidAdminAlert)
		}
	case "latency":
		if value, ok := condition["percentile"]; ok && (!isString(value) || !conditionHasString(condition, "percentile", "p50", "p95", "p99")) {
			return fmt.Errorf("%w: latency percentile is invalid", ErrInvalidAdminAlert)
		}
	case "log_match":
		if !conditionHasBoundedString(condition, "search", 256) && !conditionHasBoundedString(condition, "message", 256) {
			return fmt.Errorf("%w: log match alerts require search text", ErrInvalidAdminAlert)
		}
		if value, ok := condition["level"]; ok && (!isString(value) || !validAdminControlText(strings.TrimSpace(value.(string)), 1, 64)) {
			return fmt.Errorf("%w: log level is invalid", ErrInvalidAdminAlert)
		}
	case "service_health":
		if !conditionHasBoundedString(condition, "service", 128) {
			return fmt.Errorf("%w: service health alerts require service", ErrInvalidAdminAlert)
		}
	case "disk_pressure":
		if value, ok := condition["metric"]; ok && (!isString(value) || !validAdminControlText(strings.TrimSpace(value.(string)), 1, 256)) {
			return fmt.Errorf("%w: disk metric is invalid", ErrInvalidAdminAlert)
		}
	}
	return nil
}

func conditionHasNumber(condition map[string]any, key string) bool {
	value, ok := condition[key]
	if !ok {
		return false
	}
	number, ok := value.(float64)
	return ok && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func conditionHasPositiveNumber(condition map[string]any, key string) bool {
	value, ok := condition[key]
	if !ok {
		return false
	}
	number, ok := value.(float64)
	return ok && number > 0 && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func positiveCertificateThresholds(condition map[string]any) bool {
	hasThreshold := false
	for _, key := range []string{"days", "threshold"} {
		if _, exists := condition[key]; !exists {
			continue
		}
		hasThreshold = true
		if !conditionHasPositiveNumber(condition, key) {
			return false
		}
	}
	return hasThreshold
}

func validateAdminAlertMonitorReferenceTx(ctx context.Context, tx pgx.Tx, kind string, raw json.RawMessage) error {
	if kind != "monitor_failure" && kind != "heartbeat_failure" && kind != "certificate_expiry" {
		return nil
	}
	var condition map[string]any
	if err := json.Unmarshal(raw, &condition); err != nil {
		return fmt.Errorf("%w: monitor condition is invalid", ErrInvalidAdminAlert)
	}
	monitorIDValue, ok := condition["monitor_id"].(string)
	if !ok {
		return fmt.Errorf("%w: monitor_id is invalid", ErrInvalidAdminAlert)
	}
	monitorID, err := uuid.Parse(strings.TrimSpace(monitorIDValue))
	if err != nil || monitorID == uuid.Nil {
		return fmt.Errorf("%w: monitor_id is invalid", ErrInvalidAdminAlert)
	}
	var monitorKind string
	err = tx.QueryRow(ctx, `SELECT kind FROM admin_monitors WHERE id=$1 FOR UPDATE`, monitorID).Scan(&monitorKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: monitor does not exist", ErrInvalidAdminAlert)
	}
	if err != nil {
		return err
	}
	switch kind {
	case "heartbeat_failure":
		if monitorKind != "heartbeat" {
			return fmt.Errorf("%w: heartbeat_failure requires a heartbeat monitor", ErrInvalidAdminAlert)
		}
	case "certificate_expiry":
		if monitorKind != "tls" {
			return fmt.Errorf("%w: certificate_expiry requires a TLS monitor", ErrInvalidAdminAlert)
		}
	}
	return nil
}

func conditionHasString(condition map[string]any, key string, values ...string) bool {
	value, ok := condition[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return false
	}
	for _, allowed := range values {
		if value == allowed {
			return true
		}
	}
	return false
}

func conditionHasUUID(condition map[string]any, key string) bool {
	value, ok := condition[key].(string)
	if !ok {
		return false
	}
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil && parsed != uuid.Nil
}

func conditionHasBoundedString(condition map[string]any, key string, maximum int) bool {
	value, ok := condition[key].(string)
	return ok && validAdminControlText(value, 1, maximum)
}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

func normalizeAdminSeverity(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "info" && value != "warning" && value != "critical" {
		return "", errors.New("invalid severity")
	}
	return value, nil
}

func (r *Repository) ListAdminIncidents(ctx context.Context, limit int) ([]domain.AdminIncident, error) {
	if limit < 1 || limit > adminIncidentMaxLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidAdminIncident, adminIncidentMaxLimit)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT i.id::text,i.title,i.severity,i.status,i.services,i.started_at,i.resolved_at,
		       i.created_by_account_id::text,i.created_at,i.updated_at
		FROM admin_incidents i ORDER BY i.started_at DESC,i.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AdminIncident, 0, limit)
	for rows.Next() {
		item, scanErr := scanAdminIncident(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) AdminIncidentByID(ctx context.Context, id uuid.UUID) (domain.AdminIncident, error) {
	if id == uuid.Nil {
		return domain.AdminIncident{}, ErrNotFound
	}
	item, err := scanAdminIncident(r.pool.QueryRow(ctx, `
		SELECT i.id::text,i.title,i.severity,i.status,i.services,i.started_at,i.resolved_at,
		       i.created_by_account_id::text,i.created_at,i.updated_at
		FROM admin_incidents i WHERE i.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminIncident{}, ErrNotFound
	}
	if err != nil {
		return domain.AdminIncident{}, err
	}
	if err := loadAdminIncidentEvents(ctx, r.pool, &item); err != nil {
		return domain.AdminIncident{}, err
	}
	return item, nil
}

func (r *Repository) CreateAdminIncident(ctx context.Context, accountID, id uuid.UUID, input AdminIncidentInput) (domain.AdminIncident, error) {
	normalized, err := normalizeAdminIncidentInput(input)
	if err != nil {
		return domain.AdminIncident{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminIncident{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminIncident{}, err
	}
	var resolvedAt any
	if normalized.Status == "resolved" {
		resolvedAt = time.Now().UTC()
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO admin_incidents (id,title,severity,status,services,resolved_at,created_by_account_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, normalized.Title, normalized.Severity, normalized.Status, normalized.Services, resolvedAt, accountID)
	if err != nil {
		return domain.AdminIncident{}, mapError(err)
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return domain.AdminIncident{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_incident_events (id,incident_id,kind,message,actor_account_id) VALUES ($1,$2,'note',$3,$4)`, eventID, id, normalized.Message, accountID)
	if err != nil {
		return domain.AdminIncident{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.incident.create", "admin_incident", id, map[string]any{"severity": normalized.Severity, "status": normalized.Status}); err != nil {
		return domain.AdminIncident{}, err
	}
	item, err := scanAdminIncident(tx.QueryRow(ctx, `
		SELECT i.id::text,i.title,i.severity,i.status,i.services,i.started_at,i.resolved_at,
		       i.created_by_account_id::text,i.created_at,i.updated_at FROM admin_incidents i WHERE i.id=$1`, id))
	if err != nil {
		return domain.AdminIncident{}, err
	}
	if err := loadAdminIncidentEvents(ctx, tx, &item); err != nil {
		return domain.AdminIncident{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminIncident{}, err
	}
	return item, nil
}

func (r *Repository) UpdateAdminIncident(ctx context.Context, accountID, id uuid.UUID, patch AdminIncidentPatch) (domain.AdminIncident, error) {
	normalized, err := normalizeAdminIncidentPatch(patch)
	if err != nil {
		return domain.AdminIncident{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminIncident{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminIncident{}, err
	}
	item, err := scanAdminIncident(tx.QueryRow(ctx, `
		SELECT i.id::text,i.title,i.severity,i.status,i.services,i.started_at,i.resolved_at,
		       i.created_by_account_id::text,i.created_at,i.updated_at FROM admin_incidents i WHERE i.id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminIncident{}, ErrNotFound
	}
	if err != nil {
		return domain.AdminIncident{}, err
	}
	title := item.Title
	if normalized.Title != nil {
		title = *normalized.Title
	}
	severity := item.Severity
	if normalized.Severity != nil {
		severity = *normalized.Severity
	}
	status := item.Status
	if normalized.Status != nil {
		status = *normalized.Status
	}
	services := item.Services
	if normalized.Services != nil {
		services = *normalized.Services
	}
	message := "Incident updated from the admin console."
	if normalized.Message != nil && *normalized.Message != "" {
		message = *normalized.Message
	}
	var resolvedAt any
	if status == "resolved" {
		resolvedAt = time.Now().UTC()
	}
	_, err = tx.Exec(ctx, `UPDATE admin_incidents SET title=$2,severity=$3,status=$4,services=$5,resolved_at=$6,updated_at=now() WHERE id=$1`, id, title, severity, status, services, resolvedAt)
	if err != nil {
		return domain.AdminIncident{}, mapError(err)
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return domain.AdminIncident{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_incident_events (id,incident_id,kind,message,actor_account_id) VALUES ($1,$2,'configuration',$3,$4)`, eventID, id, message, accountID)
	if err != nil {
		return domain.AdminIncident{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.incident.update", "admin_incident", id, map[string]any{"status": status}); err != nil {
		return domain.AdminIncident{}, err
	}
	item, err = scanAdminIncident(tx.QueryRow(ctx, `SELECT i.id::text,i.title,i.severity,i.status,i.services,i.started_at,i.resolved_at,i.created_by_account_id::text,i.created_at,i.updated_at FROM admin_incidents i WHERE i.id=$1`, id))
	if err != nil {
		return domain.AdminIncident{}, err
	}
	if err := loadAdminIncidentEvents(ctx, tx, &item); err != nil {
		return domain.AdminIncident{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminIncident{}, err
	}
	return item, nil
}

func (r *Repository) AddAdminIncidentEvent(ctx context.Context, accountID, incidentID, eventID uuid.UUID, input AdminIncidentEventInput) (domain.AdminIncidentEvent, error) {
	kind, message, err := normalizeAdminIncidentEvent(input)
	if err != nil {
		return domain.AdminIncidentEvent{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminIncidentEvent{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminIncidentEvent{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM admin_incidents WHERE id=$1)`, incidentID).Scan(&exists); err != nil {
		return domain.AdminIncidentEvent{}, err
	}
	if !exists {
		return domain.AdminIncidentEvent{}, ErrNotFound
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_incident_events (id,incident_id,kind,message,actor_account_id) VALUES ($1,$2,$3,$4,$5)`, eventID, incidentID, kind, message, accountID)
	if err != nil {
		return domain.AdminIncidentEvent{}, mapError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE admin_incidents SET updated_at=now() WHERE id=$1`, incidentID)
	if err != nil {
		return domain.AdminIncidentEvent{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.incident.note", "admin_incident", incidentID, map[string]any{"kind": kind}); err != nil {
		return domain.AdminIncidentEvent{}, err
	}
	var item domain.AdminIncidentEvent
	err = tx.QueryRow(ctx, `SELECT id::text,incident_id::text,kind,message,actor_account_id::text,created_at FROM admin_incident_events WHERE id=$1`, eventID).Scan(&item.ID, &item.IncidentID, &item.Kind, &item.Message, &item.ActorAccountID, &item.CreatedAt)
	if err != nil {
		return domain.AdminIncidentEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminIncidentEvent{}, err
	}
	return item, nil
}

func scanAdminIncident(row interface{ Scan(...any) error }) (domain.AdminIncident, error) {
	var item domain.AdminIncident
	if err := row.Scan(&item.ID, &item.Title, &item.Severity, &item.Status, &item.Services, &item.StartedAt, &item.ResolvedAt, &item.CreatedByAccountID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.AdminIncident{}, err
	}
	if item.Services == nil {
		item.Services = []string{}
	}
	item.Events = []domain.AdminIncidentEvent{}
	return item, nil
}

func loadAdminIncidentEvents(ctx context.Context, queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, item *domain.AdminIncident) error {
	rows, err := queryer.Query(ctx, `SELECT id::text,incident_id::text,kind,message,actor_account_id::text,created_at FROM admin_incident_events WHERE incident_id=$1 ORDER BY created_at ASC,id ASC`, uuid.MustParse(item.ID))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var event domain.AdminIncidentEvent
		if err := rows.Scan(&event.ID, &event.IncidentID, &event.Kind, &event.Message, &event.ActorAccountID, &event.CreatedAt); err != nil {
			return err
		}
		item.Events = append(item.Events, event)
	}
	return rows.Err()
}

func normalizeAdminIncidentInput(input AdminIncidentInput) (AdminIncidentInput, error) {
	title, err := normalizeAdminControlText(input.Title, 3, 240)
	if err != nil {
		return AdminIncidentInput{}, fmt.Errorf("%w: title is invalid", ErrInvalidAdminIncident)
	}
	severity, err := normalizeAdminSeverity(input.Severity)
	if err != nil {
		return AdminIncidentInput{}, ErrInvalidAdminIncident
	}
	status := strings.ToLower(strings.TrimSpace(input.Status))
	if status == "" {
		status = "investigating"
	}
	if !validAdminIncidentStatus(status) {
		return AdminIncidentInput{}, ErrInvalidAdminIncident
	}
	services, err := normalizeAdminIncidentServices(input.Services)
	if err != nil {
		return AdminIncidentInput{}, err
	}
	message := strings.TrimSpace(input.Message)
	if message == "" {
		message = "Incident opened manually from the admin console."
	}
	if !validAdminControlText(message, 1, 2000) {
		return AdminIncidentInput{}, ErrInvalidAdminIncident
	}
	return AdminIncidentInput{Title: title, Severity: severity, Status: status, Services: services, Message: message}, nil
}

func normalizeAdminIncidentPatch(patch AdminIncidentPatch) (AdminIncidentPatch, error) {
	if patch.Title == nil && patch.Severity == nil && patch.Status == nil && patch.Services == nil && patch.Message == nil {
		return AdminIncidentPatch{}, ErrInvalidAdminIncident
	}
	if patch.Title != nil {
		value, err := normalizeAdminControlText(*patch.Title, 3, 240)
		if err != nil {
			return AdminIncidentPatch{}, ErrInvalidAdminIncident
		}
		patch.Title = &value
	}
	if patch.Severity != nil {
		value, err := normalizeAdminSeverity(*patch.Severity)
		if err != nil {
			return AdminIncidentPatch{}, ErrInvalidAdminIncident
		}
		patch.Severity = &value
	}
	if patch.Status != nil {
		value := strings.ToLower(strings.TrimSpace(*patch.Status))
		if !validAdminIncidentStatus(value) {
			return AdminIncidentPatch{}, ErrInvalidAdminIncident
		}
		patch.Status = &value
	}
	if patch.Services != nil {
		value, err := normalizeAdminIncidentServices(*patch.Services)
		if err != nil {
			return AdminIncidentPatch{}, err
		}
		patch.Services = &value
	}
	if patch.Message != nil && *patch.Message != "" && !validAdminControlText(*patch.Message, 1, 2000) {
		return AdminIncidentPatch{}, ErrInvalidAdminIncident
	}
	return patch, nil
}

func normalizeAdminIncidentEvent(input AdminIncidentEventInput) (string, string, error) {
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	valid := map[string]bool{"alert": true, "deployment": true, "restart": true, "backup": true, "configuration": true, "monitor": true, "note": true}
	if !valid[kind] || !validAdminControlText(input.Message, 1, 2000) {
		return "", "", ErrInvalidAdminIncident
	}
	return kind, strings.TrimSpace(input.Message), nil
}

func normalizeAdminIncidentServices(values []string) ([]string, error) {
	if len(values) < 1 || len(values) > adminIncidentMaxServices {
		return nil, ErrInvalidAdminIncident
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validAdminControlText(value, 1, 128) {
			return nil, ErrInvalidAdminIncident
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return nil, ErrInvalidAdminIncident
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func validAdminIncidentStatus(value string) bool {
	switch value {
	case "investigating", "identified", "monitoring", "resolved":
		return true
	default:
		return false
	}
}

func (r *Repository) ListAdminDashboards(ctx context.Context, limit int) ([]domain.AdminDashboard, error) {
	if limit < 1 || limit > adminDashboardMaxLimit {
		return nil, ErrInvalidAdminDashboard
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text,name,description,definition,created_by_account_id::text,created_at,updated_at FROM admin_dashboards ORDER BY updated_at DESC,id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AdminDashboard, 0, limit)
	for rows.Next() {
		item, scanErr := scanAdminDashboard(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) AdminDashboardByID(ctx context.Context, id uuid.UUID) (domain.AdminDashboard, error) {
	if id == uuid.Nil {
		return domain.AdminDashboard{}, ErrNotFound
	}
	item, err := scanAdminDashboard(r.pool.QueryRow(ctx, `SELECT id::text,name,description,definition,created_by_account_id::text,created_at,updated_at FROM admin_dashboards WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminDashboard{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateAdminDashboard(ctx context.Context, accountID, id uuid.UUID, input AdminDashboardInput) (domain.AdminDashboard, error) {
	normalized, err := normalizeAdminDashboardInput(input)
	if err != nil {
		return domain.AdminDashboard{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminDashboard{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminDashboard{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO admin_dashboards (id,name,description,definition,created_by_account_id) VALUES ($1,$2,$3,$4,$5)`, id, normalized.Name, normalized.Description, normalized.Definition, accountID); err != nil {
		return domain.AdminDashboard{}, mapError(err)
	}
	item, err := scanAdminDashboard(tx.QueryRow(ctx, `SELECT id::text,name,description,definition,created_by_account_id::text,created_at,updated_at FROM admin_dashboards WHERE id=$1`, id))
	if err != nil {
		return domain.AdminDashboard{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.dashboard.create", "admin_dashboard", id, map[string]any{}); err != nil {
		return domain.AdminDashboard{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminDashboard{}, err
	}
	return item, nil
}

func (r *Repository) UpdateAdminDashboard(ctx context.Context, accountID, id uuid.UUID, input AdminDashboardInput) (domain.AdminDashboard, error) {
	normalized, err := normalizeAdminDashboardInput(input)
	if err != nil {
		return domain.AdminDashboard{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminDashboard{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminDashboard{}, err
	}
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM admin_dashboards WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminDashboard{}, err
	}
	if lockedID == uuid.Nil {
		return domain.AdminDashboard{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE admin_dashboards SET name=$2,description=$3,definition=$4,updated_at=now() WHERE id=$1`, id, normalized.Name, normalized.Description, normalized.Definition); err != nil {
		return domain.AdminDashboard{}, mapError(err)
	}
	item, err := scanAdminDashboard(tx.QueryRow(ctx, `SELECT id::text,name,description,definition,created_by_account_id::text,created_at,updated_at FROM admin_dashboards WHERE id=$1`, id))
	if err != nil {
		return domain.AdminDashboard{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.dashboard.update", "admin_dashboard", id, map[string]any{}); err != nil {
		return domain.AdminDashboard{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminDashboard{}, err
	}
	return item, nil
}

func (r *Repository) DeleteAdminDashboard(ctx context.Context, accountID, id uuid.UUID) error {
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
	result, err := tx.Exec(ctx, `DELETE FROM admin_dashboards WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.dashboard.delete", "admin_dashboard", id, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanAdminDashboard(row interface{ Scan(...any) error }) (domain.AdminDashboard, error) {
	var item domain.AdminDashboard
	var definition []byte
	if err := row.Scan(&item.ID, &item.Name, &item.Description, &definition, &item.CreatedByAccountID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.AdminDashboard{}, err
	}
	if !json.Valid(definition) || json.Unmarshal(definition, &item.Definition) != nil {
		return domain.AdminDashboard{}, ErrInvalidAdminDashboard
	}
	if item.Definition == nil {
		item.Definition = map[string]any{}
	}
	return item, nil
}

func normalizeAdminDashboardInput(input AdminDashboardInput) (AdminDashboardInput, error) {
	name, err := normalizeAdminControlText(input.Name, 1, 120)
	if err != nil {
		return AdminDashboardInput{}, ErrInvalidAdminDashboard
	}
	description := strings.TrimSpace(input.Description)
	if len(description) > 2000 || strings.ContainsAny(description, "\x00\r\n") {
		return AdminDashboardInput{}, ErrInvalidAdminDashboard
	}
	if len(input.Definition) == 0 || len(input.Definition) > adminDashboardMaxDef || !json.Valid(input.Definition) {
		return AdminDashboardInput{}, ErrInvalidAdminDashboard
	}
	var definition map[string]any
	if err := json.Unmarshal(input.Definition, &definition); err != nil || definition == nil {
		return AdminDashboardInput{}, ErrInvalidAdminDashboard
	}
	if err := validateAdminDashboardDefinition(definition); err != nil {
		return AdminDashboardInput{}, err
	}
	return AdminDashboardInput{Name: name, Description: description, Definition: append(json.RawMessage(nil), input.Definition...)}, nil
}

func validateAdminDashboardDefinition(definition map[string]any) error {
	panels, ok := definition["panels"].([]any)
	if !ok || len(panels) > 64 {
		return ErrInvalidAdminDashboard
	}
	allowedTypes := map[string]bool{
		"metric": true, "time_series": true, "logs": true, "table": true,
		"stat": true, "heatmap": true, "error_groups": true,
		"monitor_status": true, "service_health": true,
	}
	for _, raw := range panels {
		panel, ok := raw.(map[string]any)
		if !ok || len(panel) > 12 || containsForbiddenDashboardKey(panel) {
			return ErrInvalidAdminDashboard
		}
		kind, ok := panel["type"].(string)
		if !ok || !allowedTypes[kind] {
			return ErrInvalidAdminDashboard
		}
		for key, value := range panel {
			switch key {
			case "id", "type", "title", "metric", "service", "level", "query":
			default:
				return ErrInvalidAdminDashboard
			}
			if text, isText := value.(string); isText && !validAdminControlText(text, 0, 512) {
				return ErrInvalidAdminDashboard
			}
		}
	}
	return nil
}

func containsForbiddenDashboardKey(value any) bool {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			key = strings.ToLower(key)
			if key == "sql" || strings.Contains(key, "password") || strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "authorization") {
				return true
			}
			if containsForbiddenDashboardKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsForbiddenDashboardKey(child) {
				return true
			}
		}
	}
	return false
}

func (r *Repository) AdminStatusPage(ctx context.Context) (domain.AdminStatusPage, error) {
	var item domain.AdminStatusPage
	var components, published []byte
	err := r.pool.QueryRow(ctx, `SELECT name,description,is_public,components,published_incidents,updated_at FROM admin_status_page_config WHERE id=TRUE`).Scan(&item.Name, &item.Description, &item.IsPublic, &components, &published, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminStatusPage{}, ErrNotFound
	}
	if err != nil {
		return domain.AdminStatusPage{}, err
	}
	if !json.Valid(components) || json.Unmarshal(components, &item.Components) != nil {
		return domain.AdminStatusPage{}, ErrInvalidAdminStatus
	}
	if item.Components == nil {
		item.Components = []map[string]any{}
	}
	if !json.Valid(published) || json.Unmarshal(published, &item.PublishedIncidents) != nil {
		return domain.AdminStatusPage{}, ErrInvalidAdminStatus
	}
	if item.PublishedIncidents == nil {
		item.PublishedIncidents = []string{}
	}
	return item, nil
}

func (r *Repository) PublicAdminStatusPage(ctx context.Context) (domain.AdminPublicStatusPage, error) {
	var page domain.AdminPublicStatusPage
	var components, published []byte
	var publishedIDs []uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT name,description,components,published_incidents,updated_at FROM admin_status_page_config WHERE id=TRUE AND is_public`).Scan(&page.Name, &page.Description, &components, &published, &page.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminPublicStatusPage{}, ErrNotFound
	}
	if err != nil {
		return domain.AdminPublicStatusPage{}, err
	}
	if !json.Valid(components) || json.Unmarshal(components, &page.Components) != nil {
		return domain.AdminPublicStatusPage{}, ErrInvalidAdminStatus
	}
	if page.Components == nil {
		page.Components = []map[string]any{}
	}
	var publishedStrings []string
	if !json.Valid(published) || json.Unmarshal(published, &publishedStrings) != nil {
		return domain.AdminPublicStatusPage{}, ErrInvalidAdminStatus
	}
	for _, value := range publishedStrings {
		id, parseErr := uuid.Parse(strings.TrimSpace(value))
		if parseErr != nil || id == uuid.Nil {
			return domain.AdminPublicStatusPage{}, ErrInvalidAdminStatus
		}
		publishedIDs = append(publishedIDs, id)
	}
	page.Incidents = []domain.AdminPublicIncident{}
	if len(publishedIDs) > 0 {
		rows, queryErr := r.pool.Query(ctx, `
			SELECT id::text,title,severity,status,services,started_at,resolved_at
			FROM admin_incidents WHERE id=ANY($1::uuid[])
			ORDER BY started_at DESC,id DESC`, publishedIDs)
		if queryErr != nil {
			return domain.AdminPublicStatusPage{}, queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var incident domain.AdminPublicIncident
			if scanErr := rows.Scan(&incident.ID, &incident.Title, &incident.Severity, &incident.Status, &incident.Services, &incident.StartedAt, &incident.ResolvedAt); scanErr != nil {
				return domain.AdminPublicStatusPage{}, scanErr
			}
			if incident.Services == nil {
				incident.Services = []string{}
			}
			page.Incidents = append(page.Incidents, incident)
		}
		if err := rows.Err(); err != nil {
			return domain.AdminPublicStatusPage{}, err
		}
	}
	return page, nil
}

func (r *Repository) UpdateAdminStatusPage(ctx context.Context, accountID uuid.UUID, input AdminStatusPageInput) (domain.AdminStatusPage, error) {
	normalized, err := normalizeAdminStatusPageInput(input)
	if err != nil {
		return domain.AdminStatusPage{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminStatusPage{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminStatusPage{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE admin_status_page_config SET name=$2,description=$3,is_public=$4,components=$5,published_incidents=$6,updated_by_account_id=$7,updated_at=now() WHERE id=TRUE`, normalized.Name, normalized.Description, normalized.IsPublic, normalized.Components, normalized.PublishedIncidents, accountID)
	if err != nil {
		return domain.AdminStatusPage{}, mapError(err)
	}
	statusID := uuid.Nil
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.status_page.update", "admin_status_page", statusID, map[string]any{"is_public": normalized.IsPublic}); err != nil {
		return domain.AdminStatusPage{}, err
	}
	var item domain.AdminStatusPage
	var components, published []byte
	if err := tx.QueryRow(ctx, `SELECT name,description,is_public,components,published_incidents,updated_at FROM admin_status_page_config WHERE id=TRUE`).Scan(&item.Name, &item.Description, &item.IsPublic, &components, &published, &item.UpdatedAt); err != nil {
		return domain.AdminStatusPage{}, err
	}
	if err := json.Unmarshal(components, &item.Components); err != nil {
		return domain.AdminStatusPage{}, err
	}
	if err := json.Unmarshal(published, &item.PublishedIncidents); err != nil {
		return domain.AdminStatusPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminStatusPage{}, err
	}
	return item, nil
}

func normalizeAdminStatusPageInput(input AdminStatusPageInput) (AdminStatusPageInput, error) {
	name, err := normalizeAdminControlText(input.Name, 1, 160)
	if err != nil {
		return AdminStatusPageInput{}, ErrInvalidAdminStatus
	}
	if len(input.Description) > 4000 || strings.ContainsAny(input.Description, "\x00\r\n") {
		return AdminStatusPageInput{}, ErrInvalidAdminStatus
	}
	if len(input.Components) == 0 || len(input.Components) > 128<<10 || !json.Valid(input.Components) {
		return AdminStatusPageInput{}, ErrInvalidAdminStatus
	}
	var components []map[string]any
	if err := json.Unmarshal(input.Components, &components); err != nil || len(components) > 64 {
		return AdminStatusPageInput{}, ErrInvalidAdminStatus
	}
	for _, component := range components {
		if err := validatePublicStatusComponent(component); err != nil {
			return AdminStatusPageInput{}, err
		}
	}
	if len(input.PublishedIncidents) > 64 {
		return AdminStatusPageInput{}, ErrInvalidAdminStatus
	}
	seen := make(map[string]struct{}, len(input.PublishedIncidents))
	published := make([]string, 0, len(input.PublishedIncidents))
	for _, value := range input.PublishedIncidents {
		parsed, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil || parsed == uuid.Nil {
			return AdminStatusPageInput{}, ErrInvalidAdminStatus
		}
		normalizedID := parsed.String()
		if _, exists := seen[normalizedID]; exists {
			return AdminStatusPageInput{}, ErrInvalidAdminStatus
		}
		seen[normalizedID] = struct{}{}
		published = append(published, normalizedID)
	}
	return AdminStatusPageInput{Name: name, Description: strings.TrimSpace(input.Description), IsPublic: input.IsPublic, Components: append(json.RawMessage(nil), input.Components...), PublishedIncidents: published}, nil
}

func validatePublicStatusComponent(component map[string]any) error {
	if component == nil || !conditionHasBoundedString(component, "name", 120) || !conditionHasBoundedString(component, "status", 32) {
		return ErrInvalidAdminStatus
	}
	allowed := map[string]bool{"name": true, "status": true, "description": true, "url": true}
	for key, value := range component {
		if !allowed[key] || strings.Contains(strings.ToLower(key), "secret") || strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "password") {
			return ErrInvalidAdminStatus
		}
		text, ok := value.(string)
		if !ok || !validAdminControlText(text, 0, 512) {
			return ErrInvalidAdminStatus
		}
		if key == "status" {
			switch text {
			case "operational", "degraded", "partial_outage", "major_outage", "maintenance":
			default:
				return ErrInvalidAdminStatus
			}
		}
		if key == "url" {
			parsed, err := url.ParseRequestURI(text)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return ErrInvalidAdminStatus
			}
		}
	}
	return nil
}

func normalizeAdminControlText(value string, minimum, maximum int) (string, error) {
	value = strings.TrimSpace(value)
	if !validAdminControlText(value, minimum, maximum) {
		return "", errors.New("invalid text")
	}
	return value, nil
}

func validAdminControlText(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}
