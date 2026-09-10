package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidOrganizationInvitation is intentionally shared by missing,
// expired, consumed, and revoked tokens so the API does not reveal whether a
// token ever existed.
var ErrInvalidOrganizationInvitation = errors.New("invalid organization invitation")

func invitationStatus(invitation domain.OrganizationInvitation, now time.Time) string {
	if invitation.RevokedAt != nil {
		return "revoked"
	}
	if invitation.AcceptedAt != nil {
		return "accepted"
	}
	if !invitation.ExpiresAt.After(now) {
		return "expired"
	}
	return "pending"
}

func (r *Repository) organizationManagerRoleTx(ctx context.Context, tx pgx.Tx, organizationID, accountID uuid.UUID) (string, error) {
	role, err := r.organizationRoleTx(ctx, tx, organizationID, accountID)
	if errors.Is(err, ErrNotFound) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", err
	}
	if role != "owner" && role != "admin" {
		return "", ErrForbidden
	}
	return role, nil
}

func invitationFromRow(row interface{ Scan(...any) error }, now time.Time) (domain.OrganizationInvitation, error) {
	var item domain.OrganizationInvitation
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.Email,
		&item.Role,
		&item.InvitedByAccountID,
		&item.InvitedByEmail,
		&item.Status,
		&item.ExpiresAt,
		&item.AcceptedAt,
		&item.RevokedAt,
		&item.CreatedAt,
	); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	item.Status = invitationStatus(item, now)
	return item, nil
}

const invitationProjection = `
	i.id,
	i.organization_id,
	i.email,
	i.role,
	i.invited_by_account_id,
	a.email,
	CASE WHEN i.revoked_at IS NOT NULL THEN 'revoked'
	     WHEN i.accepted_at IS NOT NULL THEN 'accepted'
	     WHEN i.expires_at <= now() THEN 'expired'
	     ELSE 'pending' END,
	i.expires_at,
	i.accepted_at,
	i.revoked_at,
	i.created_at`

