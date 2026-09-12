package repository

import (
	"context"
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
	var email, instanceRole, provider, providerUserID, providerLogin, displayName, avatarURL sql.NullString
	err := r.pool.QueryRow(ctx, `
		SELECT a.id,a.email,a.email_verified,ir.role,
		       ai.provider,ai.provider_user_id,ai.provider_login,ai.display_name,ai.avatar_url,
		       a.created_at,s.id
		FROM sessions s
		JOIN accounts a ON a.id=s.account_id
		LEFT JOIN instance_roles ir ON ir.account_id=a.id
		LEFT JOIN account_identities ai ON ai.account_id=a.id AND ai.provider='github'
		WHERE s.token_hash=$1 AND s.expires_at > now()`, tokenHash).Scan(
		&account.ID, &email, &account.EmailVerified, &instanceRole,
		&provider, &providerUserID, &providerLogin, &displayName, &avatarURL,
		&account.CreatedAt, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, uuid.Nil, ErrNotFound
	}
	if email.Valid {
		account.Email = email.String
	}
	if instanceRole.Valid {
		account.InstanceRole = instanceRole.String
	}
	populateGitHubIdentity(&account, provider, providerUserID, providerLogin, displayName, avatarURL)
	return account, sessionID, err
}
func (r *Repository) AccountPassword(ctx context.Context, email string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var hash sql.NullString
	err := r.pool.QueryRow(ctx, `SELECT id,password_hash FROM accounts WHERE email=$1`, email).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrNotFound
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	// GitHub-only first owners intentionally have no local password. Treat
	// password login for those accounts like an unknown credential rather than
	// surfacing a database NULL-scan error or inventing a password hash.
	if !hash.Valid {
		return uuid.Nil, "", ErrNotFound
	}
	return id, hash.String, nil
}

func populateGitHubIdentity(account *domain.Account, provider, providerUserID, providerLogin, displayName, avatarURL sql.NullString) {
	if provider.Valid {
		account.Provider = provider.String
	}
	if providerUserID.Valid {
		account.ProviderUserID = providerUserID.String
	}
	if providerLogin.Valid {
		account.ProviderLogin = providerLogin.String
	}
	if displayName.Valid {
		account.DisplayName = displayName.String
	}
	if avatarURL.Valid {
		account.AvatarURL = avatarURL.String
	}
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
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
