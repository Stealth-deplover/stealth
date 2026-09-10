package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrPlanLimitExceeded = errors.New("organization plan limit exceeded")

// PlanLimitError preserves a machine-readable limit violation while keeping
// the HTTP layer independent from SQL details. A plan limit is checked in the
// same transaction as the resource write, so concurrent writers cannot both
// consume the final slot.
type PlanLimitError struct {
	PlanKey  string
	Resource string
	Limit    int64
	Current  int64
}

func (e *PlanLimitError) Error() string {
	return fmt.Sprintf("%s plan limit for %s reached (%d/%d)", e.PlanKey, e.Resource, e.Current, e.Limit)
}

func (e *PlanLimitError) Is(target error) bool { return target == ErrPlanLimitExceeded }

type planDefinition struct {
	Key    string
	Limits domain.OrganizationPlanLimits
}

var planDefinitions = []planDefinition{
	{Key: "free", Limits: domain.OrganizationPlanLimits{Projects: 3, Members: 5, Databases: 5, StorageBuckets: 10, Functions: 10, Sites: 10}},
	{Key: "pro", Limits: domain.OrganizationPlanLimits{Projects: 25, Members: 25, Databases: 50, StorageBuckets: 100, Functions: 100, Sites: 100}},
	{Key: "enterprise", Limits: domain.OrganizationPlanLimits{Projects: -1, Members: -1, Databases: -1, StorageBuckets: -1, Functions: -1, Sites: -1}},
}

func planDefinitionForKey(key string) planDefinition {
	for _, definition := range planDefinitions {
		if definition.Key == key {
			return definition
		}
	}
	return planDefinitions[0]
}

func organizationPlanMembership(ctx context.Context, tx pgx.Tx, organizationID, accountID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND account_id=$2 FOR SHARE)`, organizationID, accountID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrForbidden
	}
	return nil
}

func ensureOrganizationPlanTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) (domain.OrganizationPlan, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO organization_plans (organization_id) VALUES ($1) ON CONFLICT (organization_id) DO NOTHING`, organizationID); err != nil {
		return domain.OrganizationPlan{}, err
	}
	var item domain.OrganizationPlan
	if err := tx.QueryRow(ctx, `
		SELECT organization_id,plan_key,status,to_char(current_period_start,'YYYY-MM-DD'),to_char(current_period_end,'YYYY-MM-DD')
		FROM organization_plans
		WHERE organization_id=$1
		FOR UPDATE`, organizationID).Scan(&item.OrganizationID, &item.PlanKey, &item.Status, &item.CurrentPeriodStart, &item.CurrentPeriodEnd); errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationPlan{}, ErrNotFound
	} else if err != nil {
		return domain.OrganizationPlan{}, err
	}
	item.Limits = planDefinitionForKey(item.PlanKey).Limits
	return item, nil
}

// OrganizationPlan returns the effective plan and bounded resource counts for
// an organization member. It also backfills the default plan row for
// organizations created before the billing migration was installed.
func (r *Repository) OrganizationPlan(ctx context.Context, organizationID, accountID uuid.UUID) (domain.OrganizationPlan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationPlan{}, err
	}
	defer tx.Rollback(ctx)
	if err := organizationPlanMembership(ctx, tx, organizationID, accountID); err != nil {
		return domain.OrganizationPlan{}, err
	}
	item, err := ensureOrganizationPlanTx(ctx, tx, organizationID)
	if err != nil {
		return domain.OrganizationPlan{}, err
	}
	if err := scanOrganizationPlanUsage(ctx, tx, &item, organizationID); err != nil {
		return domain.OrganizationPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OrganizationPlan{}, err
	}
	return item, nil
}

func scanOrganizationPlanUsage(ctx context.Context, tx pgx.Tx, item *domain.OrganizationPlan, organizationID uuid.UUID) error {
	return tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM projects WHERE organization_id=$1),
			(SELECT count(*) FROM organization_memberships WHERE organization_id=$1),
			(SELECT count(*) FROM project_databases d JOIN projects p ON p.id=d.project_id WHERE p.organization_id=$1),
			(SELECT count(*) FROM storage_buckets b JOIN projects p ON p.id=b.project_id WHERE p.organization_id=$1),
			(SELECT count(*) FROM project_functions f JOIN projects p ON p.id=f.project_id WHERE p.organization_id=$1),
			(SELECT count(*) FROM project_sites s JOIN projects p ON p.id=s.project_id WHERE p.organization_id=$1)`, organizationID).Scan(
		&item.Usage.Projects,
		&item.Usage.Members,
		&item.Usage.Databases,
		&item.Usage.StorageBuckets,
		&item.Usage.Functions,
		&item.Usage.Sites,
	)
}

func (r *Repository) enforceOrganizationLimitTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, resource string) error {
	plan, err := ensureOrganizationPlanTx(ctx, tx, organizationID)
	if err != nil {
		return err
	}
	var current int64
	var limit int64
	switch resource {
	case "projects":
		limit = plan.Limits.Projects
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM projects WHERE organization_id=$1`, organizationID).Scan(&current); err != nil {
			return err
		}
	case "members":
		limit = plan.Limits.Members
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE organization_id=$1`, organizationID).Scan(&current); err != nil {
			return err
		}
	case "databases":
		limit = plan.Limits.Databases
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM project_databases d JOIN projects p ON p.id=d.project_id WHERE p.organization_id=$1`, organizationID).Scan(&current); err != nil {
			return err
		}
	case "storage_buckets":
		limit = plan.Limits.StorageBuckets
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM storage_buckets b JOIN projects p ON p.id=b.project_id WHERE p.organization_id=$1`, organizationID).Scan(&current); err != nil {
			return err
		}
	case "functions":
		limit = plan.Limits.Functions
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM project_functions f JOIN projects p ON p.id=f.project_id WHERE p.organization_id=$1`, organizationID).Scan(&current); err != nil {
			return err
		}
	case "sites":
		limit = plan.Limits.Sites
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM project_sites s JOIN projects p ON p.id=s.project_id WHERE p.organization_id=$1`, organizationID).Scan(&current); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown organization plan resource %q", resource)
	}
	if limit >= 0 && current >= limit {
		return &PlanLimitError{PlanKey: plan.PlanKey, Resource: resource, Limit: limit, Current: current}
	}
	return nil
}
