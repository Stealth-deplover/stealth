package realtime

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnvelopeRoundTripAndSafePayload(t *testing.T) {
	organizationID := uuid.Must(uuid.NewV7())
	projectID := uuid.Must(uuid.NewV7())
	eventID := uuid.Must(uuid.NewV7())
	payload := SafePayload(map[string]any{
		"status":         "running",
		"api_key_secret": "must-not-leak",
		"nested":         map[string]any{"webhook_secret": "must-not-leak"},
	})
	event := Envelope{
		ID:             eventID.String(),
		Type:           "agent.run.running",
		Version:        CurrentVersion,
		OccurredAt:     time.Now().UTC(),
		OrganizationID: organizationID.String(),
		ProjectID:      projectID.String(),
		ResourceID:     uuid.Must(uuid.NewV7()).String(),
		Payload:        payload,
	}
	raw, err := Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "must-not-leak") || strings.Contains(string(raw), "api_key") {
		t.Fatalf("sensitive event metadata leaked: %s", raw)
	}
	decoded, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != event.Type || decoded.ProjectID != projectID.String() || decoded.Payload["status"] != "running" {
		t.Fatalf("decoded event = %#v", decoded)
	}
}

func TestChannelRejectsNonUUIDProject(t *testing.T) {
	if _, err := Channel("not-a-project"); err == nil {
		t.Fatal("invalid project channel was accepted")
	}
}

func TestShouldFanoutOnlyIncludesRealtimeConsumerEvents(t *testing.T) {
	for _, eventType := range []string{
		"agent.run.running",
		"agent.run.queued",
		"function_deployment.updated",
		"webhook.delivery.updated",
		"database_row.update",
		"storage_file.create",
		"database_table.update",
		"database_column.create",
		"database_index.delete",
		"database_relationship.create",
		"database_backup.restore",
		"function_variable.update",
		"site_domain.verify",
		"project_api_key.revoke",
		"project_user.status_change",
		"messaging.subscriber.create",
		"messaging.subscriber.delete",
		"messaging.message.create",
		"messaging.message.cancel",
		"webhook.secret_rotate",
	} {
		if !ShouldFanout(eventType) {
			t.Fatalf("ShouldFanout(%q) = false, want true", eventType)
		}
	}
	for _, eventType := range []string{
		"organization.membership.add",
		"billing.invoice.updated",
		"audit.recorded",
	} {
		if ShouldFanout(eventType) {
			t.Fatalf("ShouldFanout(%q) = true, want false", eventType)
		}
	}
}
