package repository

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SignupInput struct {
	AccountID, OrganizationID, SessionID                    uuid.UUID
	Email, PasswordHash, OrganizationName, OrganizationSlug string
	TokenHash                                               []byte
	SessionExpiresAt                                        time.Time
}

func (r *Repository) Signup(ctx context.Context, input SignupInput) (domain.Account, domain.Organization, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireBootstrapSealedTx(ctx, tx); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	account := domain.Account{ID: input.AccountID.String(), Email: input.Email, EmailVerified: false}
	organization := domain.Organization{ID: input.OrganizationID.String(), Name: input.OrganizationName, Slug: input.OrganizationSlug}
	if err := tx.QueryRow(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,$3) RETURNING created_at`, input.AccountID, input.Email, input.PasswordHash).Scan(&account.CreatedAt); err != nil {
		return domain.Account{}, domain.Organization{}, mapError(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3) RETURNING created_at`, input.OrganizationID, input.OrganizationName, input.OrganizationSlug).Scan(&organization.CreatedAt); err != nil {
		return domain.Account{}, domain.Organization{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_plans (organization_id) VALUES ($1)`, input.OrganizationID); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,'owner')`, input.OrganizationID, input.AccountID); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (id,account_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, input.SessionID, input.AccountID, input.TokenHash, input.SessionExpiresAt); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if err := writeAudit(ctx, tx, uuid.Nil, input.AccountID, "account.signup", "account", input.AccountID); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if err := writeAudit(ctx, tx, input.OrganizationID, input.AccountID, "organization.create", "organization", input.OrganizationID); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, domain.Organization{}, err
	}
	return account, organization, nil
}

func (r *Repository) AccountBySession(ctx context.Context, tokenHash []byte) (domain.Account, uuid.UUID, error) {
	var account domain.Account
	var sessionID uuid.UUID
	var instanceRole sql.NullString
	err := r.pool.QueryRow(ctx, `SELECT a.id,a.email,a.email_verified,ir.role,a.created_at,s.id FROM sessions s JOIN accounts a ON a.id=s.account_id LEFT JOIN instance_roles ir ON ir.account_id=a.id WHERE s.token_hash=$1 AND s.expires_at > now()`, tokenHash).Scan(&account.ID, &account.Email, &account.EmailVerified, &instanceRole, &account.CreatedAt, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, uuid.Nil, ErrNotFound
	}
	if instanceRole.Valid {
		account.InstanceRole = instanceRole.String
	}
	return account, sessionID, err
}
func (r *Repository) AccountPassword(ctx context.Context, email string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var hash string
	err := r.pool.QueryRow(ctx, `SELECT id,password_hash FROM accounts WHERE email=$1`, email).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrNotFound
	}
	return id, hash, err
}

func requireBootstrapSealedTx(ctx context.Context, tx pgx.Tx) error {
	var sealed bool
	err := tx.QueryRow(ctx, `
		SELECT (b.sealed_at IS NOT NULL OR EXISTS (
			SELECT 1 FROM instance_roles WHERE role = 'instance_owner'
		))
		FROM instance_bootstrap b
		WHERE b.id = TRUE
		FOR UPDATE`).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !sealed {
		return ErrBootstrapRequired
	}
	return nil
}

type BootstrapStatus struct {
	SetupRequired bool
}

type BootstrapSessionInput struct {
	ID        uuid.UUID
	CodeHash  []byte
	ExpiresAt time.Time
}

