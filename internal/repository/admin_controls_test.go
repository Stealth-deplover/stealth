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
	raw := json.RawMessage(`{"operator":"gte","threshold":0.95,"metric":"http.server.request.duration","service":"api","aggregation":"avg"}`)
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

func TestAdminAlertRuleAcceptsBoundedTelemetryKinds(t *testing.T) {
	for kind, condition := range map[string]string{
		"error_rate":       `{"operator":"gt","threshold":0.05,"service":"stealth-api"}`,
		"latency":          `{"operator":"gte","threshold":500,"percentile":"p95"}`,
		"log_match":        `{"operator":"gte","threshold":1,"search":"panic","level":"ERROR"}`,
		"service_health":   `{"operator":"gt","threshold":0.1,"service":"stealth-api"}`,
		"disk_pressure":    `{"operator":"gte","threshold":0.9}`,
		"metric_threshold": `{"operator":"gte","threshold":0.95,"metric":"system.memory.utilization"}`,
	} {
		t.Run(kind, func(t *testing.T) {
			if _, err := normalizeAdminAlertRuleInput(AdminAlertRuleInput{
				Name: "Telemetry rule", Kind: kind, Condition: json.RawMessage(condition),
				Severity: "warning", Enabled: true,
			}); err != nil {
				t.Fatalf("normalizeAdminAlertRuleInput() error = %v", err)
			}
		})
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
