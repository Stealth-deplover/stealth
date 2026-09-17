package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
)

const adminOperationsMaxLimit = 100

// adminOperationProjection normalizes durable control-plane work into one
// read-only projection. The source tables remain the source of truth; this is
// intentionally not a new job system.
const adminOperationProjection = `
	SELECT id, kind, name, project_id, project_name, status,
	       started_at, finished_at, duration_ms, error_message, created_at
	FROM (
		SELECT d.id::text,
		       'function_deployment'::text AS kind,
		       f.name::text AS name,
		       d.project_id::text,
		       p.name::text AS project_name,
		       d.status::text,
		       d.build_started_at AS started_at,
		       d.finished_at,
		       CASE
				WHEN d.build_started_at IS NOT NULL AND d.finished_at IS NOT NULL
				THEN (EXTRACT(EPOCH FROM (d.finished_at - d.build_started_at)) * 1000)::bigint
				ELSE 0
		       END AS duration_ms,
		       d.error_message,
		       d.created_at
		FROM function_deployments d
		JOIN project_functions f ON f.id=d.function_id AND f.project_id=d.project_id
		JOIN projects p ON p.id=d.project_id
		UNION ALL
		SELECT d.id::text,
		       'site_deployment'::text,
		       s.name::text,
		       d.project_id::text,
		       p.name::text,
		       d.status::text,
		       d.build_started_at,
		       d.finished_at,
		       CASE
				WHEN d.build_started_at IS NOT NULL AND d.finished_at IS NOT NULL
				THEN (EXTRACT(EPOCH FROM (d.finished_at - d.build_started_at)) * 1000)::bigint
				ELSE 0
		       END,
		       d.error_message,
		       d.created_at
		FROM site_deployments d
		JOIN project_sites s ON s.id=d.site_id AND s.project_id=d.project_id
		JOIN projects p ON p.id=d.project_id
		UNION ALL
		SELECT e.id::text,
		       'function_execution'::text,
		       f.name::text,
		       e.project_id::text,
		       p.name::text,
		       e.status::text,
		       e.started_at,
		       e.finished_at,
		       CASE
				WHEN e.started_at IS NOT NULL AND e.finished_at IS NOT NULL
				THEN (EXTRACT(EPOCH FROM (e.finished_at - e.started_at)) * 1000)::bigint
				ELSE 0
		       END,
		       e.error_message,
		       e.created_at
		FROM function_executions e
		JOIN project_functions f ON f.id=e.function_id AND f.project_id=e.project_id
		JOIN projects p ON p.id=e.project_id
		UNION ALL
		SELECT r.id::text,
		       'agent_run'::text,
		       a.name::text,
		       r.project_id::text,
		       p.name::text,
		       r.status::text,
		       r.started_at,
		       r.finished_at,
		       CASE
				WHEN r.started_at IS NOT NULL AND r.finished_at IS NOT NULL
				THEN (EXTRACT(EPOCH FROM (r.finished_at - r.started_at)) * 1000)::bigint
				ELSE 0
		       END,
		       r.error_message,
		       r.created_at
		FROM agent_runs r
		JOIN project_agents a ON a.id=r.agent_id AND a.project_id=r.project_id
		JOIN projects p ON p.id=r.project_id
		UNION ALL
		SELECT c.id::text,
		       'artifact_cleanup'::text,
		       c.store_kind || ':' || c.operation,
		       c.project_id::text,
		       p.name::text,
		       c.status::text,
		       c.created_at,
		       CASE WHEN c.status='failed' THEN c.failed_at ELSE NULL END,
		       CASE
				WHEN c.failed_at IS NOT NULL
				THEN (EXTRACT(EPOCH FROM (c.failed_at - c.created_at)) * 1000)::bigint
				ELSE 0
		       END,
		       c.last_error,
		       c.created_at
		FROM artifact_cleanup_jobs c
		JOIN projects p ON p.id=c.project_id
		UNION ALL
		SELECT b.id::text,
		       'database_backup'::text,
		       d.name::text,
		       b.project_id::text,
		       p.name::text,
		       'succeeded'::text,
		       b.created_at,
		       b.created_at,
		       0::bigint,
		       NULL::text,
		       b.created_at
		FROM database_backups b
		JOIN project_databases d ON d.id=b.database_id AND d.project_id=b.project_id
		JOIN projects p ON p.id=b.project_id
	) operations
	WHERE created_at >= $1 AND created_at < $2
	ORDER BY created_at DESC, id DESC
	LIMIT $3`

