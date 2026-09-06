package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
)

const (
	MaxProjectServiceLayoutItems = 500
	MinProjectServiceLayoutCoord = -100000
	MaxProjectServiceLayoutCoord = 100000
)

var ErrInvalidServiceLayout = errors.New("invalid service layout")

type ProjectServiceLayoutInput struct {
	ResourceType string
	ResourceID   uuid.UUID
	X            int
	Y            int
}

// ListProjectServiceLayout returns only rows that still point at a live
// resource. Resource deletion has independent tables, so the existence filter
// prevents stale polymorphic rows from leaking into the console projection.
func (r *Repository) ListProjectServiceLayout(ctx context.Context, projectID, accountID uuid.UUID) ([]domain.ProjectServiceLayout, bool, error) {
	role, err := r.projectRole(ctx, projectID, accountID)
	if err != nil {
		return nil, false, err
	}
	rows, err := r.pool.Query(ctx, serviceLayoutListQuery, projectID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	items := make([]domain.ProjectServiceLayout, 0)
	for rows.Next() {
		var item domain.ProjectServiceLayout
		if err := rows.Scan(&item.ProjectID, &item.ResourceType, &item.ResourceID, &item.X, &item.Y, &item.UpdatedAt); err != nil {
			return nil, false, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return items, role == "owner" || role == "admin", nil
}

// ReplaceProjectServiceLayout atomically replaces the complete canvas layout.
// A whole-document write keeps drag persistence deterministic and also removes
// positions for resources deleted since the previous read.
func (r *Repository) ReplaceProjectServiceLayout(ctx context.Context, projectID, accountID uuid.UUID, inputs []ProjectServiceLayoutInput) ([]domain.ProjectServiceLayout, error) {
	if len(inputs) > MaxProjectServiceLayoutItems {
		return nil, ErrInvalidServiceLayout
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if !validServiceLayoutType(input.ResourceType) || !validServiceLayoutCoord(input.X) || !validServiceLayoutCoord(input.Y) || input.ResourceID == uuid.Nil {
			return nil, ErrInvalidServiceLayout
		}
		key := input.ResourceType + ":" + input.ResourceID.String()
		if _, exists := seen[key]; exists {
			return nil, ErrInvalidServiceLayout
		}
		seen[key] = struct{}{}
		resourceExists, err := projectServiceResourceExists(ctx, tx, projectID, input.ResourceType, input.ResourceID)
		if err != nil {
			return nil, err
		}
		if !resourceExists {
			return nil, ErrNotFound
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_service_layouts WHERE project_id=$1`, projectID); err != nil {
		return nil, err
	}
	for _, input := range inputs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_service_layouts (project_id, resource_type, resource_id, x, y)
			VALUES ($1,$2,$3,$4,$5)`, projectID, input.ResourceType, input.ResourceID, input.X, input.Y); err != nil {
			return nil, err
		}
	}
	items, err := listProjectServiceLayoutTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

const serviceLayoutListQuery = `
	SELECT l.project_id, l.resource_type, l.resource_id, l.x, l.y, l.updated_at
	FROM project_service_layouts l
	WHERE l.project_id=$1
	  AND (
		(l.resource_type='function' AND EXISTS (SELECT 1 FROM project_functions f WHERE f.project_id=l.project_id AND f.id=l.resource_id))
		OR (l.resource_type='site' AND EXISTS (SELECT 1 FROM project_sites s WHERE s.project_id=l.project_id AND s.id=l.resource_id))
		OR (l.resource_type='database' AND EXISTS (SELECT 1 FROM project_databases d WHERE d.project_id=l.project_id AND d.id=l.resource_id))
		OR (l.resource_type='storage' AND EXISTS (SELECT 1 FROM storage_buckets b WHERE b.project_id=l.project_id AND b.id=l.resource_id))
	  )
	ORDER BY l.resource_type, l.resource_id`

func listProjectServiceLayoutTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) ([]domain.ProjectServiceLayout, error) {
	rows, err := tx.Query(ctx, serviceLayoutListQuery, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.ProjectServiceLayout, 0)
	for rows.Next() {
		var item domain.ProjectServiceLayout
		if err := rows.Scan(&item.ProjectID, &item.ResourceType, &item.ResourceID, &item.X, &item.Y, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func validServiceLayoutType(resourceType string) bool {
	switch resourceType {
	case "function", "site", "database", "storage":
		return true
	default:
		return false
	}
}

func validServiceLayoutCoord(value int) bool {
	return value >= MinProjectServiceLayoutCoord && value <= MaxProjectServiceLayoutCoord
}

func projectServiceResourceExists(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, resourceType string, resourceID uuid.UUID) (bool, error) {
	var query string
	switch resourceType {
	case "function":
		query = `SELECT EXISTS (SELECT 1 FROM project_functions WHERE project_id=$1 AND id=$2)`
	case "site":
		query = `SELECT EXISTS (SELECT 1 FROM project_sites WHERE project_id=$1 AND id=$2)`
	case "database":
		query = `SELECT EXISTS (SELECT 1 FROM project_databases WHERE project_id=$1 AND id=$2)`
	case "storage":
		query = `SELECT EXISTS (SELECT 1 FROM storage_buckets WHERE project_id=$1 AND id=$2)`
	default:
		return false, ErrInvalidServiceLayout
	}
	var exists bool
	if err := tx.QueryRow(ctx, query, projectID, resourceID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
