package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
)

const (
	AuthTokenEmailVerification = "email_verification"
	AuthTokenPasswordReset     = "password_reset"
)

var ErrInvalidAuthToken = errors.New("invalid or expired auth token")

func validateAuthTokenInput(kind string, tokenHash []byte, expiresAt time.Time) error {
	if kind != AuthTokenEmailVerification && kind != AuthTokenPasswordReset {
		return errors.New("unsupported auth token kind")
	}
	if len(tokenHash) != 32 {
		return errors.New("auth token hash must be 32 bytes")
	}
	if !expiresAt.After(time.Now().UTC()) {
		return errors.New("auth token expiry must be in the future")
	}
	return nil
}

func (r *Repository) IssueAccountAuthToken(ctx context.Context, id uuid.UUID, kind string, tokenHash []byte, expiresAt time.Time) (domain.Account, error) {
	if err := validateAuthTokenInput(kind, tokenHash, expiresAt); err != nil {
		return domain.Account{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback(ctx)
	var account domain.Account
	err = tx.QueryRow(ctx, `
		SELECT id,email,email_verified,created_at
		FROM accounts WHERE id=$1 FOR UPDATE`, id).
		Scan(&account.ID, &account.Email, &account.EmailVerified, &account.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, ErrNotFound
	}
	if err != nil {
		return domain.Account{}, err
	}
	if err := pruneAccountAuthTokensTx(ctx, tx, id); err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE account_auth_tokens SET consumed_at=now() WHERE account_id=$1 AND kind=$2 AND consumed_at IS NULL`, id, kind); err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO account_auth_tokens (id,account_id,kind,token_hash,expires_at)
		VALUES ($1,$2,$3,$4,$5)`, uuid.Must(uuid.NewV7()), id, kind, tokenHash, expiresAt.UTC()); err != nil {
		return domain.Account{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func (r *Repository) IssueProjectUserAuthToken(ctx context.Context, projectID, userID uuid.UUID, kind string, tokenHash []byte, expiresAt time.Time) (domain.ApplicationUser, error) {
	if err := validateAuthTokenInput(kind, tokenHash, expiresAt); err != nil {
		return domain.ApplicationUser{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	defer tx.Rollback(ctx)
	var user domain.ApplicationUser
	err = tx.QueryRow(ctx, `
		SELECT id,project_id,email,display_name,status,email_verified,created_at,updated_at
		FROM project_users WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, userID).
		Scan(&user.ID, &user.ProjectID, &user.Email, &user.Name, &user.Status, &user.EmailVerified, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, ErrNotFound
	}
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	if user.Status != "active" {
		return domain.ApplicationUser{}, ErrForbidden
	}
	if err := pruneProjectUserAuthTokensTx(ctx, tx, projectID, userID); err != nil {
		return domain.ApplicationUser{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_user_auth_tokens SET consumed_at=now() WHERE project_user_id=$1 AND kind=$2 AND consumed_at IS NULL`, userID, kind); err != nil {
		return domain.ApplicationUser{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO project_user_auth_tokens (id,project_id,project_user_id,kind,token_hash,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, uuid.Must(uuid.NewV7()), projectID, userID, kind, tokenHash, expiresAt.UTC()); err != nil {
		return domain.ApplicationUser{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ApplicationUser{}, err
	}
	return user, nil
}

// CreateAccountPasswordResetToken intentionally returns no existence signal to
// the HTTP layer. The caller can always return the same 202 response and avoid
// leaking which Console addresses have accounts.
func (r *Repository) CreateAccountPasswordResetToken(ctx context.Context, email string, tokenHash []byte, expiresAt time.Time) (domain.Account, bool, error) {
	if err := validateAuthTokenInput(AuthTokenPasswordReset, tokenHash, expiresAt); err != nil {
		return domain.Account{}, false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Account{}, false, err
	}
	defer tx.Rollback(ctx)
	var account domain.Account
	err = tx.QueryRow(ctx, `SELECT id,email,email_verified,created_at FROM accounts WHERE email=$1 FOR UPDATE`, email).
		Scan(&account.ID, &account.Email, &account.EmailVerified, &account.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, false, nil
	}
	if err != nil {
		return domain.Account{}, false, err
	}
	accountID := uuid.MustParse(account.ID)
	if err := pruneAccountAuthTokensTx(ctx, tx, accountID); err != nil {
		return domain.Account{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE account_auth_tokens SET consumed_at=now() WHERE account_id=$1 AND kind=$2 AND consumed_at IS NULL`, accountID, AuthTokenPasswordReset); err != nil {
		return domain.Account{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO account_auth_tokens (id,account_id,kind,token_hash,expires_at) VALUES ($1,$2,$3,$4,$5)`, uuid.Must(uuid.NewV7()), accountID, AuthTokenPasswordReset, tokenHash, expiresAt.UTC()); err != nil {
		return domain.Account{}, false, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, false, err
	}
	return account, true, nil
}

func (r *Repository) CreateProjectUserPasswordResetToken(ctx context.Context, projectID uuid.UUID, email string, tokenHash []byte, expiresAt time.Time) (domain.ApplicationUser, bool, error) {
	if err := validateAuthTokenInput(AuthTokenPasswordReset, tokenHash, expiresAt); err != nil {
		return domain.ApplicationUser{}, false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, false, err
	}
	defer tx.Rollback(ctx)
	var user domain.ApplicationUser
	err = tx.QueryRow(ctx, `
		SELECT id,project_id,email,display_name,status,email_verified,created_at,updated_at
		FROM project_users WHERE project_id=$1 AND email=$2 FOR UPDATE`, projectID, email).
		Scan(&user.ID, &user.ProjectID, &user.Email, &user.Name, &user.Status, &user.EmailVerified, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, false, nil
	}
	if err != nil {
		return domain.ApplicationUser{}, false, err
	}
	if user.Status != "active" {
		return domain.ApplicationUser{}, false, nil
	}
	userID := uuid.MustParse(user.ID)
	if err := pruneProjectUserAuthTokensTx(ctx, tx, projectID, userID); err != nil {
		return domain.ApplicationUser{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_user_auth_tokens SET consumed_at=now() WHERE project_user_id=$1 AND kind=$2 AND consumed_at IS NULL`, userID, AuthTokenPasswordReset); err != nil {
		return domain.ApplicationUser{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_user_auth_tokens (id,project_id,project_user_id,kind,token_hash,expires_at) VALUES ($1,$2,$3,$4,$5,$6)`, uuid.Must(uuid.NewV7()), projectID, userID, AuthTokenPasswordReset, tokenHash, expiresAt.UTC()); err != nil {
		return domain.ApplicationUser{}, false, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ApplicationUser{}, false, err
	}
	return user, true, nil
}

// Opportunistic pruning keeps high-volume tenants from accumulating consumed
// and long-expired secrets. Active-but-expired rows are removed too; the
// partial unique index then remains available for a fresh link immediately.
func pruneAccountAuthTokensTx(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM account_auth_tokens WHERE account_id=$1 AND ((consumed_at IS NOT NULL AND consumed_at < now() - interval '7 days') OR expires_at < now() - interval '7 days')`, accountID)
	return err
}

func pruneProjectUserAuthTokensTx(ctx context.Context, tx pgx.Tx, projectID, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM project_user_auth_tokens WHERE project_id=$1 AND project_user_id=$2 AND ((consumed_at IS NOT NULL AND consumed_at < now() - interval '7 days') OR expires_at < now() - interval '7 days')`, projectID, userID)
	return err
}

func (r *Repository) VerifyAccountEmail(ctx context.Context, tokenHash []byte) (domain.Account, error) {
	if len(tokenHash) != 32 {
		return domain.Account{}, ErrInvalidAuthToken
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback(ctx)
	var account domain.Account
	var tokenID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT a.id,a.email,a.email_verified,a.created_at,t.id
		FROM account_auth_tokens t JOIN accounts a ON a.id=t.account_id
		WHERE t.kind=$1 AND t.token_hash=$2 AND t.consumed_at IS NULL AND t.expires_at>now()
		FOR UPDATE OF t,a`, AuthTokenEmailVerification, tokenHash).
		Scan(&account.ID, &account.Email, &account.EmailVerified, &account.CreatedAt, &tokenID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, ErrInvalidAuthToken
	}
	if err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE account_auth_tokens SET consumed_at=now() WHERE id=$1`, tokenID); err != nil {
		return domain.Account{}, err
	}
	if !account.EmailVerified {
		err = tx.QueryRow(ctx, `UPDATE accounts SET email_verified=true,updated_at=now() WHERE id=$1 RETURNING id,email,email_verified,created_at`, uuid.MustParse(account.ID)).
			Scan(&account.ID, &account.Email, &account.EmailVerified, &account.CreatedAt)
		if err != nil {
			return domain.Account{}, err
		}
	}
	if err := writeAudit(ctx, tx, uuid.Nil, uuid.MustParse(account.ID), "account.email_verify", "account", uuid.MustParse(account.ID)); err != nil {
		return domain.Account{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func (r *Repository) VerifyProjectUserEmail(ctx context.Context, projectID uuid.UUID, tokenHash []byte) (domain.ApplicationUser, error) {
	if len(tokenHash) != 32 {
		return domain.ApplicationUser{}, ErrInvalidAuthToken
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	defer tx.Rollback(ctx)
	var user domain.ApplicationUser
	var tokenID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT u.id,u.project_id,u.email,u.display_name,u.status,u.email_verified,u.created_at,u.updated_at,t.id
		FROM project_user_auth_tokens t JOIN project_users u ON u.id=t.project_user_id AND u.project_id=t.project_id
		WHERE t.project_id=$1 AND t.kind=$2 AND t.token_hash=$3 AND t.consumed_at IS NULL AND t.expires_at>now() AND u.status='active'
		FOR UPDATE OF t,u`, projectID, AuthTokenEmailVerification, tokenHash).
		Scan(&user.ID, &user.ProjectID, &user.Email, &user.Name, &user.Status, &user.EmailVerified, &user.CreatedAt, &user.UpdatedAt, &tokenID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, ErrInvalidAuthToken
	}
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_user_auth_tokens SET consumed_at=now() WHERE id=$1`, tokenID); err != nil {
		return domain.ApplicationUser{}, err
	}
	if !user.EmailVerified {
		err = tx.QueryRow(ctx, `UPDATE project_users SET email_verified=true,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING id,project_id,email,display_name,status,email_verified,created_at,updated_at`, projectID, uuid.MustParse(user.ID)).
			Scan(&user.ID, &user.ProjectID, &user.Email, &user.Name, &user.Status, &user.EmailVerified, &user.CreatedAt, &user.UpdatedAt)
		if err != nil {
			return domain.ApplicationUser{}, err
		}
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	metadata := map[string]any{"project_id": projectID.String(), "email_verified": true}
	if err := writeAuditMetadata(ctx, tx, orgID, uuid.Nil, "project_user.email_verify", "project_user", uuid.MustParse(user.ID), metadata); err != nil {
		return domain.ApplicationUser{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project_user.email_verify", "project_user", uuid.MustParse(user.ID), metadata); err != nil {
		return domain.ApplicationUser{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ApplicationUser{}, err
	}
	return user, nil
}

func (r *Repository) ResetAccountPassword(ctx context.Context, tokenHash []byte, passwordHash string) (domain.Account, error) {
	if len(tokenHash) != 32 {
		return domain.Account{}, ErrInvalidAuthToken
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback(ctx)
	var account domain.Account
	var tokenID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT a.id,a.email,a.email_verified,a.created_at,t.id
		FROM account_auth_tokens t JOIN accounts a ON a.id=t.account_id
		WHERE t.kind=$1 AND t.token_hash=$2 AND t.consumed_at IS NULL AND t.expires_at>now()
		FOR UPDATE OF t,a`, AuthTokenPasswordReset, tokenHash).
		Scan(&account.ID, &account.Email, &account.EmailVerified, &account.CreatedAt, &tokenID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, ErrInvalidAuthToken
	}
	if err != nil {
		return domain.Account{}, err
	}
	accountID := uuid.MustParse(account.ID)
	if _, err := tx.Exec(ctx, `UPDATE account_auth_tokens SET consumed_at=now() WHERE id=$1`, tokenID); err != nil {
		return domain.Account{}, err
	}
	err = tx.QueryRow(ctx, `UPDATE accounts SET password_hash=$2,updated_at=now() WHERE id=$1 RETURNING id,email,email_verified,created_at`, accountID, passwordHash).
		Scan(&account.ID, &account.Email, &account.EmailVerified, &account.CreatedAt)
	if err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE account_id=$1`, accountID); err != nil {
		return domain.Account{}, err
	}
	if err := writeAudit(ctx, tx, uuid.Nil, accountID, "account.password_reset", "account", accountID); err != nil {
		return domain.Account{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func (r *Repository) ResetProjectUserPassword(ctx context.Context, projectID uuid.UUID, tokenHash []byte, passwordHash string) (domain.ApplicationUser, error) {
	if len(tokenHash) != 32 {
		return domain.ApplicationUser{}, ErrInvalidAuthToken
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	defer tx.Rollback(ctx)
	var user domain.ApplicationUser
	var tokenID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT u.id,u.project_id,u.email,u.display_name,u.status,u.email_verified,u.created_at,u.updated_at,t.id
		FROM project_user_auth_tokens t JOIN project_users u ON u.id=t.project_user_id AND u.project_id=t.project_id
		WHERE t.project_id=$1 AND t.kind=$2 AND t.token_hash=$3 AND t.consumed_at IS NULL AND t.expires_at>now() AND u.status='active'
		FOR UPDATE OF t,u`, projectID, AuthTokenPasswordReset, tokenHash).
		Scan(&user.ID, &user.ProjectID, &user.Email, &user.Name, &user.Status, &user.EmailVerified, &user.CreatedAt, &user.UpdatedAt, &tokenID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApplicationUser{}, ErrInvalidAuthToken
	}
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	userID := uuid.MustParse(user.ID)
	if _, err := tx.Exec(ctx, `UPDATE project_user_auth_tokens SET consumed_at=now() WHERE id=$1`, tokenID); err != nil {
		return domain.ApplicationUser{}, err
	}
	err = tx.QueryRow(ctx, `UPDATE project_users SET password_hash=$3,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING id,project_id,email,display_name,status,email_verified,created_at,updated_at`, projectID, userID, passwordHash).
		Scan(&user.ID, &user.ProjectID, &user.Email, &user.Name, &user.Status, &user.EmailVerified, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_user_sessions WHERE project_id=$1 AND project_user_id=$2`, projectID, userID); err != nil {
		return domain.ApplicationUser{}, err
	}
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.ApplicationUser{}, err
	}
	metadata := map[string]any{"project_id": projectID.String(), "password_reset": true}
	if err := writeAuditMetadata(ctx, tx, orgID, uuid.Nil, "project_user.password_reset", "project_user", userID, metadata); err != nil {
		return domain.ApplicationUser{}, err
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "project_user.password_reset", "project_user", userID, metadata); err != nil {
		return domain.ApplicationUser{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ApplicationUser{}, err
	}
	return user, nil
}
