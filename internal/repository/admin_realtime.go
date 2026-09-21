package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/realtime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	adminRealtimeMaxBatch     = 100
	adminRealtimeExpiry       = 24 * time.Hour
	adminRealtimePruneMax     = 1000
	adminRealtimeEventVersion = 1
)

var (
	ErrInvalidAdminRealtime = errors.New("invalid admin realtime request")
	ErrNoAdminRealtimeEvent = errors.New("no admin realtime event available")
)

// AdminRealtimeEvent is a bounded invalidation notification. It deliberately
// contains no resource snapshot; clients refetch the authenticated API state.
type AdminRealtimeEvent struct {
	ID         uuid.UUID
	EventName  string
	TargetType string
	TargetID   *uuid.UUID
	Payload    json.RawMessage
	OccurredAt time.Time
}

// enqueueAdminRealtimeEventTx records an instance-admin notification in the
// same transaction as the state change that caused it. The payload is passed
// through the shared realtime sanitizer and is never allowed to contain
// credentials or arbitrary resource data.
func enqueueAdminRealtimeEventTx(ctx context.Context, tx pgx.Tx, eventName, targetType string, targetID uuid.UUID, payload map[string]any) error {
	if !strings.HasPrefix(eventName, "admin.") || len(eventName) < 9 || len(eventName) > 160 {
		return ErrInvalidAdminRealtime
	}
	if len(targetType) < 3 || len(targetType) > 80 || strings.ContainsAny(targetType, "\x00\r\n") {
		return ErrInvalidAdminRealtime
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	safePayload := realtime.SafePayload(payload)
	envelope := map[string]any{
		"id":          id.String(),
		"type":        eventName,
		"version":     adminRealtimeEventVersion,
		"occurred_at": now,
		"resource_id": func() any {
			if targetID == uuid.Nil {
				return nil
			}
			return targetID.String()
		}(),
		"payload": safePayload,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil || len(encoded) > 16384 {
		return ErrInvalidAdminRealtime
	}
	var target any
	if targetID != uuid.Nil {
		target = targetID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO admin_realtime_events (id,event_name,target_type,target_id,payload,occurred_at,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, eventName, targetType, target, encoded, now, now.Add(adminRealtimeExpiry))
	return err
}

// ResolveAdminRealtimeStartCursor returns the requested retained event or the
// current tail. A new stream therefore does not replay the retention window,
// while reconnects with a retained Last-Event-ID receive every later event.
func (r *Repository) ResolveAdminRealtimeStartCursor(ctx context.Context, requested *uuid.UUID) (*uuid.UUID, error) {
	if r == nil || r.pool == nil || (requested != nil && *requested == uuid.Nil) {
		return nil, ErrInvalidAdminRealtime
	}
	if requested != nil {
		var retained uuid.UUID
		err := r.pool.QueryRow(ctx, `
			SELECT id FROM admin_realtime_events
			WHERE id=$1 AND expires_at>now()`, *requested).Scan(&retained)
		if err == nil {
			return &retained, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	var tail uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT id FROM admin_realtime_events
		WHERE expires_at>now()
		ORDER BY id DESC
		LIMIT 1`).Scan(&tail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &tail, nil
}

// ListAdminRealtimeEvents returns the next bounded batch after a cursor.
// UUIDv7 IDs provide the same monotonic ordering used by project realtime.
func (r *Repository) ListAdminRealtimeEvents(ctx context.Context, after *uuid.UUID, limit int) ([]AdminRealtimeEvent, *uuid.UUID, error) {
	if r == nil || r.pool == nil || limit < 1 || limit > adminRealtimeMaxBatch || (after != nil && *after == uuid.Nil) {
		return nil, nil, ErrInvalidAdminRealtime
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id,event_name,target_type,target_id,payload,occurred_at
		FROM admin_realtime_events
		WHERE ($1::uuid IS NULL OR id>$1) AND expires_at>now()
		ORDER BY id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := make([]AdminRealtimeEvent, 0, limit)
	var next *uuid.UUID
	for rows.Next() {
		var item AdminRealtimeEvent
		if err := rows.Scan(&item.ID, &item.EventName, &item.TargetType, &item.TargetID, &item.Payload, &item.OccurredAt); err != nil {
			return nil, nil, err
		}
		if !json.Valid(item.Payload) {
			return nil, nil, fmt.Errorf("%w: malformed event payload", ErrInvalidAdminRealtime)
		}
		last := item.ID
		next = &last
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return items, next, nil
}

// PruneExpiredAdminRealtimeEvents removes a bounded batch so the notification
// table remains finite without making an update transaction perform cleanup.
func (r *Repository) PruneExpiredAdminRealtimeEvents(ctx context.Context, limit int) (int64, error) {
	if r == nil || r.pool == nil || limit < 1 || limit > adminRealtimePruneMax {
		return 0, ErrInvalidAdminRealtime
	}
	result, err := r.pool.Exec(ctx, `
		DELETE FROM admin_realtime_events
		WHERE id IN (
			SELECT id FROM admin_realtime_events
			WHERE expires_at<=now()
			ORDER BY expires_at,id
			LIMIT $1
		)`, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
