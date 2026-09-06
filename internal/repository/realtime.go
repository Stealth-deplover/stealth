package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	dbcore "github.com/nazxf/stealth-api/internal/database"
	"github.com/nazxf/stealth-api/internal/domain"
)

var (
	ErrRealtimeForbidden = errors.New("realtime access is forbidden")
	ErrInvalidRealtime   = errors.New("invalid realtime request")
)

const (
	maxRealtimeBatch = 100
)

// ListRealtimeEvents returns the bounded, short-lived project event stream.
// The same event envelope is used by Webhooks, but delivery configuration is
// not required for an event to be retained. Application actors only receive
// permission-filtered database row events; management actors may inspect the
// complete project stream.
func (r *Repository) ListRealtimeEvents(ctx context.Context, projectID uuid.UUID, actor DatabaseActor, after *uuid.UUID, limit int) ([]domain.RealtimeEvent, *uuid.UUID, error) {
	if limit < 1 || limit > maxRealtimeBatch {
		return nil, nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidRealtime, maxRealtimeBatch)
	}
	if err := r.requireRealtimeRead(ctx, projectID, actor); err != nil {
		return nil, nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id,project_id,event_name,target_type,target_id,payload,created_at
		FROM webhook_events
		WHERE project_id=$1 AND ($2::uuid IS NULL OR id>$2) AND expires_at>now()
		ORDER BY id
		LIMIT $3`, projectID, after, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := make([]domain.RealtimeEvent, 0, limit)
	var nextCursor *uuid.UUID
	for rows.Next() {
		var id, eventProjectID uuid.UUID
		var eventName, targetType string
		var targetID *uuid.UUID
		var payload []byte
		var createdAt time.Time
		if err := rows.Scan(&id, &eventProjectID, &eventName, &targetType, &targetID, &payload, &createdAt); err != nil {
			return nil, nil, err
		}
		lastID := id
		nextCursor = &lastID
		event, err := decodeRealtimeEvent(id, eventProjectID, eventName, targetType, targetID, payload, createdAt)
		if err != nil {
			return nil, nil, err
		}
		if actor.IsApplication() && !realtimeApplicationEventVisible(event, actor) {
			continue
		}
		items = append(items, event)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return items, nextCursor, nil
}

func (r *Repository) requireRealtimeRead(ctx context.Context, projectID uuid.UUID, actor DatabaseActor) error {
	switch actor.Kind {
	case DatabaseConsoleActor:
		if actor.AccountID == uuid.Nil {
			return ErrRealtimeForbidden
		}
		return r.requireProjectAccess(ctx, projectID, actor.AccountID)
	case DatabaseAPIKeyActor:
		if !hasRealtimeScope(actor.APIKeyScopes) {
			return ErrRealtimeForbidden
		}
		return requireActiveProjectAPIKey(ctx, r.pool, projectID, actor.APIKeyID, "realtime.read")
	case DatabaseApplicationActor:
		if actor.ProjectUserID == uuid.Nil {
			return ErrRealtimeForbidden
		}
		var active bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_users WHERE id=$1 AND project_id=$2 AND status='active')`, actor.ProjectUserID, projectID).Scan(&active); err != nil {
			return err
		}
		if !active {
			return ErrRealtimeForbidden
		}
		return nil
	default:
		return ErrRealtimeForbidden
	}
}

func hasRealtimeScope(scopes []string) bool {
	for _, scope := range scopes {
		if scope == "realtime.read" {
			return true
		}
	}
	return false
}

func requireActiveProjectAPIKey(ctx context.Context, querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, keyID uuid.UUID, scope string) error {
	var active bool
	if err := querier.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM project_api_keys
		WHERE id=$1 AND project_id=$2 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at>now()) AND $3=ANY(scopes)
	)`, keyID, projectID, scope).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrRealtimeForbidden
	}
	return nil
}

func decodeRealtimeEvent(id, projectID uuid.UUID, eventName, targetType string, targetID *uuid.UUID, payload []byte, createdAt time.Time) (domain.RealtimeEvent, error) {
	var envelope struct {
		ID        string `json:"id"`
		Event     string `json:"event"`
		ProjectID string `json:"project_id"`
		Target    struct {
			Type string  `json:"type"`
			ID   *string `json:"id"`
		} `json:"target"`
		Data      map[string]any `json:"data"`
		CreatedAt string         `json:"created_at"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return domain.RealtimeEvent{}, fmt.Errorf("decode realtime event %s: %w", id, err)
	}
	if envelope.ID != id.String() || envelope.ProjectID != projectID.String() || envelope.Event != eventName || envelope.Target.Type != targetType {
		return domain.RealtimeEvent{}, fmt.Errorf("%w: event envelope does not match its row", ErrInvalidRealtime)
	}
	if envelope.Data == nil {
		envelope.Data = map[string]any{}
	}
	var targetValue *string
	if targetID != nil {
		value := targetID.String()
		targetValue = &value
	}
	if (envelope.Target.ID == nil) != (targetValue == nil) || (envelope.Target.ID != nil && *envelope.Target.ID != *targetValue) {
		return domain.RealtimeEvent{}, fmt.Errorf("%w: event target does not match its row", ErrInvalidRealtime)
	}
	return domain.RealtimeEvent{
		ID:         id.String(),
		ProjectID:  projectID.String(),
		EventName:  eventName,
		TargetType: targetType,
		TargetID:   targetValue,
		Data:       envelope.Data,
		CreatedAt:  createdAt.UTC(),
		Payload:    append(json.RawMessage(nil), payload...),
	}, nil
}

// realtimeApplicationEventVisible deliberately limits application sessions
// to database row events. The permission snapshot is written atomically with
// the mutation, so a delete remains authorizable even after its row is gone.
func realtimeApplicationEventVisible(event domain.RealtimeEvent, actor DatabaseActor) bool {
	if !strings.HasPrefix(event.EventName, "database_row.") {
		return false
	}
	marker, ok := event.Data["realtime"].(map[string]any)
	if !ok {
		return false
	}
	rowSecurity, ok := marker["row_security"].(bool)
	if !ok {
		return false
	}
	tablePermissions, ok := stringSlice(marker["table_read_permissions"])
	if !ok {
		return false
	}
	rowPermissions, ok := stringSlice(marker["row_read_permissions"])
	if !ok {
		return false
	}
	if !rowSecurity {
		return dbcore.Grants(tablePermissions, dbcore.Actor{Authenticated: true, UserID: actor.ProjectUserID})
	}
	return dbcore.Grants(rowPermissions, dbcore.Actor{Authenticated: true, UserID: actor.ProjectUserID}) || dbcore.Grants(tablePermissions, dbcore.Actor{Authenticated: true, UserID: actor.ProjectUserID})
}

func stringSlice(value any) ([]string, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

// PruneExpiredWebhookEvents removes both stale realtime events and any
// delivery rows that cascade from them. It is intentionally separate from
// delivery claiming so operators can schedule retention independently.
func (r *Repository) PruneExpiredWebhookEvents(ctx context.Context) (int64, error) {
	result, err := r.pool.Exec(ctx, `DELETE FROM webhook_events WHERE expires_at<=now()`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
