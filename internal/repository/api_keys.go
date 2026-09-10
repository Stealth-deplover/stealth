package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type projectAPIKeyRow interface {
	Scan(dest ...any) error
}

func scanProjectAPIKey(row projectAPIKeyRow) (domain.ProjectAPIKey, error) {
	var item domain.ProjectAPIKey
	err := row.Scan(
		&item.ID,
		&item.ProjectID,
		&item.Name,
		&item.Prefix,
		&item.Scopes,
		&item.ExpiresAt,
		&item.RevokedAt,
		&item.LastUsedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

const projectAPIKeyProjection = `id,project_id,name,prefix,scopes,expires_at,revoked_at,last_used_at,created_at,updated_at`

func (r *Repository) CreateProjectAPIKey(ctx context.Context, id, projectID, accountID uuid.UUID, name, prefix string, secretHash []byte, scopes []string, expiresAt *time.Time) (domain.ProjectAPIKey, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ProjectAPIKey{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return domain.ProjectAPIKey{}, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO project_api_keys (id,project_id,name,prefix,secret_hash,scopes,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING `+projectAPIKeyProjection, id, projectID, name, prefix, secretHash, scopes, expiresAt)
	item, err := scanProjectAPIKey(row)
	if err != nil {
		return domain.ProjectAPIKey{}, mapError(err)
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.ProjectAPIKey{}, err
	}
	metadata := map[string]any{"project_id": projectID.String(), "prefix": prefix, "scopes": scopes}
	if err := writeAuditMetadata(ctx, tx, orgID, accountID, "project_api_key.create", "project_api_key", id, metadata); err != nil {
		return domain.ProjectAPIKey{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project_api_key.create", "project_api_key", id, metadata); err != nil {
		return domain.ProjectAPIKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProjectAPIKey{}, err
	}
	return item, nil
}

func (r *Repository) ListProjectAPIKeys(ctx context.Context, projectID, accountID uuid.UUID, limit int, cursor *uuid.UUID) ([]domain.ProjectAPIKey, string, bool, error) {
	role, err := r.projectRole(ctx, projectID, accountID)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+projectAPIKeyProjection+`
		FROM project_api_keys
		WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3)
		ORDER BY id
		LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.ProjectAPIKey, 0, limit)
	for rows.Next() {
		item, err := scanProjectAPIKey(rows)
		if err != nil {
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

func (r *Repository) ProjectAPIKeyByID(ctx context.Context, projectID, keyID, accountID uuid.UUID) (domain.ProjectAPIKey, error) {
	if err := r.requireProjectAccess(ctx, projectID, accountID); err != nil {
		return domain.ProjectAPIKey{}, err
	}
	item, err := scanProjectAPIKey(r.pool.QueryRow(ctx, `
		SELECT `+projectAPIKeyProjection+`
		FROM project_api_keys
		WHERE project_id=$1 AND id=$2`, projectID, keyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectAPIKey{}, ErrNotFound
	}
	return item, err
}

// RevokeProjectAPIKey is idempotent. Only the transition from active to
// revoked creates an audit event; a repeated revoke cannot create duplicates.
func (r *Repository) RevokeProjectAPIKey(ctx context.Context, projectID, keyID, accountID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := requireProjectRoleTx(ctx, tx, projectID, accountID, "owner", "admin"); err != nil {
		return err
	}
	var prefix string
	var revokedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT prefix,revoked_at FROM project_api_keys WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, keyID).Scan(&prefix, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if revokedAt == nil {
		if err := tx.QueryRow(ctx, `UPDATE project_api_keys SET revoked_at=now(),updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING revoked_at`, projectID, keyID).Scan(&revokedAt); err != nil {
			return err
		}
		orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
		if err != nil {
			return err
		}
		metadata := map[string]any{"project_id": projectID.String(), "prefix": prefix}
		if err := writeAuditMetadata(ctx, tx, orgID, accountID, "project_api_key.revoke", "project_api_key", keyID, metadata); err != nil {
			return err
		}
		if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project_api_key.revoke", "project_api_key", keyID, metadata); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AuthenticateProjectAPIKey intentionally filters project binding, revocation,
// and expiry in one query so all invalid key classes have the same 401 result.
func (r *Repository) AuthenticateProjectAPIKey(ctx context.Context, projectID uuid.UUID, secretHash []byte) (domain.ProjectAPIKeyAuth, error) {
	var item domain.ProjectAPIKeyAuth
	err := r.pool.QueryRow(ctx, `
		SELECT id,project_id,scopes,last_used_at
		FROM project_api_keys
		WHERE project_id=$1 AND secret_hash=$2 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at>now())`, projectID, secretHash).
		Scan(&item.ID, &item.ProjectID, &item.Scopes, &item.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectAPIKeyAuth{}, ErrNotFound
	}
	return item, err
}

// TouchProjectAPIKey updates usage at most once per five minutes. The
// conditional update is serialized by PostgreSQL's row lock, so concurrent
// requests cannot amplify writes beyond the interval.
func (r *Repository) TouchProjectAPIKey(ctx context.Context, keyID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE project_api_keys
		SET last_used_at=now(),updated_at=now()
		WHERE id=$1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at>now())
		  AND (last_used_at IS NULL OR last_used_at <= now() - interval '5 minutes')`, keyID)
	return err
}

func (r *Repository) ListProjectUsersByAPIKey(ctx context.Context, projectID uuid.UUID, limit int, cursor *uuid.UUID) ([]domain.ApplicationUser, string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id,project_id,email,display_name,status,email_verified,created_at,updated_at
		FROM project_users
		WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3)
		ORDER BY id
		LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.ApplicationUser, 0, limit)
	for rows.Next() {
		var item domain.ApplicationUser
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *Repository) ProjectUserByIDForAPIKey(ctx context.Context, projectID, userID uuid.UUID) (domain.ApplicationUser, error) {
	var item domain.ApplicationUser
	err := r.pool.QueryRow(ctx, `
		SELECT id,project_id,email,display_name,status,email_verified,created_at,updated_at
		FROM project_users WHERE project_id=$1 AND id=$2`, projectID, userID).
		Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateProjectUserByAPIKey(ctx context.Context, id, projectID, apiKeyID uuid.UUID, email, passwordHash string, name *string) (domain.ApplicationUser, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireActiveProjectAPIKeyTx(ctx, tx, projectID, apiKeyID, "users.write"); err != nil {
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
	metadata := map[string]any{"project_id": projectID.String(), "actor": "api_key", "api_key_id": apiKeyID.String()}
	if err := writeAuditMetadata(ctx, tx, orgID, uuid.Nil, "project_user.create", "project_user", id, metadata); err != nil {
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

func (r *Repository) UpdateProjectUserStatusByAPIKey(ctx context.Context, projectID, userID, apiKeyID uuid.UUID, status string) (domain.ApplicationUser, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireActiveProjectAPIKeyTx(ctx, tx, projectID, apiKeyID, "users.write"); err != nil {
		return domain.ApplicationUser{}, err
	}
	var item domain.ApplicationUser
	err = tx.QueryRow(ctx, `
		SELECT id,project_id,email,display_name,status,email_verified,created_at,updated_at
		FROM project_users WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, userID).
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
			UPDATE project_users SET status=$3,updated_at=now()
			WHERE project_id=$1 AND id=$2
			RETURNING id,project_id,email,display_name,status,email_verified,created_at,updated_at`, projectID, userID, status).
			Scan(&item.ID, &item.ProjectID, &item.Email, &item.Name, &item.Status, &item.EmailVerified, &item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			return domain.ApplicationUser{}, err
		}
		orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
		if err != nil {
			return domain.ApplicationUser{}, err
		}
		metadata := map[string]any{"project_id": projectID.String(), "from": previousStatus, "to": status, "actor": "api_key", "api_key_id": apiKeyID.String()}
		if err := writeAuditMetadata(ctx, tx, orgID, uuid.Nil, "project_user.status_change", "project_user", userID, metadata); err != nil {
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

// requireActiveProjectAPIKeyTx closes the small race between middleware
// authentication and a mutating request. Revoking a key while a request is
// waiting on the database must prevent that request from writing with the key.
func requireActiveProjectAPIKeyTx(ctx context.Context, tx pgx.Tx, projectID, keyID uuid.UUID, scope string) error {
	var active bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM project_api_keys
			WHERE id=$1 AND project_id=$2
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at>now())
			  AND $3 = ANY(scopes)
		)`, keyID, projectID, scope).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return ErrNotFound
	}
	return nil
}
