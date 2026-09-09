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
	ErrNoRealtimeEvent   = errors.New("no realtime event available")
)

const (
	maxRealtimeBatch           = 100
	maxRealtimePublishAttempts = 12
)

// RealtimePublishJob is the publisher's leased projection of one durable
// outbox row. The payload is already sanitized and can be sent without a
// second database read.
type RealtimePublishJob struct {
	EventID      uuid.UUID
	ProjectID    uuid.UUID
	EventName    string
	Payload      []byte
	AttemptCount int
}

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
		SELECT id,project_id,organization_id,event_name,target_type,target_id,event_version,correlation_id,payload,occurred_at,created_at
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
		var id, eventProjectID, organizationID uuid.UUID
		var eventName, targetType string
		var targetID *uuid.UUID
		var version int
		var correlationID *string
		var payload []byte
		var occurredAt, createdAt time.Time
		if err := rows.Scan(&id, &eventProjectID, &organizationID, &eventName, &targetType, &targetID, &version, &correlationID, &payload, &occurredAt, &createdAt); err != nil {
			return nil, nil, err
		}
		lastID := id
		nextCursor = &lastID
		event, err := decodeRealtimeEventWithMetadata(id, eventProjectID, organizationID, eventName, targetType, targetID, version, correlationID, payload, occurredAt, createdAt)
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

// ClaimNextRealtimeEvent leases one pending outbox event. SKIP LOCKED keeps
// multiple publisher workers safe while the lease makes a crash recoverable.
func (r *Repository) ClaimNextRealtimeEvent(ctx context.Context, workerID string, leaseAge time.Duration) (RealtimePublishJob, error) {
	return r.claimNextRealtimeEvent(ctx, workerID, leaseAge, nil)
}

// ClaimNextRealtimeEventForProject is the same leasing primitive restricted to
// one project. It is useful for project-scoped operational work and keeps
// integration assertions deterministic when a shared database contains other
// pending tenants.
func (r *Repository) ClaimNextRealtimeEventForProject(ctx context.Context, projectID uuid.UUID, workerID string, leaseAge time.Duration) (RealtimePublishJob, error) {
	if projectID == uuid.Nil {
		return RealtimePublishJob{}, ErrInvalidRealtime
	}
	return r.claimNextRealtimeEvent(ctx, workerID, leaseAge, &projectID)
}

