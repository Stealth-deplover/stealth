package repository

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
)

func TestDecodeRealtimeEventAndApplicationVisibility(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	projectID := uuid.Must(uuid.NewV7())
	tableID := uuid.Must(uuid.NewV7())
	rowID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	payload := map[string]any{
		"id": id.String(), "event": "database_row.create", "project_id": projectID.String(),
		"target": map[string]any{"type": "database_row", "id": rowID.String()},
		"data": map[string]any{"changed_fields": []string{"title"}, "realtime": map[string]any{
			"database_id": tableID.String(), "table_id": tableID.String(), "row_security": true,
			"table_read_permissions": []string{}, "row_read_permissions": []string{"user:" + userID.String()},
		}},
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	event, err := decodeRealtimeEvent(id, projectID, "database_row.create", "database_row", &rowID, raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !realtimeApplicationEventVisible(event, DatabaseActor{Kind: DatabaseApplicationActor, ProjectUserID: userID}) {
		t.Fatal("user-specific row grant was not honored")
	}
	otherUser := uuid.Must(uuid.NewV7())
	if realtimeApplicationEventVisible(event, DatabaseActor{Kind: DatabaseApplicationActor, ProjectUserID: otherUser}) {
		t.Fatal("row event leaked to an unrelated user")
	}
}

func TestRealtimeApplicationVisibilityRejectsNonRowEvents(t *testing.T) {
	event := domain.RealtimeEvent{EventName: "project.create", Data: map[string]any{}}
	if realtimeApplicationEventVisible(event, DatabaseActor{Kind: DatabaseApplicationActor, ProjectUserID: uuid.Must(uuid.NewV7())}) {
		t.Fatal("non-row event was visible to an application actor")
	}
}
