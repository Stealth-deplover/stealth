package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListAuditEvents returns the newest tenant audit events visible to a member.
// UUIDv7 event IDs provide a monotonic cursor for this descending projection;
// the API keeps the cursor opaque to callers while validating it as a UUID.
func (r *Repository) ListAuditEvents(ctx context.Context, organizationID, accountID uuid.UUID, limit int, cursor string) ([]domain.AuditEvent, string, error) {
	if err := r.requireMembership(ctx, organizationID, accountID); err != nil {
		return nil, "", err
	}
	return r.listAuditEvents(ctx, organizationID, accountID, "", limit, cursor)
}

// ListProjectAuditEvents returns only events that carry the project scope in
// their metadata (plus the project creation event, whose target is the
// project itself). Older events without that scope stay visible at the
// organization level but cannot be misattributed to a project.
func (r *Repository) ListProjectAuditEvents(ctx context.Context, projectID, accountID uuid.UUID, limit int, cursor string) ([]domain.AuditEvent, string, error) {
	var organizationID uuid.UUID
	if err := r.pool.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, projectID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	} else if err != nil {
		return nil, "", err
	}
	if err := r.requireMembership(ctx, organizationID, accountID); err != nil {
		return nil, "", err
	}
	return r.listAuditEvents(ctx, organizationID, accountID, projectID.String(), limit, cursor)
}

func (r *Repository) listAuditEvents(ctx context.Context, organizationID, accountID uuid.UUID, projectID string, limit int, cursor string) ([]domain.AuditEvent, string, error) {
	query := `
		SELECT e.id::text,e.organization_id::text,e.actor_account_id::text,a.email,
		       e.action,e.target_type,e.target_id::text,e.metadata,e.created_at
		FROM audit_events e
		LEFT JOIN accounts a ON a.id=e.actor_account_id
		WHERE e.organization_id=$1 AND ($2='' OR e.id::text<$2)`
	args := []any{organizationID, cursor, limit + 1}
	if projectID != "" {
		query += ` AND (e.metadata->>'project_id'=$4 OR (e.target_type='project' AND e.target_id::text=$4))`
		args = append(args, projectID)
	}
	query += `
		ORDER BY e.id DESC
		LIMIT $3`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	items := make([]domain.AuditEvent, 0, limit)
	for rows.Next() {
		var item domain.AuditEvent
		var metadata []byte
		if err := rows.Scan(
			&item.ID,
			&item.OrganizationID,
			&item.ActorAccountID,
			&item.ActorEmail,
			&item.Action,
			&item.TargetType,
			&item.TargetID,
			&metadata,
			&item.CreatedAt,
		); err != nil {
			return nil, "", err
		}
		if len(metadata) == 0 {
			item.Metadata = json.RawMessage(`{}`)
		} else if !json.Valid(metadata) {
			// The column is JSONB, but keep the response contract safe if a
			// future driver or migration returns malformed bytes.
			item.Metadata = json.RawMessage(`{}`)
		} else {
			item.Metadata = json.RawMessage(metadata)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}
