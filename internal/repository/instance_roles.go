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
