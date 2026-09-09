package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
)

func (r *Repository) ListProjects(ctx context.Context, organizationID, accountID uuid.UUID, limit int, cursor string) ([]domain.Project, string, bool, error) {
	if err := r.requireMembership(ctx, organizationID, accountID); err != nil {
		return nil, "", false, err
	}
	var role string
	if err := r.pool.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, organizationID, accountID).Scan(&role); err != nil {
		return nil, "", false, err
	}
	canManage := role == "owner" || role == "admin" || role == "developer"
	rows, err := r.pool.Query(ctx, `SELECT id,organization_id,name,created_at FROM projects WHERE organization_id=$1 AND ($2='' OR id::text>$2) ORDER BY id LIMIT $3`, organizationID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.Project, 0, limit)
	for rows.Next() {
		var item domain.Project
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.Name, &item.CreatedAt); err != nil {
			return nil, "", false, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, canManage, nil
}
func (r *Repository) CreateProject(ctx context.Context, id, organizationID, accountID uuid.UUID, name string) (domain.Project, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Project{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireRoleTx(ctx, tx, organizationID, accountID, "owner", "admin", "developer"); err != nil {
		return domain.Project{}, err
	}
	if err := r.enforceOrganizationLimitTx(ctx, tx, organizationID, "projects"); err != nil {
		return domain.Project{}, err
	}
	item := domain.Project{ID: id.String(), OrganizationID: organizationID.String(), Name: name}
	if err = tx.QueryRow(ctx, `INSERT INTO projects (id,organization_id,name) VALUES ($1,$2,$3) RETURNING created_at`, id, organizationID, name).Scan(&item.CreatedAt); err != nil {
		return domain.Project{}, mapError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_auth_settings (project_id) VALUES ($1)`, id); err != nil {
		return domain.Project{}, err
	}
	if err = writeAuditMetadata(ctx, tx, organizationID, accountID, "project.create", "project", id, map[string]any{"project_id": id.String()}); err != nil {
		return domain.Project{}, err
	}
	if err = r.enqueueWebhookEventTx(ctx, tx, id, "project.create", "project", id, map[string]any{"name": name}); err != nil {
		return domain.Project{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Project{}, err
	}
	return item, nil
}
func (r *Repository) ProjectByID(ctx context.Context, id, accountID uuid.UUID) (domain.Project, error) {
	var item domain.Project
	err := r.pool.QueryRow(ctx, `SELECT p.id,p.organization_id,p.name,p.created_at FROM projects p JOIN organization_memberships m ON m.organization_id=p.organization_id WHERE p.id=$1 AND m.account_id=$2`, id, accountID).Scan(&item.ID, &item.OrganizationID, &item.Name, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, ErrNotFound
	}
	return item, err
}

// UpdateProject changes the mutable project metadata while holding the
// project row lock. A repeated name is deliberately idempotent and does not
// emit an audit event or webhook notification.
func (r *Repository) UpdateProject(ctx context.Context, projectID, accountID uuid.UUID, name string) (domain.Project, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Project{}, err
	}
	defer tx.Rollback(ctx)

	var item domain.Project
	err = tx.QueryRow(ctx, `
		SELECT id,organization_id,name,created_at
		FROM projects
		WHERE id=$1
		FOR UPDATE`, projectID).
		Scan(&item.ID, &item.OrganizationID, &item.Name, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, ErrNotFound
	}
	if err != nil {
		return domain.Project{}, err
	}
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return domain.Project{}, err
	}
	previousName := item.Name
	if previousName == name {
		if err := tx.Commit(ctx); err != nil {
			return domain.Project{}, err
		}
		return item, nil
	}

	orgID, err := uuid.Parse(item.OrganizationID)
	if err != nil {
		return domain.Project{}, err
	}
	err = tx.QueryRow(ctx, `
		UPDATE projects
		SET name=$2,updated_at=now()
		WHERE id=$1
		RETURNING id,organization_id,name,created_at`, projectID, name).
		Scan(&item.ID, &item.OrganizationID, &item.Name, &item.CreatedAt)
	if err != nil {
		return domain.Project{}, mapError(err)
	}
	metadata := map[string]any{
		"project_id": projectID.String(),
		"fields":     []string{"name"},
		"from":       previousName,
		"to":         name,
	}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "project.update", "project", projectID, metadata); err != nil {
		return domain.Project{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project.update", "project", projectID, metadata); err != nil {
		return domain.Project{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Project{}, err
	}
	return item, nil
}

// DeleteProject permanently removes a project and all tenant-owned database
// rows that reference it. The schema uses ON DELETE CASCADE for every project
// resource, so this operation remains atomic from the API's perspective. A
// caller must be the organization owner and repeat the current project name as
// an explicit confirmation to protect against accidental destructive calls.
func (r *Repository) DeleteProject(ctx context.Context, projectID, accountID uuid.UUID, confirmationName string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var orgID uuid.UUID
	var name string
	if err := tx.QueryRow(ctx, `SELECT organization_id,name FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&orgID, &name); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner"); err != nil {
		return err
	}
	if confirmationName != name {
		return ErrConfirmationRequired
	}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "project.delete", "project", projectID, map[string]any{
		"project_id": projectID.String(),
		"name":       name,
	}); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// ListProjectUsers returns only the safe application-user projection. The
// project membership check deliberately happens before the list query so a
// caller from another tenant receives the same hidden-resource 404 as
// ProjectByID.
