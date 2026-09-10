package repository

import (
	"context"
	"encoding/json"
	"errors"
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

func TestDecodeRealtimeEventVersionedEnvelope(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	organizationID := uuid.Must(uuid.NewV7())
	projectID := uuid.Must(uuid.NewV7())
	targetID := uuid.Must(uuid.NewV7())
	raw := []byte(`{"id":"` + id.String() + `","event":"agent.run.running","type":"agent.run.running","version":1,"organization_id":"` + organizationID.String() + `","project_id":"` + projectID.String() + `","resource_id":"` + targetID.String() + `","payload":{"agent_id":"agent-1","status":"running"},"target":{"type":"agent_run","id":"` + targetID.String() + `"}}`)
	event, err := decodeRealtimeEventWithMetadata(id, projectID, organizationID, "agent.run.running", "agent_run", &targetID, 1, nil, raw, time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if event.OrganizationID != organizationID.String() || event.Version != 1 || event.ResourceID == nil || event.Data["status"] != "running" {
		t.Fatalf("decoded versioned event = %#v", event)
	}
}

func TestRealtimePruneBatchRejectsUnboundedSizes(t *testing.T) {
	for _, batchSize := range []int{0, -1, maxRealtimePruneBatch + 1} {
		_, err := (&Repository{}).PruneExpiredWebhookEventsBatch(context.Background(), batchSize)
		if !errors.Is(err, ErrInvalidRealtime) {
			t.Fatalf("prune batch size %d error = %v, want ErrInvalidRealtime", batchSize, err)
		}
	}
}
