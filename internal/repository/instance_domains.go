package repository

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/domainname"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidInstanceDomain = errors.New("invalid instance domain configuration")

func (r *Repository) GetInstanceDomainSettings(ctx context.Context, instanceHostname string) (domain.InstanceDomainSettings, error) {
	if r == nil || r.pool == nil {
		return domain.InstanceDomainSettings{}, ErrNotFound
	}
	canonicalHostname, err := normalizeInstanceHostname(instanceHostname)
	if err != nil {
		return domain.InstanceDomainSettings{}, err
	}
	var workloadBaseDomain *string
	if err := r.pool.QueryRow(ctx, `SELECT workload_base_domain FROM instance_domain_settings WHERE id=TRUE`).Scan(&workloadBaseDomain); errors.Is(err, pgx.ErrNoRows) {
		return domain.InstanceDomainSettings{}, ErrNotFound
	} else if err != nil {
		return domain.InstanceDomainSettings{}, err
	}
	return domain.InstanceDomainSettings{InstanceHostname: canonicalHostname, WorkloadBaseDomain: workloadBaseDomain}, nil
}

// UpdateInstanceDomainSettings performs owner authorization and persistence in
// one transaction. A nil workloadBaseDomain explicitly clears the setting.
func (r *Repository) UpdateInstanceDomainSettings(ctx context.Context, accountID uuid.UUID, instanceHostname string, workloadBaseDomain *string) (domain.InstanceDomainSettings, error) {
	if r == nil || r.pool == nil {
		return domain.InstanceDomainSettings{}, ErrNotFound
	}
	canonicalHostname, err := normalizeInstanceHostname(instanceHostname)
	if err != nil {
		return domain.InstanceDomainSettings{}, err
	}
	canonicalWorkloadDomain, err := normalizeWorkloadBaseDomain(workloadBaseDomain)
	if err != nil {
		return domain.InstanceDomainSettings{}, err
	}
	if canonicalWorkloadDomain != nil && (canonicalHostname == *canonicalWorkloadDomain || domainname.IsSubdomain(canonicalHostname, *canonicalWorkloadDomain)) {
		return domain.InstanceDomainSettings{}, ErrInvalidInstanceDomain
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.InstanceDomainSettings{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceOwnerTx(ctx, tx, accountID); err != nil {
		return domain.InstanceDomainSettings{}, err
	}
	var storedWorkloadDomain *string
	if err := tx.QueryRow(ctx, `
		UPDATE instance_domain_settings
		SET workload_base_domain=$1,updated_at=now()
		WHERE id=TRUE
		RETURNING workload_base_domain`, nullableDomainValue(canonicalWorkloadDomain)).Scan(&storedWorkloadDomain); errors.Is(err, pgx.ErrNoRows) {
		return domain.InstanceDomainSettings{}, ErrNotFound
	} else if err != nil {
		return domain.InstanceDomainSettings{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InstanceDomainSettings{}, err
	}
	return domain.InstanceDomainSettings{InstanceHostname: canonicalHostname, WorkloadBaseDomain: storedWorkloadDomain}, nil
}

func normalizeInstanceHostname(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrInvalidInstanceDomain
	}
	if ip := net.ParseIP(value); ip != nil {
		return strings.ToLower(value), nil
	}
	normalized, err := domainname.NormalizeHostname(value)
	if err != nil {
		return "", ErrInvalidInstanceDomain
	}
	return normalized, nil
}

func normalizeWorkloadBaseDomain(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := domainname.NormalizeDomain(*value)
	if err != nil {
		return nil, ErrInvalidInstanceDomain
	}
	return &normalized, nil
}

func nullableDomainValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
