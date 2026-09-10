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
		"function_deployment.updated",
		"webhook.delivery.updated",
		"database_row.update",
	} {
		if !ShouldFanout(eventType) {
			t.Fatalf("ShouldFanout(%q) = false, want true", eventType)
		}
	}
	for _, eventType := range []string{
		"organization.membership.add",
		"project_api_key.create",
		"project_user.password_reset",
		"function_variable.create",
		"site_domain.create",
		"database_table.create",
	} {
		if ShouldFanout(eventType) {
			t.Fatalf("ShouldFanout(%q) = true, want false", eventType)
		}
	}
}
