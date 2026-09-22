package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func workloadDomainTx(ctx context.Context, tx pgx.Tx) (*string, error) {
	var workloadBaseDomain *string
	if err := tx.QueryRow(ctx, `
		SELECT workload_base_domain
		FROM instance_domain_settings
		WHERE id=TRUE
		FOR UPDATE`).Scan(&workloadBaseDomain); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return workloadBaseDomain, nil
}

// workloadDomainConflictsWithSiteDomainsTx checks all custom-domain rows,
// including pending and disabled rows. Keeping the namespace clear before a
// domain becomes verified prevents a later verification from colliding with a
// platform hostname.
func workloadDomainConflictsWithSiteDomainsTx(ctx context.Context, tx pgx.Tx, workloadBaseDomain *string) (bool, error) {
	if workloadBaseDomain == nil {
		return false, nil
	}
	var conflicts bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM site_domains
			WHERE hostname=$1 OR hostname LIKE '%.' || $1
		)`, *workloadBaseDomain).Scan(&conflicts)
	return conflicts, err
}
