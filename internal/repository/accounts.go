package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/domain"
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
	err := r.pool.QueryRow(ctx, `SELECT a.id,a.email,a.email_verified,a.created_at,s.id FROM sessions s JOIN accounts a ON a.id=s.account_id WHERE s.token_hash=$1 AND s.expires_at > now()`, tokenHash).Scan(&account.ID, &account.Email, &account.EmailVerified, &account.CreatedAt, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, uuid.Nil, ErrNotFound
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
