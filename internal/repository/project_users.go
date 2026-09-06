package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
)

func (r *Repository) ListProjectUsers(ctx context.Context, projectID, accountID uuid.UUID, limit int, cursor *uuid.UUID) ([]domain.ApplicationUser, string, bool, error) {
	role, err := r.projectRole(ctx, projectID, accountID)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id,project_id,email,display_name,status,email_verified,created_at,updated_at
		FROM project_users
		WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3)
		ORDER BY id
		LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.ApplicationUser, 0, limit)
	for rows.Next() {
		var item domain.ApplicationUser
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt); err != nil {
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
	return items, next, role == "owner" || role == "admin", nil
}

func (r *Repository) ProjectUserByID(ctx context.Context, projectID, userID, accountID uuid.UUID) (domain.ApplicationUser, error) {
	if err := r.requireProjectAccess(ctx, projectID, accountID); err != nil {
		return domain.ApplicationUser{}, err
	}
	var item domain.ApplicationUser
	err := r.pool.QueryRow(ctx, `
		SELECT id,project_id,email,display_name,status,email_verified,created_at,updated_at
		FROM project_users
		WHERE project_id=$1 AND id=$2`, projectID, userID).Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateProjectUser(ctx context.Context, id, projectID, accountID uuid.UUID, email, passwordHash string, name *string) (domain.ApplicationUser, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return domain.ApplicationUser{}, err
	}
	var item domain.ApplicationUser
	err = tx.QueryRow(ctx, `
		INSERT INTO project_users (id,project_id,email,display_name,password_hash)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id,project_id,email,display_name,status,email_verified,created_at,updated_at`, id, projectID, email, name, passwordHash).
		Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.ApplicationUser{}, mapError(err)
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	metadata := map[string]any{"project_id": projectID.String()}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "project_user.create", "project_user", id, metadata); err != nil {
		return domain.ApplicationUser{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project_user.create", "project_user", id, metadata); err != nil {
		return domain.ApplicationUser{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ApplicationUser{}, err
	}
	return item, nil
}

// AuthorizeProjectUserWrite is a cheap preflight used before expensive
// password hashing. CreateProjectUser repeats this check inside its write
// transaction so a membership change between the two calls cannot bypass
// authorization.
func (r *Repository) AuthorizeProjectUserWrite(ctx context.Context, projectID, accountID uuid.UUID) error {
	var role string
	err := r.pool.QueryRow(ctx, `
		SELECT m.role
		FROM projects p
		JOIN organization_memberships m ON m.organization_id=p.organization_id
		WHERE p.id=$1 AND m.account_id=$2`, projectID, accountID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" {
		return ErrForbidden
	}
	return nil
}

// ApplicationUserPassword is an internal credential lookup. Callers must not
// serialize the returned hash; it exists only to perform Argon2id verification.
func (r *Repository) ApplicationUserPassword(ctx context.Context, projectID uuid.UUID, email string) (uuid.UUID, string, string, error) {
	var userID uuid.UUID
	var passwordHash, status string
	err := r.pool.QueryRow(ctx, `
		SELECT id,password_hash,status
		FROM project_users
		WHERE project_id=$1 AND email=$2`, projectID, email).Scan(&userID, &passwordHash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", "", ErrNotFound
	}
	return userID, passwordHash, status, err
}

// RegisterProjectUser creates a project user and its first application session
// in one transaction. Registration is checked again while the transaction is
// open so a settings change cannot race this write.
func (r *Repository) RegisterProjectUser(ctx context.Context, userID, sessionID, projectID uuid.UUID, email, passwordHash string, name *string, tokenHash []byte, expiresAt time.Time) (domain.ApplicationUser, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	defer tx.Rollback(ctx)
	var registrationEnabled bool
	err = tx.QueryRow(ctx, `SELECT registration_enabled FROM project_auth_settings WHERE project_id=$1 FOR SHARE`, projectID).Scan(&registrationEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, ErrNotFound
	}
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	if !registrationEnabled {
		return domain.ApplicationUser{}, ErrRegistrationDisabled
	}
	var item domain.ApplicationUser
	err = tx.QueryRow(ctx, `
		INSERT INTO project_users (id,project_id,email,display_name,password_hash)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id,project_id,email,display_name,status,email_verified,created_at,updated_at`, userID, projectID, email, name, passwordHash).
		Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.ApplicationUser{}, mapError(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO project_user_sessions (id,project_id,project_user_id,token_hash,expires_at)
		VALUES ($1,$2,$3,$4,$5)`, sessionID, projectID, userID, tokenHash, expiresAt); err != nil {
		return domain.ApplicationUser{}, mapError(err)
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	metadata := map[string]any{"project_id": projectID.String(), "source": "self_registration"}
	if err := writeAuditMetadata(ctx, tx, orgID, uuid.Nil, "project_user.create", "project_user", userID, metadata); err != nil {
		return domain.ApplicationUser{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project_user.create", "project_user", userID, metadata); err != nil {
		return domain.ApplicationUser{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ApplicationUser{}, err
	}
	return item, nil
}

// CreateProjectUserSession rechecks that the application user is still active
// under a row lock. This closes the race where a block could otherwise happen
// between password verification and session insertion.
func (r *Repository) CreateProjectUserSession(ctx context.Context, sessionID, projectID, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM project_users WHERE id=$1 AND project_id=$2 FOR UPDATE`, userID, projectID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return ErrForbidden
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO project_user_sessions (id,project_id,project_user_id,token_hash,expires_at)
		VALUES ($1,$2,$3,$4,$5)`, sessionID, projectID, userID, tokenHash, expiresAt); err != nil {
		return mapError(err)
	}
	return tx.Commit(ctx)
}

func (r *Repository) ApplicationUserBySession(ctx context.Context, projectID uuid.UUID, tokenHash []byte) (domain.ApplicationUser, uuid.UUID, error) {
	var item domain.ApplicationUser
	var sessionID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT u.id,u.project_id,u.email,u.display_name,u.status,u.email_verified,u.created_at,u.updated_at,s.id
		FROM project_user_sessions s
		JOIN project_users u ON u.id=s.project_user_id AND u.project_id=s.project_id
		WHERE s.project_id=$1 AND s.token_hash=$2 AND s.expires_at>now() AND u.status='active'`, projectID, tokenHash).
		Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, uuid.Nil, ErrNotFound
	}
	return item, sessionID, err
}

