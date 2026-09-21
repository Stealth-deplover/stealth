package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/repository"
)

func TestAdminMonitorErrorMapsAlertRuleConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	adminMonitorError(nil, recorder, repository.ErrAdminMonitorHasRules)
	if recorder.Code != 409 {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"monitor_has_alert_rules"`) {
		t.Fatalf("body = %q, want monitor_has_alert_rules code", recorder.Body.String())
	}
}
