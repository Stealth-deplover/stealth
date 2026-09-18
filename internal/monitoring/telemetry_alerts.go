package monitoring

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
	"github.com/google/uuid"
)

const (
	telemetryAlertRuleLimit = 100
	defaultAlertWindow      = 5 * time.Minute
	maxAlertWindow          = 24 * time.Hour
)

// TelemetryAlertPersistence is implemented by the control-plane repository.
// The evaluator only receives safe rule projections and sends measurements
// back through the repository's transactional state machine.
type TelemetryAlertPersistence interface {
	ListAdminAlertRules(context.Context, int) ([]domain.AdminAlertRule, error)
	EvaluateAdminAlert(context.Context, uuid.UUID, bool, *float64, string) error
}

type TelemetryAlertEvaluator struct {
	Store    TelemetryAlertPersistence
	Explorer telemetry.AlertExplorer
	Logger   *slog.Logger
}

func NewTelemetryAlertEvaluator(store TelemetryAlertPersistence, explorer telemetry.AlertExplorer, logger *slog.Logger) (*TelemetryAlertEvaluator, error) {
	if store == nil || explorer == nil {
		return nil, fmt.Errorf("telemetry alert evaluator requires a store and explorer")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TelemetryAlertEvaluator{Store: store, Explorer: explorer, Logger: logger}, nil
}

// RunOnce evaluates only rules backed by ClickHouse measurements. Monitor
// rules remain transactionally evaluated by CompleteAdminMonitorCheck. A
// telemetry backend outage leaves the last durable alert state untouched so a
// transient outage cannot manufacture a recovery event.
func (e *TelemetryAlertEvaluator) RunOnce(ctx context.Context) (int, error) {
	if e == nil || e.Store == nil || e.Explorer == nil {
		return 0, fmt.Errorf("telemetry alert evaluator is not configured")
	}
	rules, err := e.Store.ListAdminAlertRules(ctx, telemetryAlertRuleLimit)
	if err != nil {
		return 0, err
	}
	evaluated := 0
	for _, rule := range rules {
		if !rule.Enabled || !isTelemetryAlertKind(rule.Kind) {
			continue
		}
		compiled, err := compileTelemetryAlert(rule)
		if err != nil {
			// The API rejects new invalid definitions, but old rows must not
			// make the trusted worker fail or emit a misleading alert.
			e.logError("invalid telemetry alert rule", err)
			continue
		}
		measurement, err := e.Explorer.EvaluateAlert(ctx, compiled.query)
		if err != nil {
			return evaluated, err
		}
		if !measurement.Available {
			continue
		}
		trigger, err := compareAlertValue(measurement.Value, compiled.operator, compiled.threshold)
		if err != nil {
			e.logError("invalid telemetry alert comparison", err)
			continue
		}
		ruleID, err := uuid.Parse(rule.ID)
		if err != nil || ruleID == uuid.Nil {
			e.logError("telemetry alert has invalid identifier", fmt.Errorf("rule id is invalid"))
			continue
		}
		message := fmt.Sprintf("%s measured %s %s %s", rule.Kind, formatAlertValue(measurement.Value), compiled.operator, formatAlertValue(compiled.threshold))
		if err := e.Store.EvaluateAdminAlert(ctx, ruleID, trigger, &measurement.Value, message); err != nil {
			return evaluated, err
		}
		evaluated++
	}
	return evaluated, nil
}

type compiledTelemetryAlert struct {
	query     telemetry.AlertQuery
	operator  string
	threshold float64
}

func compileTelemetryAlert(rule domain.AdminAlertRule) (compiledTelemetryAlert, error) {
	condition := rule.Condition
	operator, threshold, err := alertComparison(condition)
	if err != nil {
		return compiledTelemetryAlert{}, err
	}
	window, err := alertWindow(condition)
	if err != nil {
		return compiledTelemetryAlert{}, err
	}
	query := telemetry.AlertQuery{Kind: rule.Kind, Range: telemetry.TimeRange{From: time.Now().UTC().Add(-window), To: time.Now().UTC()}}
	query.Service = boundedConditionString(condition, "service")
	switch rule.Kind {
	case "metric_threshold":
		query.Metric = boundedConditionString(condition, "metric")
		query.Aggregation = boundedConditionString(condition, "aggregation")
		if query.Metric == "" {
			return compiledTelemetryAlert{}, fmt.Errorf("metric is required")
		}
	case "disk_pressure":
		query.Metric = boundedConditionString(condition, "metric")
		if query.Metric == "" {
			query.Metric = "system.filesystem.utilization"
		}
	case "error_rate", "service_health":
		if rule.Kind == "service_health" && query.Service == "" {
			return compiledTelemetryAlert{}, fmt.Errorf("service is required")
		}
	case "latency":
		query.Percentile = boundedConditionString(condition, "percentile")
	case "log_match":
		query.Search = boundedConditionString(condition, "search")
		if query.Search == "" {
			query.Search = boundedConditionString(condition, "message")
		}
		query.Level = boundedConditionString(condition, "level")
		if query.Search == "" {
			return compiledTelemetryAlert{}, fmt.Errorf("search is required")
		}
	}
	return compiledTelemetryAlert{query: query, operator: operator, threshold: threshold}, nil
}

func alertComparison(condition map[string]any) (string, float64, error) {
	operator, ok := condition["operator"].(string)
	operator = strings.ToLower(strings.TrimSpace(operator))
	if !ok || (operator != "gt" && operator != "gte" && operator != "lt" && operator != "lte") {
		return "", 0, fmt.Errorf("operator is invalid")
	}
	threshold, ok := condition["threshold"].(float64)
	if !ok || math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return "", 0, fmt.Errorf("threshold is invalid")
	}
	return operator, threshold, nil
}

func alertWindow(condition map[string]any) (time.Duration, error) {
	value, ok := condition["window_seconds"]
	if !ok {
		return defaultAlertWindow, nil
	}
	seconds, ok := value.(float64)
	if !ok || seconds < 30 || seconds > maxAlertWindow.Seconds() || math.Trunc(seconds) != seconds {
		return 0, fmt.Errorf("window_seconds is invalid")
	}
	return time.Duration(seconds) * time.Second, nil
}

func boundedConditionString(condition map[string]any, key string) string {
	value, ok := condition[key].(string)
	if !ok {
		return ""
	}
	value = strings.TrimSpace(value)
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func compareAlertValue(value float64, operator string, threshold float64) (bool, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return false, fmt.Errorf("alert value is not finite")
	}
	switch operator {
	case "gt":
		return value > threshold, nil
	case "gte":
		return value >= threshold, nil
	case "lt":
		return value < threshold, nil
	case "lte":
		return value <= threshold, nil
	default:
		return false, fmt.Errorf("operator is invalid")
	}
}

func formatAlertValue(value float64) string {
	return strconv.FormatFloat(value, 'f', 4, 64)
}

func isTelemetryAlertKind(kind string) bool {
	switch kind {
	case "metric_threshold", "error_rate", "latency", "log_match", "service_health", "disk_pressure":
		return true
	default:
		return false
	}
}

func (e *TelemetryAlertEvaluator) logError(message string, err error) {
	if e == nil || e.Logger == nil || err == nil {
		return
	}
	e.Logger.Warn(message, "error", safeWorkerError(err))
}
