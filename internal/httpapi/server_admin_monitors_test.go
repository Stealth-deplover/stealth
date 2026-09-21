package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/repository"
)

func TestAdminMonitorErrorMapsAlertRuleConflict(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "delete", err: repository.ErrAdminMonitorHasRules, code: "monitor_has_alert_rules"},
		{name: "kind update", err: repository.ErrAdminMonitorRuleConflict, code: "monitor_alert_rule_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			adminMonitorError(nil, recorder, test.err)
			if recorder.Code != 409 {
				t.Fatalf("status = %d, want 409", recorder.Code)
			}
			if !strings.Contains(recorder.Body.String(), `"`+test.code+`"`) {
				t.Fatalf("body = %q, want %s code", recorder.Body.String(), test.code)
			}
		})
	}
}