// CreateOrganizationInvitation persists a new email-bound invitation. A new
// send replaces any prior live invitation for the same address, making the UI
// retry-safe while retaining an audit trail for the replacement.
func (r *Repository) CreateOrganizationInvitation(ctx context.Context, organizationID, actorID uuid.UUID, email, role string, tokenHash []byte, expiresAt time.Time) (domain.OrganizationInvitation, error) {
	if !OrganizationMembershipRole(role) || len(tokenHash) != 32 || strings.TrimSpace(email) == "" || !expiresAt.After(time.Now().UTC()) {
		return domain.OrganizationInvitation{}, ErrForbidden
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationInvitation{}, err
	}
	defer tx.Rollback(ctx)
	actorRole, err := r.organizationManagerRoleTx(ctx, tx, organizationID, actorID)
	if err != nil {
		return domain.OrganizationInvitation{}, err
	}
	if !canManageOrganizationMembership(actorRole, "viewer", role) {
		return domain.OrganizationInvitation{}, ErrForbidden
	}
	// Existing membership should be managed directly rather than through an
	// invitation; this also prevents an invitation from downgrading access.
	var member bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships m JOIN accounts a ON a.id=m.account_id WHERE m.organization_id=$1 AND a.email=$2)`, organizationID, email).Scan(&member); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	if member {
		return domain.OrganizationInvitation{}, ErrConflict
	}
	// The partial unique index covers this invariant. Revoking first lets an
	// administrator resend after an expiry or an earlier delivery failure.
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM organization_invitations
		WHERE organization_id=$1 AND email=$2 AND accepted_at IS NULL AND revoked_at IS NULL
		FOR UPDATE`, organizationID, email)
	if err != nil {
		return domain.OrganizationInvitation{}, err
	}
	var replaced []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return domain.OrganizationInvitation{}, err
		}
		replaced = append(replaced, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.OrganizationInvitation{}, err
	}
	rows.Close()
	for _, oldID := range replaced {
		if _, err := tx.Exec(ctx, `UPDATE organization_invitations SET revoked_at=now() WHERE id=$1`, oldID); err != nil {
			return domain.OrganizationInvitation{}, err
		}
		if err := writeAuditMetadata(ctx, tx, organizationID, actorID, "organization.invitation.replaced", "organization_invitation", oldID, map[string]any{"email": email, "reason": "resent"}); err != nil {
			return domain.OrganizationInvitation{}, err
		}
	}
	id := uuid.Must(uuid.NewV7())
	var item domain.OrganizationInvitation
	if err := tx.QueryRow(ctx, `INSERT INTO organization_invitations (id,organization_id,email,role,token_hash,expires_at,invited_by_account_id) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, id, organizationID, email, role, tokenHash, expiresAt, actorID).Scan(&item.CreatedAt); err != nil {
		return domain.OrganizationInvitation{}, mapError(err)
	}
	item.ID = id.String()
	item.OrganizationID = organizationID.String()
	item.Email = email
	item.Role = role
	inviter := actorID.String()
	item.InvitedByAccountID = &inviter
	item.ExpiresAt = expiresAt
	item.Status = invitationStatus(item, time.Now().UTC())
	metadata := map[string]any{"organization_id": organizationID.String(), "email": email, "role": role}
	if err := writeAuditMetadata(ctx, tx, organizationID, actorID, "organization.invitation.create", "organization_invitation", id, metadata); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	return item, nil
}

// ListOrganizationInvitations returns pending (including expired but not yet
// revoked) invitations visible to organization owners and admins.
func (r *Repository) ListOrganizationInvitations(ctx context.Context, organizationID, accountID uuid.UUID, limit int, cursor string) ([]domain.OrganizationInvitation, string, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, "", false, err
	}
	defer tx.Rollback(ctx)
	role, err := r.organizationManagerRoleTx(ctx, tx, organizationID, accountID)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := tx.Query(ctx, `SELECT `+invitationProjection+`
		FROM organization_invitations i
		LEFT JOIN accounts a ON a.id=i.invited_by_account_id
		WHERE i.organization_id=$1 AND ($2='' OR i.id::text < $2)
		  AND i.accepted_at IS NULL AND i.revoked_at IS NULL
		ORDER BY i.id DESC LIMIT $3`, organizationID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.OrganizationInvitation, 0, limit)
	now := time.Now().UTC()
	for rows.Next() {
		item, scanErr := invitationFromRow(rows, now)
		if scanErr != nil {
			return nil, "", false, scanErr
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
	if err := tx.Commit(ctx); err != nil {
		return nil, "", false, err
	}
	return items, next, role == "owner" || role == "admin", nil
}

// RevokeOrganizationInvitation makes a token unusable. It is idempotent for
// an already revoked invitation only from the perspective of the caller that
// can no longer see it; accepted invitations remain immutable.
func (r *Repository) RevokeOrganizationInvitation(ctx context.Context, organizationID, invitationID, actorID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := r.organizationManagerRoleTx(ctx, tx, organizationID, actorID); err != nil {
		return err
	}
	var email, role string
	var acceptedAt, revokedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT email,role,accepted_at,revoked_at FROM organization_invitations WHERE id=$1 AND organization_id=$2 FOR UPDATE`, invitationID, organizationID).Scan(&email, &role, &acceptedAt, &revokedAt); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if acceptedAt != nil {
		return ErrConflict
	}
	if revokedAt != nil {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE organization_invitations SET revoked_at=now() WHERE id=$1`, invitationID); err != nil {
		return err
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, actorID, "organization.invitation.revoke", "organization_invitation", invitationID, map[string]any{"email": email, "role": role}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AcceptOrganizationInvitation consumes a token and creates the matching
// membership in one transaction. The recipient must be signed in as the
// normalized email address that received the invitation.
func (r *Repository) AcceptOrganizationInvitation(ctx context.Context, tokenHash []byte, accountID uuid.UUID) (domain.Membership, error) {
	if len(tokenHash) != 32 {
		return domain.Membership{}, ErrInvalidOrganizationInvitation
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Membership{}, err
	}
	defer tx.Rollback(ctx)
	var accountEmail string
	if err := tx.QueryRow(ctx, `SELECT email FROM accounts WHERE id=$1`, accountID).Scan(&accountEmail); errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, ErrInvalidOrganizationInvitation
	} else if err != nil {
		return domain.Membership{}, err
	}
	var invitationID, organizationID uuid.UUID
	var email, role string
	var acceptedAt, revokedAt *time.Time
	var expiresAt time.Time
	if err := tx.QueryRow(ctx, `SELECT id,organization_id,email,role,expires_at,accepted_at,revoked_at FROM organization_invitations WHERE token_hash=$1 FOR UPDATE`, tokenHash).Scan(&invitationID, &organizationID, &email, &role, &expiresAt, &acceptedAt, &revokedAt); errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, ErrInvalidOrganizationInvitation
	} else if err != nil {
		return domain.Membership{}, err
	}
	if acceptedAt != nil || revokedAt != nil || !expiresAt.After(time.Now().UTC()) || !strings.EqualFold(email, accountEmail) {
		if !strings.EqualFold(email, accountEmail) && acceptedAt == nil && revokedAt == nil && expiresAt.After(time.Now().UTC()) {
			return domain.Membership{}, ErrForbidden
		}
		return domain.Membership{}, ErrInvalidOrganizationInvitation
	}
	var item domain.Membership
	if err := tx.QueryRow(ctx, `SELECT m.organization_id,m.account_id,a.email,m.role,m.created_at FROM organization_memberships m JOIN accounts a ON a.id=m.account_id WHERE m.organization_id=$1 AND m.account_id=$2 FOR UPDATE`, organizationID, accountID).Scan(&item.OrganizationID, &item.AccountID, &item.Email, &item.Role, &item.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
		if err := r.enforceOrganizationLimitTx(ctx, tx, organizationID, "members"); err != nil {
			return domain.Membership{}, err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO organization_memberships (organization_id,account_id,role) VALUES ($1,$2,$3) RETURNING created_at`, organizationID, accountID, role).Scan(&item.CreatedAt); err != nil {
			return domain.Membership{}, mapError(err)
		}
		item.OrganizationID = organizationID.String()
		item.AccountID = accountID.String()
		item.Email = accountEmail
		item.Role = role
	} else if err != nil {
		return domain.Membership{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE organization_invitations SET accepted_at=now() WHERE id=$1`, invitationID); err != nil {
		return domain.Membership{}, err
	}
	if err := writeAuditMetadata(ctx, tx, organizationID, accountID, "organization.invitation.accept", "organization_invitation", invitationID, map[string]any{"account_id": accountID.String(), "email": accountEmail, "role": item.Role}); err != nil {
		return domain.Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Membership{}, err
	}
	return item, nil
}
