package cli

import (
	"testing"
)

func TestParseComposeStatusesSupportsJSONLines(t *testing.T) {
	contents := []byte(`{"Service":"api","State":"running","Health":"healthy"}
{"Service":"worker","State":"running","Health":"healthy"}
{"Service":"postgres","State":"running","Health":"healthy"}`)
	statuses, err := parseComposeStatuses(contents)
	if err != nil {
		t.Fatal(err)
	}
	if !statuses["api"].Healthy() || !statuses["worker"].Healthy() || !statuses["postgres"].Healthy() {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}
}

func TestServiceStatusDisplayAndMissingState(t *testing.T) {
	if got := (ServiceStatus{Service: "api", Health: "healthy"}).Display(); got != "healthy" {
		t.Fatalf("Display() = %q", got)
	}
	if (ServiceStatus{Service: "api", State: "exited"}).Healthy() {
		t.Fatal("exited API reported healthy")
	}
	if anyServiceUnhealthy(map[string]ServiceStatus{}) == false {
		t.Fatal("missing services reported healthy")
	}
}
