package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OrganizationMembershipRole reports whether a role may be assigned by the
// Console membership management API. Owners are created with an organization
// and are intentionally not transferable through this endpoint yet.
func OrganizationMembershipRole(role string) bool {
	switch role {
	case "admin", "developer", "viewer", "billing":
		return true
	default:
		return false
	}
}

func (r *Repository) organizationRoleTx(ctx context.Context, tx pgx.Tx, organizationID, accountID uuid.UUID) (string, error) {
	var role string
	err := tx.QueryRow(ctx, `
		SELECT role
		FROM organization_memberships
		WHERE organization_id=$1 AND account_id=$2
		FOR SHARE`, organizationID, accountID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func canManageOrganizationMembership(actorRole, targetRole, nextRole string) bool {
	if targetRole == "owner" || nextRole == "owner" {
		return false
	}
	if actorRole == "owner" {
		return OrganizationMembershipRole(nextRole)
	}
	// Admins can manage regular members but cannot alter another admin or
	// grant/revoke the admin role. This keeps owner-level delegation explicit.
	return actorRole == "admin" && targetRole != "admin" && OrganizationMembershipRole(nextRole) && nextRole != "admin"
}

func membershipFromRow(row interface{ Scan(...any) error }) (domain.Membership, error) {
	var item domain.Membership
	var email, provider, providerLogin sql.NullString
	err := row.Scan(&item.OrganizationID, &item.AccountID, &email, &provider, &providerLogin, &item.Role, &item.CreatedAt)
	item.Email = nullableStringPointer(email)
	if provider.Valid {
		item.Provider = provider.String
	}
	if providerLogin.Valid {
		item.ProviderLogin = providerLogin.String
	}
	return item, err
}

// AddOrganizationMembership attaches an existing Console account to an
// organization. Account creation and email invitations remain separate from
// this mutation, so no unverified identity is silently created here.
func (r *Repository) AddOrganizationMembership(ctx context.Context, organizationID, actorID uuid.UUID, email, role string) (domain.Membership, error) {
	if !OrganizationMembershipRole(role) {
		return domain.Membership{}, ErrForbidden
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Membership{}, err
	}
	defer tx.Rollback(ctx)
	actorRole, err := r.organizationRoleTx(ctx, tx, organizationID, actorID)
	if errors.Is(err, ErrNotFound) {
		return domain.Membership{}, ErrForbidden
	}
	if err != nil {
		return domain.Membership{}, err
	}
	if !canManageOrganizationMembership(actorRole, "viewer", role) {
		return domain.Membership{}, ErrForbidden
	}
	if err := r.enforceOrganizationLimitTx(ctx, tx, organizationID, "members"); err != nil {
		return domain.Membership{}, err
	}
	var item domain.Membership
	var accountEmail sql.NullString
	var provider, providerLogin sql.NullString
	err = tx.QueryRow(ctx, `
		SELECT a.id,a.email,ai.provider,ai.provider_login
		FROM accounts a
		LEFT JOIN account_identities ai ON ai.account_id=a.id AND ai.provider='github'
		WHERE a.email=$1`, email).Scan(&item.AccountID, &accountEmail, &provider, &providerLogin)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, ErrNotFound
	}
	if err != nil {
		return domain.Membership{}, err
	}
	item.Email = nullableStringPointer(accountEmail)
	if provider.Valid {
		item.Provider = provider.String
	}
	if providerLogin.Valid {
		item.ProviderLogin = providerLogin.String
	}
	accountID, err := uuid.Parse(item.AccountID)
	if err != nil {
		return domain.Membership{}, err
	}
	item.OrganizationID = organizationID.String()
	item.Role = role
	err = tx.QueryRow(ctx, `
		INSERT INTO organization_memberships (organization_id,account_id,role)
		VALUES ($1,$2,$3)
		RETURNING created_at`, organizationID, accountID, role).Scan(&item.CreatedAt)
	if err != nil {
		return domain.Membership{}, mapError(err)
	}
	metadata := map[string]any{
		"organization_id": organizationID.String(),
		"account_id":      accountID.String(),
		"email":           item.Email,
		"provider":        item.Provider,
		"provider_login":  item.ProviderLogin,
		"role":            role,
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, actorID, "organization.membership.add", "organization_membership", accountID, metadata); err != nil {
		return domain.Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Membership{}, err
	}
	return item, nil
}

// UpdateOrganizationMembershipRole changes a regular member's role. Repeating
// the current role commits without creating a noisy audit event.
func (r *Repository) UpdateOrganizationMembershipRole(ctx context.Context, organizationID, targetID, actorID uuid.UUID, nextRole string) (domain.Membership, error) {
	if !OrganizationMembershipRole(nextRole) {
		return domain.Membership{}, ErrForbidden
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Membership{}, err
	}
	defer tx.Rollback(ctx)
	actorRole, err := r.organizationRoleTx(ctx, tx, organizationID, actorID)
	if errors.Is(err, ErrNotFound) {
		return domain.Membership{}, ErrForbidden
	}
	if err != nil {
		return domain.Membership{}, err
	}
	item, err := membershipFromRow(tx.QueryRow(ctx, `
		SELECT m.organization_id,m.account_id,a.email,ai.provider,ai.provider_login,m.role,m.created_at
		FROM organization_memberships m
		JOIN accounts a ON a.id=m.account_id
		LEFT JOIN account_identities ai ON ai.account_id=a.id AND ai.provider='github'
		WHERE m.organization_id=$1 AND m.account_id=$2
		FOR UPDATE OF m,a`, organizationID, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, ErrNotFound
	}
	if err != nil {
		return domain.Membership{}, err
	}
	if !canManageOrganizationMembership(actorRole, item.Role, nextRole) {
		return domain.Membership{}, ErrForbidden
	}
	previousRole := item.Role
	if previousRole == nextRole {
		if err := tx.Commit(ctx); err != nil {
			return domain.Membership{}, err
		}
		return item, nil
	}
	if err := tx.QueryRow(ctx, `
		UPDATE organization_memberships
		SET role=$3
		WHERE organization_id=$1 AND account_id=$2
		RETURNING created_at`, organizationID, targetID, nextRole).Scan(&item.CreatedAt); err != nil {
		return domain.Membership{}, err
	}
	item.Role = nextRole
	metadata := map[string]any{
		"organization_id": organizationID.String(),
		"account_id":      targetID.String(),
		"email":           item.Email,
		"provider":        item.Provider,
		"provider_login":  item.ProviderLogin,
		"from":            previousRole,
		"to":              nextRole,
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, actorID, "organization.membership.update", "organization_membership", targetID, metadata); err != nil {
		return domain.Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Membership{}, err
	}
	return item, nil
}

// RemoveOrganizationMembership removes a non-owner member. The owner role is
// immutable here, which guarantees every organization retains an owner until
// an explicit ownership-transfer workflow is introduced.
func (r *Repository) RemoveOrganizationMembership(ctx context.Context, organizationID, targetID, actorID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	actorRole, err := r.organizationRoleTx(ctx, tx, organizationID, actorID)
	if errors.Is(err, ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	item, err := membershipFromRow(tx.QueryRow(ctx, `
		SELECT m.organization_id,m.account_id,a.email,ai.provider,ai.provider_login,m.role,m.created_at
		FROM organization_memberships m
		JOIN accounts a ON a.id=m.account_id
		LEFT JOIN account_identities ai ON ai.account_id=a.id AND ai.provider='github'
		WHERE m.organization_id=$1 AND m.account_id=$2
		FOR UPDATE OF m,a`, organizationID, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !canManageOrganizationMembership(actorRole, item.Role, "viewer") {
		return ErrForbidden
	}
	if _, err := tx.Exec(ctx, `DELETE FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, organizationID, targetID); err != nil {
		return err
	}
	metadata := map[string]any{
		"organization_id": organizationID.String(),
		"account_id":      targetID.String(),
		"email":           item.Email,
		"provider":        item.Provider,
		"provider_login":  item.ProviderLogin,
		"role":            item.Role,
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, actorID, "organization.membership.remove", "organization_membership", targetID, metadata); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
