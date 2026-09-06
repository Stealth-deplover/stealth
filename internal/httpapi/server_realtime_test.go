package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRealtimeCursorPrefersExplicitQuery(t *testing.T) {
	queryID := uuid.Must(uuid.NewV7())
	headerID := uuid.Must(uuid.NewV7())
	request := httptest.NewRequest("GET", "/?cursor="+queryID.String(), nil)
	request.Header.Set("Last-Event-ID", headerID.String())
	got, err := realtimeCursor(request)
	if err != nil || got == nil || *got != queryID {
		t.Fatalf("cursor = %v, err = %v", got, err)
	}
	request = httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Last-Event-ID", headerID.String())
	got, err = realtimeCursor(request)
	if err != nil || got == nil || *got != headerID {
		t.Fatalf("Last-Event-ID cursor = %v, err = %v", got, err)
	}
}

func TestRealtimeEventFilter(t *testing.T) {
	filter, err := realtimeEventFilter("database_row.update, database_row.create")
	if err != nil || filter.all || !filter.matches("database_row.create") || filter.matches("project.create") {
		t.Fatalf("unexpected realtime filter: %#v, err=%v", filter, err)
	}
	all, err := realtimeEventFilter("*")
	if err != nil || !all.all || !all.matches("anything") {
		t.Fatalf("wildcard realtime filter: %#v, err=%v", all, err)
	}
	if _, err := realtimeEventFilter("database row"); err == nil {
		t.Fatal("invalid realtime event name was accepted")
	}
}
