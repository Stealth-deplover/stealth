package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
)

func (r *Repository) ListOrganizations(ctx context.Context, accountID uuid.UUID, limit int, cursor string) ([]domain.Organization, string, error) {
	rows, err := r.pool.Query(ctx, `SELECT o.id,o.name,o.slug,o.created_at FROM organizations o JOIN organization_memberships m ON m.organization_id=o.id WHERE m.account_id=$1 AND ($2='' OR o.id::text > $2) ORDER BY o.id LIMIT $3`, accountID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.Organization, 0, limit)
	for rows.Next() {
		var item domain.Organization
		if err := rows.Scan(&item.ID, &item.Name, &item.Slug, &item.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}
func (r *Repository) CreateOrganization(ctx context.Context, id, accountID uuid.UUID, name, slug string) (domain.Organization, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Organization{}, err
	}
	defer tx.Rollback(ctx)
	item := domain.Organization{ID: id.String(), Name: name, Slug: slug}
	if err = tx.QueryRow(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3) RETURNING created_at`, id, name, slug).Scan(&item.CreatedAt); err != nil {
		return domain.Organization{}, mapError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_plans (organization_id) VALUES ($1)`, id); err != nil {
		return domain.Organization{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner')`, id, accountID); err != nil {
		return domain.Organization{}, err
	}
	if err = writeAudit(ctx, tx, id, accountID, "organization.create", "organization", id); err != nil {
		return domain.Organization{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Organization{}, err
	}
	return item, nil
}

// UpdateOrganization changes organization identity metadata. Owners and
// admins may edit the name and slug, while the immutable organization ID keeps
// SDK configuration and audit history stable.
func (r *Repository) UpdateOrganization(ctx context.Context, organizationID, accountID uuid.UUID, name, slug string) (domain.Organization, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Organization{}, err
	}
	defer tx.Rollback(ctx)
	var item domain.Organization
	if err := tx.QueryRow(ctx, `SELECT id,name,slug,created_at FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&item.ID, &item.Name, &item.Slug, &item.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
		return domain.Organization{}, ErrNotFound
	} else if err != nil {
		return domain.Organization{}, err
	}
	if err := requireRoleTx(ctx, tx, organizationID, accountID, "owner", "admin"); err != nil {
		return domain.Organization{}, err
	}
	previousName, previousSlug := item.Name, item.Slug
	if previousName == name && previousSlug == slug {
		if err := tx.Commit(ctx); err != nil {
			return domain.Organization{}, err
		}
		return item, nil
	}
	if err := tx.QueryRow(ctx, `UPDATE organizations SET name=$2,slug=$3,updated_at=now() WHERE id=$1 RETURNING id,name,slug,created_at`, organizationID, name, slug).Scan(&item.ID, &item.Name, &item.Slug, &item.CreatedAt); err != nil {
		return domain.Organization{}, mapError(err)
	}
	metadata := map[string]any{
		"organization_id": organizationID.String(),
		"fields":          []string{"name", "slug"},
		"from":            map[string]string{"name": previousName, "slug": previousSlug},
		"to":              map[string]string{"name": name, "slug": slug},
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, accountID, "organization.update", "organization", organizationID, metadata); err != nil {
		return domain.Organization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Organization{}, err
	}
	return item, nil
}
func (r *Repository) ListMemberships(ctx context.Context, organizationID, accountID uuid.UUID, limit int, cursor string) ([]domain.Membership, string, bool, error) {
	if err := r.requireMembership(ctx, organizationID, accountID); err != nil {
		return nil, "", false, err
	}
	var role string
	if err := r.pool.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, organizationID, accountID).Scan(&role); err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT m.organization_id,m.account_id,a.email,m.role,m.created_at FROM organization_memberships m JOIN accounts a ON a.id=m.account_id WHERE m.organization_id=$1 AND ($2='' OR m.account_id::text>$2) ORDER BY m.account_id LIMIT $3`, organizationID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.Membership, 0, limit)
	for rows.Next() {
		var item domain.Membership
		if err := rows.Scan(&item.OrganizationID, &item.AccountID, &item.Email, &item.Role, &item.CreatedAt); err != nil {
			return nil, "", false, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].AccountID
		items = items[:limit]
	}
	return items, next, role == "owner" || role == "admin", nil
}
