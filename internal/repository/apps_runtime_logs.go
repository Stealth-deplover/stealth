package repository

import (
	"context"
	"github.com/google/uuid"
)

// ListAppRuntimeLogSources returns only container IDs previously verified by
// successful runtime convergence. Callers cannot select a container ID, and
// the project/App authorization check happens before the private mapping is
// read. The extra row detects overflow rather than silently hiding history.
func (r *Repository) ListAppRuntimeLogSources(ctx context.Context, projectID, appID uuid.UUID, actor AppActor) ([]string, error) {
	if r == nil || r.pool == nil || projectID == uuid.Nil || appID == uuid.Nil {
		return nil, ErrNotFound
	}
	if _, err := r.requireAppRead(ctx, projectID, actor); err != nil {
		return nil, err
	}
	if _, err := appByID(ctx, r.pool, projectID, appID, false); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT container_id
		FROM app_runtime_log_sources
		WHERE project_id=$1 AND app_id=$2
		ORDER BY first_seen_at,container_id
		LIMIT $3`, projectID, appID, maxAppRuntimeLogSources+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if !validRuntimeContainerID(id) {
			return nil, ErrInvalidAppRuntimeJob
		}
		ids = append(ids, id)
		if len(ids) > maxAppRuntimeLogSources {
			return nil, ErrAppRuntimeLogSourcesLimit
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