// BootstrapSession stores only the durable portion of a one-time setup
// session. The plaintext code is generated and returned by the HTTP layer but
// is never passed to this repository.
func (r *Repository) CreateBootstrapSession(ctx context.Context, input BootstrapSessionInput) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var sealed bool
	err = tx.QueryRow(ctx, `
		SELECT (b.sealed_at IS NOT NULL OR EXISTS (
			SELECT 1 FROM instance_roles WHERE role = 'instance_owner'
		))
		FROM instance_bootstrap b
		WHERE b.id = TRUE
		FOR UPDATE`).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if sealed {
		return ErrBootstrapSealed
	}
	if len(input.CodeHash) != 32 || input.ExpiresAt.Before(time.Now().UTC()) {
		return ErrInvalidBootstrapCode
	}
	if _, err := tx.Exec(ctx, `
		UPDATE bootstrap_sessions
		SET invalidated_at = now()
		WHERE used_at IS NULL AND invalidated_at IS NULL`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bootstrap_sessions (id,code_hash,expires_at) VALUES ($1,$2,$3)`, input.ID, input.CodeHash, input.ExpiresAt); err != nil {
		return mapError(err)
	}
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, uuid.Nil, "instance.bootstrap.session_created", "bootstrap_session", input.ID, map[string]any{"expires_at": input.ExpiresAt}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) BootstrapStatus(ctx context.Context) (BootstrapStatus, error) {
	var setupRequired bool
	err := r.pool.QueryRow(ctx, `
		SELECT b.sealed_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM instance_roles WHERE role = 'instance_owner')
		FROM instance_bootstrap b
		WHERE b.id = TRUE`).Scan(&setupRequired)
	if errors.Is(err, pgx.ErrNoRows) {
		return BootstrapStatus{}, ErrNotFound
	}
	return BootstrapStatus{SetupRequired: setupRequired}, err
}

type InstanceOwnerInput struct {
	AccountID         uuid.UUID
	SessionID         uuid.UUID
	Email             string
	PasswordHash      string
	TokenHash         []byte
	SessionExpiresAt  time.Time
	BootstrapCodeHash []byte
}

// CreateInstanceOwner consumes the current setup code and creates the first
// instance-level owner and authenticated Console session in one transaction.
// The singleton bootstrap row is locked before the code is checked, so two
// concurrent requests cannot both pass the availability check.
func (r *Repository) CreateInstanceOwner(ctx context.Context, input InstanceOwnerInput) (domain.Account, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback(ctx)

	var sealed bool
	err = tx.QueryRow(ctx, `
		SELECT (b.sealed_at IS NOT NULL OR EXISTS (
			SELECT 1 FROM instance_roles WHERE role = 'instance_owner'
		))
		FROM instance_bootstrap b
		WHERE b.id = TRUE
		FOR UPDATE`).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, ErrNotFound
	}
	if err != nil {
		return domain.Account{}, err
	}
	if sealed {
		return domain.Account{}, ErrBootstrapSealed
	}

	var sessionID uuid.UUID
	var storedHash []byte
	err = tx.QueryRow(ctx, `
		SELECT id,code_hash
		FROM bootstrap_sessions
		WHERE used_at IS NULL AND invalidated_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE`).Scan(&sessionID, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, ErrInvalidBootstrapCode
	}
	if err != nil {
		return domain.Account{}, err
	}
	if len(input.BootstrapCodeHash) != len(storedHash) || subtle.ConstantTimeCompare(input.BootstrapCodeHash, storedHash) != 1 {
		return domain.Account{}, ErrInvalidBootstrapCode
	}

	account := domain.Account{ID: input.AccountID.String(), Email: input.Email, EmailVerified: false, InstanceRole: "instance_owner"}
	if err := tx.QueryRow(ctx, `INSERT INTO accounts (id,email,password_hash) VALUES ($1,$2,$3) RETURNING created_at`, input.AccountID, input.Email, input.PasswordHash).Scan(&account.CreatedAt); err != nil {
		return domain.Account{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO instance_roles (account_id,role) VALUES ($1,'instance_owner')`, input.AccountID); err != nil {
		return domain.Account{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (id,account_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, input.SessionID, input.AccountID, input.TokenHash, input.SessionExpiresAt); err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bootstrap_sessions SET used_at = now() WHERE id = $1`, sessionID); err != nil {
		return domain.Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE instance_bootstrap SET sealed_at = now(),sealed_reason = 'instance_owner_created' WHERE id = TRUE`); err != nil {
		return domain.Account{}, err
	}
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, input.AccountID, "instance.owner.created", "instance_owner", input.AccountID, map[string]any{"role": "instance_owner"}); err != nil {
		return domain.Account{}, err
	}
	if err := writeAuditMetadata(ctx, tx, uuid.Nil, input.AccountID, "instance.bootstrap.sealed", "instance_bootstrap", input.AccountID, map[string]any{"reason": "instance_owner_created"}); err != nil {
		return domain.Account{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

func (r *Repository) UpdateAccountPassword(ctx context.Context, accountID, currentSessionID uuid.UUID, passwordHash string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var lockedAccountID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&lockedAccountID); errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET password_hash=$2,updated_at=now() WHERE id=$1`, accountID, passwordHash); err != nil {
		return 0, err
	}
	result, err := tx.Exec(ctx, `DELETE FROM sessions WHERE account_id=$1 AND id<>$2`, accountID, currentSessionID)
	if err != nil {
		return 0, err
	}
	if err := writeAudit(ctx, tx, uuid.Nil, accountID, "account.password_update", "account", accountID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