func scanAdminOperation(row interface{ Scan(...any) error }) (domain.AdminOperation, error) {
	var item domain.AdminOperation
	var id, kind, name string
	var projectID, projectName *string
	if err := row.Scan(&id, &kind, &name, &projectID, &projectName, &item.Status, &item.StartedAt, &item.FinishedAt, &item.DurationMS, &item.Error, &item.CreatedAt); err != nil {
		return domain.AdminOperation{}, err
	}
	item.ID = id
	item.Kind = kind
	item.Name = name
	item.ProjectID = projectID
	item.ProjectName = projectName
	return item, nil
}

// ListAdminOperations returns durable operational records inside a bounded
// time window. It does not expose prompts, request bodies, storage paths, or
// other sensitive payloads from the source tables.
func (r *Repository) ListAdminOperations(ctx context.Context, from, to time.Time, limit int) ([]domain.AdminOperation, error) {
	if r == nil || r.pool == nil {
		return nil, ErrNotFound
	}
	if from.IsZero() || to.IsZero() || !to.After(from) || to.Sub(from) > 30*24*time.Hour {
		return nil, fmt.Errorf("%w: operation time range is invalid", ErrInvalidQuery)
	}
	if limit < 1 || limit > adminOperationsMaxLimit {
		return nil, fmt.Errorf("%w: operation limit must be between 1 and %d", ErrInvalidQuery, adminOperationsMaxLimit)
	}
	rows, err := r.pool.Query(ctx, adminOperationProjection, from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AdminOperation, 0, limit)
	for rows.Next() {
		item, scanErr := scanAdminOperation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// AdminOperationSummary derives queue state from the existing durable tables.
// Every count is a snapshot taken by one PostgreSQL statement, so the UI can
// distinguish a real empty queue from unavailable telemetry.
func (r *Repository) AdminOperationSummary(ctx context.Context) (domain.AdminOperationSummary, error) {
	if r == nil || r.pool == nil {
		return domain.AdminOperationSummary{}, ErrNotFound
	}
	var result domain.AdminOperationSummary
	err := r.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM function_deployments WHERE status IN ('queued','building')) +
		  (SELECT count(*) FROM site_deployments WHERE status='queued'),
		  (SELECT count(*) FROM function_executions WHERE status='accepted') +
		  (SELECT count(*) FROM agent_runs WHERE status='queued') +
		  (SELECT count(*) FROM artifact_cleanup_jobs WHERE status='pending'),
		  (SELECT count(*) FROM function_executions WHERE status='running') +
		  (SELECT count(*) FROM agent_runs WHERE status='running') +
		  (SELECT count(*) FROM function_deployments WHERE status='building'),
		  (SELECT count(*) FROM function_executions WHERE status='failed') +
		  (SELECT count(*) FROM agent_runs WHERE status='failed') +
		  (SELECT count(*) FROM function_deployments WHERE status='failed') +
		  (SELECT count(*) FROM site_deployments WHERE status='failed') +
		  (SELECT count(*) FROM artifact_cleanup_jobs WHERE status='failed')`,
	).Scan(&result.ActiveDeployments, &result.QueuedJobs, &result.RunningJobs, &result.FailedJobs)
	return result, err
}

const adminAuditMaxLimit = 100

// ListInstanceAuditEvents is the instance-level view of the existing audit
// ledger. Organization-scoped events are intentionally excluded; admin
// actions use a NULL organization_id and remain available after an
// organization is removed.
func (r *Repository) ListInstanceAuditEvents(ctx context.Context, limit int, before *uuid.UUID) ([]domain.AuditEvent, string, error) {
	if r == nil || r.pool == nil {
		return nil, "", ErrNotFound
	}
	if limit < 1 || limit > adminAuditMaxLimit {
		return nil, "", fmt.Errorf("%w: audit limit must be between 1 and %d", ErrInvalidQuery, adminAuditMaxLimit)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT e.id::text,e.organization_id::text,e.actor_account_id::text,a.email,
		       e.action,e.target_type,e.target_id::text,e.metadata,e.created_at
		FROM audit_events e
		LEFT JOIN accounts a ON a.id=e.actor_account_id
		WHERE e.organization_id IS NULL AND ($1::uuid IS NULL OR e.id<$1)
		ORDER BY e.id DESC
		LIMIT $2`, before, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.AuditEvent, 0, limit)
	for rows.Next() {
		var item domain.AuditEvent
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.ActorAccountID, &item.ActorEmail, &item.Action, &item.TargetType, &item.TargetID, &metadata, &item.CreatedAt); err != nil {
			return nil, "", err
		}
		if len(metadata) == 0 || !json.Valid(metadata) {
			item.Metadata = []byte(`{}`)
		} else {
			item.Metadata = metadata
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) <= limit {
		return items, "", nil
	}
	next := items[limit-1].ID
	return items[:limit], next, nil
}
