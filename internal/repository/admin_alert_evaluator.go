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

func evaluateAdminMonitorAlertsTx(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, input AdminMonitorCheckInput) error {
	if monitorID == uuid.Nil {
		return ErrInvalidAdminMonitor
	}
	rows, err := tx.Query(ctx, `
		SELECT id,kind,for_seconds,state,pending_since,condition
		FROM admin_alert_rules
		WHERE enabled AND kind IN ('monitor_failure','heartbeat_failure','certificate_expiry')
		  AND condition->>'monitor_id'=$1
		ORDER BY id
		FOR UPDATE`, monitorID.String())
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now().UTC()
	for rows.Next() {
		var ruleID uuid.UUID
		var kind string
		var forSeconds int
		var state string
		var pendingSince *time.Time
		var conditionRaw []byte
		if err := rows.Scan(&ruleID, &kind, &forSeconds, &state, &pendingSince, &conditionRaw); err != nil {
			return err
		}
		var condition map[string]any
		if err := json.Unmarshal(conditionRaw, &condition); err != nil {
			return fmt.Errorf("decode admin alert condition: %w", err)
		}
		trigger := !input.Success
		if kind == "certificate_expiry" && input.Success {
			trigger = certificateExpiryTriggered(condition, input.Details)
		}
		transition := transitionAdminAlert(state, pendingSince, now, trigger, forSeconds)
		var lastValue any
		if input.LatencyMS >= 0 {
			lastValue = float64(input.LatencyMS)
		}
		var lastError any
		if strings.TrimSpace(input.Error) != "" {
			lastError = normalizeAdminMonitorError(input.Error)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE admin_alert_rules
			SET state=$2,pending_since=$3,last_evaluated_at=$4,last_value=$5,last_error=$6,updated_at=$4
			WHERE id=$1`, ruleID, transition.State, transition.PendingSince, now, lastValue, lastError); err != nil {
			return err
		}
		if transition.EventState != "" {
			eventID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			message := "Admin monitor condition is firing"
			if transition.EventState == "resolved" {
				message = "Admin monitor condition recovered"
			}
			if _, err := tx.Exec(ctx, `INSERT INTO admin_alert_events (id,rule_id,state,value,message,occurred_at) VALUES ($1,$2,$3,$4,$5,$6)`, eventID, ruleID, transition.EventState, lastValue, message, now); err != nil {
				return err
			}
		}
	}
	return rows.Err()
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