func (r *Repository) DeleteProjectUserSession(ctx context.Context, projectID, sessionID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM project_user_sessions WHERE project_id=$1 AND id=$2`, projectID, sessionID)
	return err
}

func (r *Repository) UpdateProjectUserStatus(ctx context.Context, projectID, userID, accountID uuid.UUID, status string) (domain.ApplicationUser, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return domain.ApplicationUser{}, err
	}
	var item domain.ApplicationUser
	err = tx.QueryRow(ctx, `
		SELECT id,project_id,email,display_name,status,email_verified,created_at,updated_at
		FROM project_users
		WHERE project_id=$1 AND id=$2
		FOR UPDATE`, projectID, userID).
		Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, ErrNotFound
	}
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	previousStatus := item.Status
	if previousStatus != status {
		err = tx.QueryRow(ctx, `
			UPDATE project_users
			SET status=$3,updated_at=now()
			WHERE project_id=$1 AND id=$2
			RETURNING id,project_id,email,display_name,status,email_verified,created_at,updated_at`, projectID, userID, status).
			Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			return domain.ApplicationUser{}, err
		}
		orgID, orgErr := projectOrganizationIDValue(ctx, tx, projectID)
		if orgErr != nil {
			return domain.ApplicationUser{}, orgErr
		}
		metadata := map[string]any{"project_id": projectID.String(), "from": previousStatus, "to": status}
		if err := writeAuditMetadata(ctx, tx, orgID, accountID, "project_user.status_change", "project_user", userID, metadata); err != nil {
			return domain.ApplicationUser{}, err
		}
		if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project_user.status_change", "project_user", userID, metadata); err != nil {
			return domain.ApplicationUser{}, err
		}
	}
	if status == "blocked" {
		if _, err := tx.Exec(ctx, `DELETE FROM project_user_sessions WHERE project_id=$1 AND project_user_id=$2`, projectID, userID); err != nil {
			return domain.ApplicationUser{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ApplicationUser{}, err
	}
	return item, nil
}

// DeleteProjectUser permanently removes an application identity. Its
// project-scoped sessions and recovery tokens are deleted by foreign-key
// cascade, while database rows keep their data and clear the creator pointer.
// Only project owners and admins may perform the operation.
func (r *Repository) DeleteProjectUser(ctx context.Context, projectID, userID, accountID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return err
	}
	if err := r.deleteProjectUserTx(ctx, tx, projectID, userID, accountID, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteProjectUserByAPIKey is the server-to-server equivalent. The API key
// is revalidated inside the write transaction so revocation cannot race this
// destructive mutation.
func (r *Repository) DeleteProjectUserByAPIKey(ctx context.Context, projectID, userID, apiKeyID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := requireActiveProjectAPIKeyTx(ctx, tx, projectID, apiKeyID, "users.write"); err != nil {
		return err
	}
	if err := r.deleteProjectUserTx(ctx, tx, projectID, userID, uuid.Nil, map[string]any{
		"actor":      "api_key",
		"api_key_id": apiKeyID.String(),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) deleteProjectUserTx(ctx context.Context, tx pgx.Tx, projectID, userID, actorID uuid.UUID, metadata map[string]any) error {
	var email string
	if err := tx.QueryRow(ctx, `SELECT email FROM project_users WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, userID).Scan(&email); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["project_id"] = projectID.String()
	webhookMetadata := make(map[string]any, len(metadata))
	for key, value := range metadata {
		webhookMetadata[key] = value
	}
	auditMetadata := make(map[string]any, len(metadata)+1)
	for key, value := range metadata {
		auditMetadata[key] = value
	}
	// Email is useful for audit operators but is deliberately kept out of
	// webhook payloads, where it would widen the recipient data surface.
	auditMetadata["email"] = email
	if _, err := tx.Exec(ctx, `DELETE FROM project_users WHERE project_id=$1 AND id=$2`, projectID, userID); err != nil {
		return err
	}
	if err := writeAuditMetadata(ctx, tx, orgID, actorID, "project_user.delete", "project_user", userID, auditMetadata); err != nil {
		return err
	}
	return r.enqueueWebhookEventTx(ctx, tx, projectID, "project_user.delete", "project_user", userID, webhookMetadata)
}
