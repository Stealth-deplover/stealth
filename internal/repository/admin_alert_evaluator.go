package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type adminAlertTransition struct {
	State        string
	PendingSince *time.Time
	EventState   string
}

// transitionAdminAlert is a small deterministic state machine. It is kept
// independent of SQL so pending/for-duration behavior can be regression
// tested without a database.
func transitionAdminAlert(current string, pendingSince *time.Time, now time.Time, trigger bool, forSeconds int) adminAlertTransition {
	if current == "muted" {
		return adminAlertTransition{State: "muted", PendingSince: pendingSince}
	}
	if trigger {
		switch current {
		case "firing":
			return adminAlertTransition{State: "firing", PendingSince: pendingSince}
		case "pending":
			if pendingSince != nil && !now.Before(pendingSince.Add(time.Duration(forSeconds)*time.Second)) {
				return adminAlertTransition{State: "firing", EventState: "firing"}
			}
			return adminAlertTransition{State: "pending", PendingSince: pendingSince}
		default:
			if forSeconds > 0 {
				return adminAlertTransition{State: "pending", PendingSince: &now}
			}
			return adminAlertTransition{State: "firing", EventState: "firing"}
		}
	}
	if current == "pending" || current == "firing" {
		return adminAlertTransition{State: "resolved", EventState: "resolved"}
	}
	return adminAlertTransition{State: "normal"}
}

// EvaluateAdminAlert applies one observation to the durable alert state
// machine. It is used by trusted workers for telemetry-backed rules. The
// transition, event insertion, and notification enqueue happen in one
// transaction so a firing or recovery cannot be observed without its
// corresponding delivery work.
func (r *Repository) EvaluateAdminAlert(ctx context.Context, ruleID uuid.UUID, trigger bool, value *float64, message string) error {
	if r == nil || r.pool == nil || ruleID == uuid.Nil {
		return ErrInvalidAdminAlert
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := evaluateAdminAlertTx(ctx, tx, ruleID, trigger, value, "", message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func evaluateAdminAlertTx(ctx context.Context, tx pgx.Tx, ruleID uuid.UUID, trigger bool, value *float64, lastError, message string) (uuid.UUID, error) {
	var name, kind, severity, state string
	var condition json.RawMessage
	var forSeconds int
	var pendingSince *time.Time
	var enabled bool
	if err := tx.QueryRow(ctx, `
		SELECT name,kind,condition,severity,state,for_seconds,pending_since,enabled
		FROM admin_alert_rules WHERE id=$1 FOR UPDATE`, ruleID).Scan(&name, &kind, &condition, &severity, &state, &forSeconds, &pendingSince, &enabled); err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	if !enabled {
		return uuid.Nil, nil
	}
	now := time.Now().UTC()
	transition := transitionAdminAlert(state, pendingSince, now, trigger, forSeconds)
	lastError = normalizeAdminMonitorError(lastError)
	if _, err := tx.Exec(ctx, `
		UPDATE admin_alert_rules
		SET state=$2,pending_since=$3,last_evaluated_at=$4,last_value=$5,last_error=$6,updated_at=$4
		WHERE id=$1`, ruleID, transition.State, transition.PendingSince, now, value, nullableError(lastError)); err != nil {
		return uuid.Nil, err
	}
	if transition.EventState == "" {
		return uuid.Nil, nil
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	message = normalizeAdminAlertMessage(message)
	if transition.EventState == "resolved" {
		message = strings.TrimSpace(message)
		if strings.HasSuffix(message, " is firing") {
			message = strings.TrimSuffix(message, " is firing") + " recovered"
		} else {
			message += " recovered"
		}
		message = normalizeAdminAlertMessage(message)
	}
	if message == "" {
		message = "Admin alert condition changed"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO admin_alert_events (
			id,rule_id,rule_id_snapshot,rule_name_snapshot,rule_kind_snapshot,severity_snapshot,condition_snapshot,
			state,value,message,occurred_at
		) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		eventID, ruleID, name, kind, severity, condition, transition.EventState, value, message, now); err != nil {
		return uuid.Nil, err
	}
	if err := enqueueAdminNotificationDeliveriesTx(ctx, tx, eventID); err != nil {
		return uuid.Nil, err
	}
	if err := enqueueAdminRealtimeEventTx(ctx, tx, "admin.alert.updated", "admin_alert_rule", ruleID, map[string]any{"state": transition.EventState}); err != nil {
		return uuid.Nil, err
	}
	return eventID, nil
}

func evaluateAdminMonitorAlertsTx(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, input AdminMonitorCheckInput) error {
	if monitorID == uuid.Nil {
		return ErrInvalidAdminMonitor
	}
	// CompleteAdminMonitorCheck holds the monitor row before reaching this
	// monitor-specific rule evaluation boundary. Do not introduce rule ->
	// monitor locking below.
	rows, err := tx.Query(ctx, `
		SELECT id,kind,condition
		FROM admin_alert_rules
		WHERE enabled AND kind IN ('monitor_failure','heartbeat_failure','certificate_expiry')
		  AND condition->>'monitor_id'=$1
		ORDER BY id
		FOR UPDATE`, monitorID.String())
	if err != nil {
		return err
	}
	type monitorAlertRule struct {
		id        uuid.UUID
		kind      string
		condition []byte
	}
	rules := make([]monitorAlertRule, 0)
	for rows.Next() {
		var rule monitorAlertRule
		if err := rows.Scan(&rule.id, &rule.kind, &rule.condition); err != nil {
			return err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, rule := range rules {
		var condition map[string]any
		if err := json.Unmarshal(rule.condition, &condition); err != nil {
			return fmt.Errorf("decode admin alert condition: %w", err)
		}
		trigger := !input.Success
		if rule.kind == "certificate_expiry" && input.Success {
			trigger = certificateExpiryTriggered(condition, input.Details)
		}
		var lastValue *float64
		if input.LatencyMS >= 0 {
			value := float64(input.LatencyMS)
			lastValue = &value
		}
		message := "Admin monitor condition is firing"
		if rule.kind == "certificate_expiry" {
			message = "Admin certificate condition is firing"
		}
		if _, err := evaluateAdminAlertTx(ctx, tx, rule.id, trigger, lastValue, input.Error, message); err != nil {
			return err
		}
	}
	return nil
}

func normalizeAdminAlertMessage(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\x00' || r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, value))
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}

func certificateExpiryTriggered(condition map[string]any, details json.RawMessage) bool {
	threshold := 0.0
	if value, ok := condition["days"].(float64); ok {
		threshold = value
	} else if value, ok := condition["threshold"].(float64); ok {
		threshold = value
	}
	if threshold <= 0 || len(details) == 0 {
		return false
	}
	var values map[string]any
	if json.Unmarshal(details, &values) != nil {
		return false
	}
	days, ok := values["days_remaining"].(float64)
	return ok && days < threshold
}
