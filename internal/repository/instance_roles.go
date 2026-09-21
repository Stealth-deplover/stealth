package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// InstanceRole reads the instance-level role at authorization time. It is
// intentionally not inferred from organization membership and is not served
// from the session snapshot, so a role change takes effect on the next
// request.
func (r *Repository) InstanceRole(ctx context.Context, accountID uuid.UUID) (string, error) {
	if r == nil || r.pool == nil {
		return "", ErrNotFound
	}
	var role string
	if err := r.pool.QueryRow(ctx, `SELECT role FROM instance_roles WHERE account_id=$1`, accountID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	return role, nil
}

func (r *Repository) IsInstanceAdmin(ctx context.Context, accountID uuid.UUID) (bool, error) {
	if r == nil || r.pool == nil {
		return false, ErrNotFound
	}
	var allowed bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_roles WHERE account_id=$1 AND role IN ('instance_owner','instance_admin'))`, accountID).Scan(&allowed); err != nil {
		return false, err
	}
	return allowed, nil
}

// IsInstanceAdminSession verifies both the session lifecycle and the current
// instance-level role. It is used by long-lived admin streams, whose opening
// request cannot be the only authorization check.
func (r *Repository) IsInstanceAdminSession(ctx context.Context, accountID, sessionID uuid.UUID) (bool, error) {
	if r == nil || r.pool == nil || accountID == uuid.Nil || sessionID == uuid.Nil {
		return false, ErrNotFound
	}
	var allowed bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM sessions s
			JOIN instance_roles ir ON ir.account_id=s.account_id
			WHERE s.id=$1
			  AND s.account_id=$2
			  AND s.expires_at>now()
			  AND ir.role IN ('instance_owner','instance_admin')
		)`, sessionID, accountID).Scan(&allowed); err != nil {
		return false, err
	}
	return allowed, nil
}
