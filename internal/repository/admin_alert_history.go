package repository

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
)

const (
	adminAlertEventCursorVersion = 1
	adminAlertEventProjection    = `
	 e.id::text,e.rule_id_snapshot::text,e.rule_name_snapshot,e.rule_kind_snapshot,e.severity_snapshot,
	 e.state,e.value,e.message,e.occurred_at,(e.rule_id IS NOT NULL)`
)

var ErrInvalidAdminAlertHistory = errors.New("invalid admin alert history query")

// AdminAlertEventQuery is the bounded, shared query contract for global and
// per-rule alert history. RuleID filters immutable snapshots, not the
// nullable live-rule foreign key.
type AdminAlertEventQuery struct {
	RuleID *uuid.UUID
	From   *time.Time
	To     *time.Time
	Cursor *AdminAlertEventCursor
	Limit  int
}

type AdminAlertEventCursor struct {
	OccurredAt time.Time
	ID         uuid.UUID
	RuleID     *uuid.UUID
	From       *time.Time
	To         *time.Time
}

type AdminAlertEventPage struct {
	Items      []domain.AdminAlertEvent
	NextCursor string
}

type adminAlertEventCursorPayload struct {
	Version    int        `json:"v"`
	OccurredAt time.Time  `json:"at"`
	ID         uuid.UUID  `json:"id"`
	RuleID     *uuid.UUID `json:"rule"`
	From       *time.Time `json:"from,omitempty"`
	To         *time.Time `json:"to,omitempty"`
}

// EncodeAdminAlertEventCursor keeps the cursor opaque while retaining the
// exact filters that created it. The payload is versioned so a future cursor
// format can be rejected instead of being interpreted incorrectly.
func EncodeAdminAlertEventCursor(cursor AdminAlertEventCursor) string {
	payload := adminAlertEventCursorPayload{
		Version:    adminAlertEventCursorVersion,
		OccurredAt: cursor.OccurredAt.UTC(),
		ID:         cursor.ID,
		RuleID:     cursor.RuleID,
		From:       utcTimePointer(cursor.From),
		To:         utcTimePointer(cursor.To),
	}
	data, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(data)
}

// DecodeAdminAlertEventCursor validates the opaque, versioned cursor without
// exposing its SQL representation to callers.
func DecodeAdminAlertEventCursor(value string) (AdminAlertEventCursor, error) {
	if strings.TrimSpace(value) == "" {
		return AdminAlertEventCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return AdminAlertEventCursor{}, ErrInvalidAdminAlertHistory
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload adminAlertEventCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return AdminAlertEventCursor{}, ErrInvalidAdminAlertHistory
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return AdminAlertEventCursor{}, ErrInvalidAdminAlertHistory
	}
	if payload.Version != adminAlertEventCursorVersion || payload.ID == uuid.Nil || payload.OccurredAt.IsZero() {
		return AdminAlertEventCursor{}, ErrInvalidAdminAlertHistory
	}
	return AdminAlertEventCursor{
		OccurredAt: payload.OccurredAt.UTC(),
		ID:         payload.ID,
		RuleID:     payload.RuleID,
		From:       utcTimePointer(payload.From),
		To:         utcTimePointer(payload.To),
	}, nil
}

func (r *Repository) QueryAdminAlertEvents(ctx context.Context, query AdminAlertEventQuery) (AdminAlertEventPage, error) {
	query, err := normalizeAdminAlertEventQuery(query)
	if err != nil {
		return AdminAlertEventPage{}, err
	}

	conditions := make([]string, 0, 5)
	args := make([]any, 0, 7)
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if query.RuleID != nil {
		conditions = append(conditions, "e.rule_id_snapshot="+addArg(*query.RuleID)+"::uuid")
	}
	if query.From != nil {
		conditions = append(conditions, "e.occurred_at >= "+addArg(*query.From)+"::timestamptz")
	}
	if query.To != nil {
		conditions = append(conditions, "e.occurred_at <= "+addArg(*query.To)+"::timestamptz")
	}
	if query.Cursor != nil {
		occurredAtArg := addArg(query.Cursor.OccurredAt)
		idArg := addArg(query.Cursor.ID)
		conditions = append(conditions, fmt.Sprintf(
			"(e.occurred_at,e.id) < (%s::timestamptz,%s::uuid)", occurredAtArg, idArg,
		))
	}
	limitArg := addArg(query.Limit + 1)
	statement := `SELECT ` + adminAlertEventProjection + ` FROM admin_alert_events e`
	if len(conditions) > 0 {
		statement += " WHERE " + strings.Join(conditions, " AND ")
	}
	statement += " ORDER BY e.occurred_at DESC,e.id DESC LIMIT " + limitArg

	rows, err := r.pool.Query(ctx, statement, args...)
	if err != nil {
		return AdminAlertEventPage{}, err
	}
	defer rows.Close()
	items := make([]domain.AdminAlertEvent, 0, query.Limit)
	for rows.Next() {
		var item domain.AdminAlertEvent
		if err := scanAdminAlertEvent(rows, &item); err != nil {
			return AdminAlertEventPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AdminAlertEventPage{}, err
	}
	if len(items) <= query.Limit {
		return AdminAlertEventPage{Items: items}, nil
	}

	last := items[query.Limit-1]
	eventID, err := uuid.Parse(last.ID)
	if err != nil {
		return AdminAlertEventPage{}, ErrInvalidAdminAlertHistory
	}
	next := EncodeAdminAlertEventCursor(AdminAlertEventCursor{
		OccurredAt: last.OccurredAt,
		ID:         eventID,
		RuleID:     query.RuleID,
		From:       query.From,
		To:         query.To,
	})
	return AdminAlertEventPage{Items: items[:query.Limit], NextCursor: next}, nil
}

func scanAdminAlertEvent(row interface{ Scan(...any) error }, item *domain.AdminAlertEvent) error {
	return row.Scan(
		&item.ID,
		&item.RuleID,
		&item.RuleName,
		&item.RuleKind,
		&item.Severity,
		&item.State,
		&item.Value,
		&item.Message,
		&item.OccurredAt,
		&item.SourceRuleExists,
	)
}

func normalizeAdminAlertEventQuery(query AdminAlertEventQuery) (AdminAlertEventQuery, error) {
	if query.Limit < 1 || query.Limit > adminAlertMaxLimit {
		return AdminAlertEventQuery{}, ErrInvalidAdminAlertHistory
	}
	if query.RuleID != nil && *query.RuleID == uuid.Nil {
		return AdminAlertEventQuery{}, ErrInvalidAdminAlertHistory
	}
	query.From = utcTimePointer(query.From)
	query.To = utcTimePointer(query.To)
	if query.From != nil && query.To != nil && !query.To.After(*query.From) {
		return AdminAlertEventQuery{}, ErrInvalidAdminAlertHistory
	}
	if query.Cursor == nil {
		return query, nil
	}
	cursor := *query.Cursor
	cursor.OccurredAt = cursor.OccurredAt.UTC()
	cursor.From = utcTimePointer(cursor.From)
	cursor.To = utcTimePointer(cursor.To)
	if cursor.ID == uuid.Nil || cursor.OccurredAt.IsZero() || !sameOptionalUUID(query.RuleID, cursor.RuleID) || !sameOptionalTime(query.From, cursor.From) || !sameOptionalTime(query.To, cursor.To) {
		return AdminAlertEventQuery{}, ErrInvalidAdminAlertHistory
	}
	query.Cursor = &cursor
	return query, nil
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