func (r *Repository) claimNextRealtimeEvent(ctx context.Context, workerID string, leaseAge time.Duration, projectID *uuid.UUID) (RealtimePublishJob, error) {
	if !validFunctionWorkerID(workerID) || leaseAge <= 0 {
		return RealtimePublishJob{}, ErrInvalidRealtime
	}
	var projectFilter any
	if projectID != nil {
		projectFilter = *projectID
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RealtimePublishJob{}, err
	}
	defer tx.Rollback(ctx)
	var job RealtimePublishJob
	err = tx.QueryRow(ctx, `
		SELECT id,project_id,event_name,payload,publish_attempts
		FROM webhook_events
		WHERE publish_status='pending'
		  AND ($2::uuid IS NULL OR project_id=$2)
		  AND (available_at<=now() OR publish_leased_at IS NOT NULL)
		  AND expires_at>now()
		  AND (publish_leased_at IS NULL OR publish_leased_at < now() - ($1::double precision * interval '1 second'))
		ORDER BY available_at,id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, leaseAge.Seconds(), projectFilter).Scan(&job.EventID, &job.ProjectID, &job.EventName, &job.Payload, &job.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return RealtimePublishJob{}, ErrNoRealtimeEvent
	}
	if err != nil {
		return RealtimePublishJob{}, err
	}
	job.AttemptCount++
	if _, err := tx.Exec(ctx, `UPDATE webhook_events SET publish_attempts=$2,publish_leased_at=now(),publish_worker_id=$3,last_publish_error=NULL WHERE id=$1 AND publish_status='pending'`, job.EventID, job.AttemptCount, workerID); err != nil {
		return RealtimePublishJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RealtimePublishJob{}, err
	}
	return job, nil
}

// RequeueStaleRealtimeEvents returns publisher leases to the pending queue.
func (r *Repository) RequeueStaleRealtimeEvents(ctx context.Context, leaseAge time.Duration) (int64, error) {
	if leaseAge <= 0 {
		return 0, ErrInvalidRealtime
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE webhook_events
		SET publish_leased_at=NULL,publish_worker_id=NULL,available_at=LEAST(available_at,now())
		WHERE publish_status='pending' AND publish_leased_at IS NOT NULL
		  AND publish_leased_at < now() - ($1::double precision * interval '1 second')`, leaseAge.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// FinishRealtimeEvent acknowledges or schedules a bounded retry for a leased
// event. A successful Redis publish followed by a worker crash can produce a
// duplicate notification, so consumers must remain idempotent.
func (r *Repository) FinishRealtimeEvent(ctx context.Context, eventID uuid.UUID, workerID string, success bool, retryAt *time.Time, lastError string) error {
	if eventID == uuid.Nil || !validFunctionWorkerID(workerID) {
		return ErrInvalidRealtime
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status, owner string
	var attempts int
	if err := tx.QueryRow(ctx, `SELECT publish_status,COALESCE(publish_worker_id,''),publish_attempts FROM webhook_events WHERE id=$1 FOR UPDATE`, eventID).Scan(&status, &owner, &attempts); errors.Is(err, pgx.ErrNoRows) {
		return ErrNoRealtimeEvent
	} else if err != nil {
		return err
	}
	if status != "pending" || owner != workerID {
		return ErrNoRealtimeEvent
	}
	if success {
		_, err = tx.Exec(ctx, `UPDATE webhook_events SET publish_status='published',published_at=now(),publish_leased_at=NULL,publish_worker_id=NULL,last_publish_error=NULL WHERE id=$1`, eventID)
	} else if retryAt != nil && attempts < maxRealtimePublishAttempts {
		errValue := truncateRealtimeError(lastError)
		_, err = tx.Exec(ctx, `UPDATE webhook_events SET available_at=$2,publish_leased_at=NULL,publish_worker_id=NULL,last_publish_error=$3 WHERE id=$1`, eventID, retryAt.UTC(), errValue)
	} else {
		errValue := truncateRealtimeError(lastError)
		_, err = tx.Exec(ctx, `UPDATE webhook_events SET publish_status='failed',publish_leased_at=NULL,publish_worker_id=NULL,last_publish_error=$2 WHERE id=$1`, eventID, errValue)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func truncateRealtimeError(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > 4000 {
		value = value[:4000]
	}
	return &value
}

// PendingRealtimeEvents returns the non-expired publication backlog for an
// operator-facing queue-depth metric. It intentionally does not expose tenant
// identifiers or event payloads.
func (r *Repository) PendingRealtimeEvents(ctx context.Context) (int64, error) {
	var count int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE publish_status='pending' AND expires_at>now()`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
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
	return decodeRealtimeEventWithMetadata(id, projectID, uuid.Nil, eventName, targetType, targetID, 1, nil, payload, createdAt, createdAt)
}

func decodeRealtimeEventWithMetadata(id, projectID, organizationID uuid.UUID, eventName, targetType string, targetID *uuid.UUID, version int, correlationID *string, payload []byte, occurredAt, createdAt time.Time) (domain.RealtimeEvent, error) {
	var envelope struct {
		ID             string  `json:"id"`
		Event          string  `json:"event"`
		Type           string  `json:"type"`
		Version        int     `json:"version"`
		OrganizationID string  `json:"organization_id"`
		ProjectID      string  `json:"project_id"`
		ResourceID     *string `json:"resource_id"`
		CorrelationID  *string `json:"correlation_id"`
		Target         struct {
			Type string  `json:"type"`
			ID   *string `json:"id"`
		} `json:"target"`
		Data      map[string]any `json:"data"`
		Payload   map[string]any `json:"payload"`
		CreatedAt string         `json:"created_at"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return domain.RealtimeEvent{}, fmt.Errorf("decode realtime event %s: %w", id, err)
	}
	if envelope.ID != id.String() || envelope.ProjectID != projectID.String() || envelope.Event != eventName || envelope.Target.Type != targetType {
		return domain.RealtimeEvent{}, fmt.Errorf("%w: event envelope does not match its row", ErrInvalidRealtime)
	}
	if envelope.Type != "" && envelope.Type != eventName {
		return domain.RealtimeEvent{}, fmt.Errorf("%w: event type does not match its row", ErrInvalidRealtime)
	}
	if envelope.Version == 0 {
		envelope.Version = version
	}
	if envelope.Version != version {
		return domain.RealtimeEvent{}, fmt.Errorf("%w: event version does not match its row", ErrInvalidRealtime)
	}
	if envelope.OrganizationID != "" && organizationID != uuid.Nil && envelope.OrganizationID != organizationID.String() {
		return domain.RealtimeEvent{}, fmt.Errorf("%w: organization does not match its row", ErrInvalidRealtime)
	}
	data := envelope.Payload
	if data == nil {
		data = envelope.Data
	}
	if data == nil {
		data = map[string]any{}
	}
	var targetValue *string
	if targetID != nil {
		value := targetID.String()
		targetValue = &value
	}
	if (envelope.Target.ID == nil) != (targetValue == nil) || (envelope.Target.ID != nil && *envelope.Target.ID != *targetValue) {
		return domain.RealtimeEvent{}, fmt.Errorf("%w: event target does not match its row", ErrInvalidRealtime)
	}
	resolvedCorrelation := ""
	if correlationID != nil {
		resolvedCorrelation = *correlationID
	}
	if envelope.CorrelationID != nil {
		resolvedCorrelation = *envelope.CorrelationID
	}
	resolvedOccurredAt := occurredAt
	if resolvedOccurredAt.IsZero() {
		resolvedOccurredAt = createdAt
	}
	resolvedOrganization := ""
	if organizationID != uuid.Nil {
		resolvedOrganization = organizationID.String()
	}
	if envelope.OrganizationID != "" {
		resolvedOrganization = envelope.OrganizationID
	}
	resourceID := envelope.ResourceID
	if resourceID == nil {
		resourceID = targetValue
	}
	if resourceID != nil && targetValue != nil && *resourceID != *targetValue {
		return domain.RealtimeEvent{}, fmt.Errorf("%w: resource does not match its row", ErrInvalidRealtime)
	}
	return domain.RealtimeEvent{
		ID:             id.String(),
		OrganizationID: resolvedOrganization,
		ProjectID:      projectID.String(),
		EventName:      eventName,
		Version:        envelope.Version,
		TargetType:     targetType,
		TargetID:       targetValue,
		ResourceID:     resourceID,
		CorrelationID:  resolvedCorrelation,
		Data:           data,
		OccurredAt:     resolvedOccurredAt.UTC(),
		CreatedAt:      createdAt.UTC(),
		Payload:        append(json.RawMessage(nil), payload...),
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
