package monitoring

import (
	"context"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/telemetry"
	"github.com/google/uuid"
)

type fakeTelemetryAlertStore struct {
	rules       []domain.AdminAlertRule
	evaluations []telemetryAlertEvaluation
}

type telemetryAlertEvaluation struct {
	id      uuid.UUID
	trigger bool
	value   *float64
	message string
}

func (f *fakeTelemetryAlertStore) ListAdminAlertRules(context.Context, int) ([]domain.AdminAlertRule, error) {
	return f.rules, nil
}

func (f *fakeTelemetryAlertStore) EvaluateAdminAlert(_ context.Context, id uuid.UUID, trigger bool, value *float64, message string) error {
	f.evaluations = append(f.evaluations, telemetryAlertEvaluation{id: id, trigger: trigger, value: value, message: message})
	return nil
}

type fakeAlertExplorer struct {
	value telemetry.AlertValue
	query telemetry.AlertQuery
}

func (f *fakeAlertExplorer) EvaluateAlert(_ context.Context, query telemetry.AlertQuery) (telemetry.AlertValue, error) {
	f.query = query
	return f.value, nil
}

func TestTelemetryAlertEvaluatorUsesBoundedComparisonAndPersistsValue(t *testing.T) {
	ruleID := uuid.Must(uuid.NewV7())
	store := &fakeTelemetryAlertStore{rules: []domain.AdminAlertRule{{
		ID:      ruleID.String(),
		Kind:    "metric_threshold",
		Enabled: true,
		Condition: map[string]any{
			"metric":      "system.cpu.utilization",
			"aggregation": "avg",
			"operator":    "gte",
			"threshold":   0.8,
		},
	}}}
	explorer := &fakeAlertExplorer{value: telemetry.AlertValue{Value: 0.91, SampleCount: 4, Available: true}}
	evaluator, err := NewTelemetryAlertEvaluator(store, explorer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if evaluated, err := evaluator.RunOnce(context.Background()); err != nil || evaluated != 1 {
		t.Fatalf("RunOnce() = evaluated=%d err=%v", evaluated, err)
	}
	if len(store.evaluations) != 1 || !store.evaluations[0].trigger || store.evaluations[0].value == nil || *store.evaluations[0].value != 0.91 {
		t.Fatalf("unexpected evaluation = %+v", store.evaluations)
	}
	if explorer.query.Metric != "system.cpu.utilization" || explorer.query.Range.To.Sub(explorer.query.Range.From) < defaultAlertWindow || explorer.query.Range.To.Sub(explorer.query.Range.From) > defaultAlertWindow+time.Millisecond {
		t.Fatalf("unexpected bounded query = %+v", explorer.query)
	}
}

func TestTelemetryAlertEvaluatorDoesNotChangeStateWithoutSamples(t *testing.T) {
	ruleID := uuid.Must(uuid.NewV7())
	store := &fakeTelemetryAlertStore{rules: []domain.AdminAlertRule{{
		ID: ruleID.String(), Kind: "error_rate", Enabled: true,
		Condition: map[string]any{"operator": "gt", "threshold": 0.1},
	}}}
	explorer := &fakeAlertExplorer{value: telemetry.AlertValue{Available: false}}
	evaluator, err := NewTelemetryAlertEvaluator(store, explorer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.evaluations) != 0 {
		t.Fatalf("evaluations=%d, want no state change without a sample", len(store.evaluations))
	}
}

func TestCompileTelemetryAlertHonorsWindowAndLogFilters(t *testing.T) {
	rule := domain.AdminAlertRule{
		Kind: "log_match",
		Condition: map[string]any{
			"operator":       "gte",
			"threshold":      2.0,
			"search":         "database unavailable",
			"service":        "stealth-api",
			"level":          "ERROR",
			"window_seconds": 90.0,
		},
	}
	compiled, err := compileTelemetryAlert(rule)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.query.Search != "database unavailable" || compiled.query.Service != "stealth-api" || compiled.query.Level != "ERROR" || compiled.query.Range.To.Sub(compiled.query.Range.From) < 90*time.Second || compiled.query.Range.To.Sub(compiled.query.Range.From) > 90*time.Second+time.Millisecond {
		t.Fatalf("unexpected compiled alert = %+v", compiled)
	}
}
