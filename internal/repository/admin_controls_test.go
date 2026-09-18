package repository

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestAdminAlertConditionRejectsArbitrarySQL(t *testing.T) {
	raw := json.RawMessage(`{"operator":"gt","threshold":1,"sql":"SELECT password FROM users"}`)
	if err := validateAdminAlertCondition("metric_threshold", raw); !errors.Is(err, ErrInvalidAdminAlert) {
		t.Fatalf("validateAdminAlertCondition() error = %v, want invalid alert", err)
	}
}

func TestAdminAlertConditionAcceptsBoundedMetricRule(t *testing.T) {
	raw := json.RawMessage(`{"operator":"gte","threshold":0.95,"name":"http.server.request.duration","service":"api"}`)
	if err := validateAdminAlertCondition("metric_threshold", raw); err != nil {
		t.Fatalf("validateAdminAlertCondition() error = %v", err)
	}
}

func TestAdminStatusComponentCannotPublishSecretFields(t *testing.T) {
	if err := validatePublicStatusComponent(map[string]any{
		"name":   "API",
		"status": "operational",
		"token":  "must-not-be-published",
	}); !errors.Is(err, ErrInvalidAdminStatus) {
		t.Fatalf("validatePublicStatusComponent() error = %v, want invalid status", err)
	}
}

func TestAdminStatusComponentRequiresSafeStatusAndURL(t *testing.T) {
	if err := validatePublicStatusComponent(map[string]any{"name": "API"}); !errors.Is(err, ErrInvalidAdminStatus) {
		t.Fatalf("missing status error = %v, want invalid status", err)
	}
	if err := validatePublicStatusComponent(map[string]any{
		"name": "API", "status": "operational", "url": "javascript:alert(1)",
	}); !errors.Is(err, ErrInvalidAdminStatus) {
		t.Fatalf("unsafe URL error = %v, want invalid status", err)
	}
}

func TestAdminAlertRuleRejectsUnevaluatedKinds(t *testing.T) {
	condition := json.RawMessage(`{"operator":"gt","threshold":1}`)
	if _, err := normalizeAdminAlertRuleInput(AdminAlertRuleInput{
		Name: "HTTP errors", Kind: "error_rate", Condition: condition,
		Severity: "warning", Enabled: true,
	}); !errors.Is(err, ErrInvalidAdminAlert) {
		t.Fatalf("unevaluated alert error = %v, want invalid alert", err)
	}
}

func TestAdminDashboardDefinitionRejectsRawSQL(t *testing.T) {
	definition := map[string]any{
		"panels": []any{
			map[string]any{"type": "table", "query": map[string]any{"sql": "SELECT * FROM otel_logs"}},
		},
	}
	if err := validateAdminDashboardDefinition(definition); !errors.Is(err, ErrInvalidAdminDashboard) {
		t.Fatalf("validateAdminDashboardDefinition() error = %v, want invalid dashboard", err)
	}
}
