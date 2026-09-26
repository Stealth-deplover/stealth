package repository

import (
	"context"

	"github.com/google/uuid"
)

// ListProtectedAppRuntimeDeploymentIDs returns selected candidate deployments,
// including deployments selected by disabled Apps, plus whether a live runtime
// lease may be importing a stale job snapshot. Results are limited to the
// bounded cache inventory; a live lease makes collection skip its whole pass.
func (r *Repository) ListProtectedAppRuntimeDeploymentIDs(ctx context.Context, candidates []uuid.UUID) ([]uuid.UUID, bool, error) {
	if r == nil || r.pool == nil || len(candidates) < 1 || len(candidates) > 256 {
		return nil, false, ErrInvalidAppRuntimeJob
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	candidateStrings := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if id == uuid.Nil || id.Version() != uuid.Version(7) {
			return nil, false, ErrInvalidAppRuntimeJob
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, false, ErrInvalidAppRuntimeJob
		}
		seen[id] = struct{}{}
		candidateStrings = append(candidateStrings, id.String())
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT desired_deployment_id
		FROM project_apps
		WHERE desired_deployment_id = ANY($1::uuid[])
		ORDER BY desired_deployment_id`, candidateStrings)
	if err != nil {
		return nil, false, err
	}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, err
		}
		if id == uuid.Nil {
			rows.Close()
			return nil, false, ErrInvalidAppRuntimeJob
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()
	var liveRuntimeLease bool
	err = r.pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM app_runtime_state
		  WHERE worker_id IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at>now()
		)`).Scan(&liveRuntimeLease)
	if err != nil {
		return nil, false, err
	}
	return ids, liveRuntimeLease, nil
}

// PruneAppRuntimeCleanupJobs deletes only bounded batches of old terminal
// cleanup history. Completed rows are kept for 14 days and failed rows for 90
// days so operators retain a useful diagnosis window.
func (r *Repository) PruneAppRuntimeCleanupJobs(ctx context.Context, batchSize int) (int64, error) {
	if r == nil || r.pool == nil || batchSize < 1 || batchSize > 1000 {
		return 0, ErrInvalidAppRuntimeJob
	}
	result, err := r.pool.Exec(ctx, `
		DELETE FROM app_runtime_cleanup_jobs
		WHERE id IN (
		  SELECT id FROM app_runtime_cleanup_jobs
		  WHERE (status='completed' AND completed_at < now()-interval '14 days')
		     OR (status='failed' AND updated_at < now()-interval '90 days')
		  ORDER BY updated_at,id
		  LIMIT $1
		  FOR UPDATE SKIP LOCKED
		)`, batchSize)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
