// Package realtime owns the small internal event contract shared by the
// transactional outbox, publisher, SSE transport, and Console adapter.
// Events are notifications; canonical state remains in the Go API/database.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	CurrentVersion       = 1
	MaxPayloadBytes      = 262144
	MaxEventTypeLength   = 160
	MaxCorrelationLength = 128
	projectChannelPrefix = "stealth:realtime:project:"
)

var (
	ErrInvalidEnvelope = errors.New("invalid realtime event envelope")
	ErrInvalidChannel  = errors.New("invalid realtime channel")
)

// Envelope is deliberately notification-shaped. Payload is metadata useful
// for choosing which canonical query to refetch, not a resource snapshot.
type Envelope struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Version        int            `json:"version"`
	OccurredAt     time.Time      `json:"occurred_at"`
	OrganizationID string         `json:"organization_id"`
	ProjectID      string         `json:"project_id"`
	ResourceID     string         `json:"resource_id,omitempty"`
	CorrelationID  string         `json:"correlation_id,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
}

func (e Envelope) Validate() error {
	if _, err := uuid.Parse(e.ID); err != nil {
		return fmt.Errorf("%w: id", ErrInvalidEnvelope)
	}
	if !validEventType(e.Type) || e.Version < 1 || e.Version > 100 {
		return fmt.Errorf("%w: type or version", ErrInvalidEnvelope)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at", ErrInvalidEnvelope)
	}
	if _, err := uuid.Parse(e.OrganizationID); err != nil {
		return fmt.Errorf("%w: organization_id", ErrInvalidEnvelope)
	}
	if _, err := uuid.Parse(e.ProjectID); err != nil {
		return fmt.Errorf("%w: project_id", ErrInvalidEnvelope)
	}
	if e.ResourceID != "" {
		if _, err := uuid.Parse(e.ResourceID); err != nil {
			return fmt.Errorf("%w: resource_id", ErrInvalidEnvelope)
		}
	}
	if len(e.CorrelationID) > MaxCorrelationLength || strings.ContainsAny(e.CorrelationID, "\r\n") {
		return fmt.Errorf("%w: correlation_id", ErrInvalidEnvelope)
	}
	encoded, err := json.Marshal(e)
	if err != nil || len(encoded) > MaxPayloadBytes {
		return fmt.Errorf("%w: payload", ErrInvalidEnvelope)
	}
	return nil
}

func validEventType(value string) bool {
	if len(value) < 3 || len(value) > MaxEventTypeLength {
		return false
	}
	for index, character := range value {
		alphaNumeric := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		if index == 0 && !alphaNumeric {
			return false
		}
		if index > 0 && !(alphaNumeric || character == '.' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

// ShouldFanout identifies the small notification set that has an active
// realtime consumer. Other webhook/audit rows remain durable and replayable
// through PostgreSQL, but do not create unnecessary Redis traffic.
func ShouldFanout(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if strings.HasPrefix(eventType, "agent.run.") || strings.HasPrefix(eventType, "function_execution.") || strings.HasPrefix(eventType, "function_deployment.") || strings.HasPrefix(eventType, "site_deployment.") || strings.HasPrefix(eventType, "database_row.") {
		return true
	}
	switch eventType {
	case "agent.create", "agent.update", "agent.delete",
		"webhook.create", "webhook.update", "webhook.delete",
		"webhook.delivery.updated", "messaging.delivery.updated",
		"messaging.provider.create", "messaging.provider.update", "messaging.provider.delete",
		"messaging.topic.create", "messaging.topic.update", "messaging.topic.delete",
		"database.create", "database.delete", "project.update",
		"function.create", "function.update", "function.delete",
		"site.create", "site.update", "site.delete",
		"storage_bucket.create", "storage_bucket.update", "storage_bucket.delete":
		return true
	default:
		return false
	}
}

func Marshal(e Envelope) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

func Unmarshal(payload []byte) (Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

// SafePayload copies event metadata while dropping fields whose names are
// commonly used for credentials. Event metadata is intentionally small, but
// this defense-in-depth boundary prevents a future mutation from publishing a
// secret through either Redis or SSE.
func SafePayload(value map[string]any) map[string]any {
	return safeMap(value)
}

func safeMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		if sensitiveKey(key) {
			continue
		}
		result[key] = safeValue(item)
	}
	return result
}

func safeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return safeMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = safeValue(item)
		}
		return result
	default:
		return value
	}
}

func sensitiveKey(value string) bool {
	value = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(value, "-", "_"), " ", "_"))
	if value == "api_key" || strings.Contains(value, "api_key_secret") {
		return true
	}
	for _, fragment := range []string{"password", "secret", "token", "authorization", "cookie", "access_key", "access_token", "refresh_token", "private_key", "ciphertext"} {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func Channel(projectID string) (string, error) {
	if _, err := uuid.Parse(projectID); err != nil {
		return "", ErrInvalidChannel
	}
	return projectChannelPrefix + projectID, nil
}

// Broker is intentionally a thin Redis Pub/Sub adapter. Redis is an
// ephemeral fanout transport; durability belongs to PostgreSQL outbox rows.
type Broker struct{ client redis.UniversalClient }

func NewBroker(client redis.UniversalClient) *Broker {
	if client == nil {
		return nil
	}
	return &Broker{client: client}
}

func (b *Broker) Publish(ctx context.Context, projectID string, payload []byte) error {
	if b == nil || b.client == nil {
		return errors.New("realtime broker is unavailable")
	}
	channel, err := Channel(projectID)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, channel, payload).Err()
}

func (b *Broker) Subscribe(ctx context.Context, projectID string) (*redis.PubSub, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("realtime broker is unavailable")
	}
	channel, err := Channel(projectID)
	if err != nil {
		return nil, err
	}
	pubsub := b.client.Subscribe(ctx, channel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, err
	}
	return pubsub, nil
}
