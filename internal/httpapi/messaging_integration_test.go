package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/functionsecret"
	"github.com/nazxf/stealth-api/internal/httpapi"
	"github.com/nazxf/stealth-api/internal/messagingrunner"
	"github.com/nazxf/stealth-api/internal/migrate"
	"github.com/nazxf/stealth-api/internal/repository"
)

func TestProjectMessagingControlPlaneIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cipher, err := functionsecret.New(bytes.Repeat([]byte("m"), functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repository.NewWithDependencies(pool, repository.Dependencies{WebhookCipher: cipher}), logger))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email": fmt.Sprintf("messaging-owner-%s@example.test", ownerID), "password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "messaging-" + ownerID.String()[:8]}, http.StatusCreated, &project)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID)
	})
	projectURL := server.URL + "/v1/projects/" + project.Project.ID

	createBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/messaging/providers", map[string]any{
		"name": "Primary SMTP", "channel": "email", "provider": "smtp", "credentials": map[string]string{"host": "smtp.example.test", "password": "do-not-return"},
	}, http.StatusCreated)
	var createdProvider struct {
		Provider struct {
			ID                 string `json:"id"`
			CredentialsPresent bool   `json:"credentials_present"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(createBody, &createdProvider); err != nil {
		t.Fatal(err)
	}
	if createdProvider.Provider.ID == "" || !createdProvider.Provider.CredentialsPresent || bytes.Contains(createBody, []byte("do-not-return")) || bytes.Contains(createBody, []byte("ciphertext")) {
		t.Fatalf("provider response leaked or omitted credential metadata: %s", createBody)
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/messaging/providers", map[string]any{"name": "Primary SMTP", "channel": "email", "provider": "smtp"}, http.StatusConflict, nil)

	requestJSON(t, ownerClient, http.MethodPatch, projectURL+"/messaging/providers/"+createdProvider.Provider.ID, map[string]any{"credentials": map[string]string{}}, http.StatusOK, &createdProvider)
	if createdProvider.Provider.CredentialsPresent {
		t.Fatal("clearing credentials kept credentials_present=true")
	}

	topicBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/messaging/topics", map[string]any{"name": "Product updates", "description": "Release notifications"}, http.StatusCreated)
	var createdTopic struct {
		Topic struct {
			ID              string `json:"id"`
			SubscriberCount int64  `json:"subscriber_count"`
		} `json:"topic"`
	}
	if err := json.Unmarshal(topicBody, &createdTopic); err != nil {
		t.Fatal(err)
	}
	if createdTopic.Topic.ID == "" || createdTopic.Topic.SubscriberCount != 0 {
		t.Fatalf("unexpected topic response: %s", topicBody)
	}

	subscriberBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/messaging/topics/"+createdTopic.Topic.ID+"/subscribers", map[string]any{"channel": "email", "address": "Person@example.test"}, http.StatusCreated)
	var createdSubscriber struct {
		Subscriber struct {
			ID             string `json:"id"`
			AddressPreview string `json:"address_preview"`
		} `json:"subscriber"`
	}
	if err := json.Unmarshal(subscriberBody, &createdSubscriber); err != nil {
		t.Fatal(err)
	}
	if createdSubscriber.Subscriber.ID == "" || strings.Contains(strings.ToLower(createdSubscriber.Subscriber.AddressPreview), "person@example.test") || !strings.Contains(createdSubscriber.Subscriber.AddressPreview, "@") {
		t.Fatalf("subscriber address was not masked: %s", subscriberBody)
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/messaging/topics/"+createdTopic.Topic.ID+"/subscribers", map[string]any{"channel": "email", "address": "person@example.test"}, http.StatusConflict, nil)
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/messaging/topics/"+createdTopic.Topic.ID+"/subscribers", map[string]any{"channel": "sms", "address": "not-a-phone"}, http.StatusUnprocessableEntity, nil)

	var topicList struct {
		Topics []struct {
			SubscriberCount int64 `json:"subscriber_count"`
		} `json:"topics"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/messaging/topics", nil, http.StatusOK, &topicList)
	if len(topicList.Topics) != 1 || topicList.Topics[0].SubscriberCount != 1 {
		t.Fatalf("topic subscriber count was not durable: %+v", topicList)
	}

	var readKey struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		Secret string `json:"secret"`
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/api-keys", map[string]any{"name": "messaging-read", "scopes": []string{"messaging.read"}}, http.StatusCreated, &readKey)
	keyClient := &http.Client{}
	requestJSONWithHeaders(t, keyClient, http.MethodGet, projectURL+"/messaging/providers", nil, http.StatusOK, map[string]string{"X-Stealth-Key": readKey.Secret})
	requestJSONWithHeaders(t, keyClient, http.MethodPost, projectURL+"/messaging/topics", map[string]any{"name": "denied-topic"}, http.StatusForbidden, map[string]string{"X-Stealth-Key": readKey.Secret})

	requestJSON(t, ownerClient, http.MethodDelete, projectURL+"/messaging/topics/"+createdTopic.Topic.ID+"/subscribers/"+createdSubscriber.Subscriber.ID, nil, http.StatusNoContent, nil)
	requestJSON(t, ownerClient, http.MethodDelete, projectURL+"/messaging/topics/"+createdTopic.Topic.ID, nil, http.StatusNoContent, nil)
	requestJSON(t, ownerClient, http.MethodDelete, projectURL+"/messaging/providers/"+createdProvider.Provider.ID, nil, http.StatusNoContent, nil)
}

func TestProjectMessagingDeliveryIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Remove only this test's namespaced projects so a failed local run cannot
	// make the global worker claim an older fixture before the current message.
	if _, err := pool.Exec(ctx, `DELETE FROM projects WHERE name LIKE 'delivery-%'`); err != nil {
		t.Fatal(err)
	}
	cipher, err := functionsecret.New(bytes.Repeat([]byte("d"), functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := repository.NewWithDependencies(pool, repository.Dependencies{WebhookCipher: cipher})
	server := httptest.NewServer(httpapi.New(config.Config{SessionCookieName: "stealth_session", SessionTTL: time.Hour}, repo, logger))
	defer server.Close()

	ownerClient := newIntegrationClient(t)
	ownerID := uuid.Must(uuid.NewV7())
	var registration struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/account/registrations", map[string]string{
		"email": fmt.Sprintf("messaging-delivery-owner-%s@example.test", ownerID), "password": "correct-horse-battery-staple",
	}, http.StatusCreated, &registration)
	var project struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	requestJSON(t, ownerClient, http.MethodPost, server.URL+"/v1/organizations/"+registration.Organization.ID+"/projects", map[string]string{"name": "delivery-" + ownerID.String()[:8]}, http.StatusCreated, &project)
	t.Cleanup(func() {
		if _, cleanupErr := pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, project.Project.ID); cleanupErr != nil {
			t.Logf("cleanup project: %v", cleanupErr)
		}
		if _, cleanupErr := pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, registration.Organization.ID); cleanupErr != nil {
			t.Logf("cleanup organization: %v", cleanupErr)
		}
		if _, cleanupErr := pool.Exec(context.Background(), `DELETE FROM accounts WHERE id=$1`, registration.Account.ID); cleanupErr != nil {
			t.Logf("cleanup account: %v", cleanupErr)
		}
		pool.Close()
	})
	projectURL := server.URL + "/v1/projects/" + project.Project.ID

	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/messaging/providers", map[string]any{
		"name": "Local delivery log", "channel": "email", "provider": "log", "credentials": map[string]string{},
	}, http.StatusCreated, nil)
	topicBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/messaging/topics", map[string]any{"name": "Delivery test topic"}, http.StatusCreated)
	var topic struct {
		Topic struct {
			ID string `json:"id"`
		} `json:"topic"`
	}
	if err := json.Unmarshal(topicBody, &topic); err != nil {
		t.Fatal(err)
	}
	if topic.Topic.ID == "" {
		t.Fatal("topic id was empty")
	}
	subscriberBody := requestJSONRaw(t, ownerClient, http.MethodPost, projectURL+"/messaging/topics/"+topic.Topic.ID+"/subscribers", map[string]any{
		"channel": "email", "address": "delivery-recipient@example.test",
	}, http.StatusCreated)
	var subscriber struct {
		Subscriber struct {
			ID string `json:"id"`
		} `json:"subscriber"`
	}
	if err := json.Unmarshal(subscriberBody, &subscriber); err != nil {
		t.Fatal(err)
	}

	messageInput := map[string]any{"topic_id": topic.Topic.ID, "channel": "email", "subject": "Delivery test", "body": "hello from the queue", "data": map[string]string{"kind": "integration"}}
	idempotencyKey := "delivery-" + ownerID.String()
	firstBody := requestJSONRawWithHeaders(t, ownerClient, http.MethodPost, projectURL+"/messaging/messages", messageInput, http.StatusAccepted, map[string]string{"Idempotency-Key": idempotencyKey})
	var first struct {
		Message struct {
			ID             string  `json:"id"`
			TopicID        *string `json:"topic_id"`
			Status         string  `json:"status"`
			RecipientCount int64   `json:"recipient_count"`
			SucceededCount int64   `json:"succeeded_count"`
		} `json:"message"`
	}
	if err := json.Unmarshal(firstBody, &first); err != nil {
		t.Fatal(err)
	}
	if first.Message.ID == "" || first.Message.TopicID == nil || *first.Message.TopicID != topic.Topic.ID || first.Message.Status != "queued" || first.Message.RecipientCount != 1 || first.Message.SucceededCount != 0 {
		t.Fatalf("unexpected accepted message: %s", firstBody)
	}
	if bytes.Contains(firstBody, []byte("hello from the queue")) || bytes.Contains(firstBody, []byte("delivery-recipient@example.test")) {
		t.Fatalf("message response leaked encrypted content: %s", firstBody)
	}

	requestJSONWithHeaders(t, ownerClient, http.MethodPost, projectURL+"/messaging/messages", messageInput, http.StatusOK, map[string]string{"Idempotency-Key": idempotencyKey})
	requestJSONWithHeaders(t, ownerClient, http.MethodPost, projectURL+"/messaging/messages", map[string]any{"topic_id": topic.Topic.ID, "channel": "email", "subject": "different", "body": "hello from the queue"}, http.StatusConflict, map[string]string{"Idempotency-Key": idempotencyKey})

	var deliveries struct {
		Deliveries []struct {
			ID             string `json:"id"`
			Status         string `json:"status"`
			AttemptCount   int    `json:"attempt_count"`
			AddressPreview string `json:"address_preview"`
		} `json:"deliveries"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/messaging/messages/"+first.Message.ID+"/deliveries", nil, http.StatusOK, &deliveries)
	if len(deliveries.Deliveries) != 1 || deliveries.Deliveries[0].Status != "pending" || deliveries.Deliveries[0].AttemptCount != 0 || deliveries.Deliveries[0].ID == "" || strings.Contains(deliveries.Deliveries[0].AddressPreview, "delivery-recipient@example.test") {
		t.Fatalf("unexpected pending delivery projection: %+v", deliveries)
	}

	worker, err := messagingrunner.New(repo, cipher, "messaging-delivery-test", logger)
	if err != nil {
		t.Fatal(err)
	}
	registry := messagingrunner.NewRegistry()
	var delivered messagingrunner.Message
	if err := registry.Register("email", "log", messagingrunner.AdapterFunc(func(_ context.Context, _ messagingrunner.Provider, message messagingrunner.Message) error {
		delivered = message
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	worker.Adapters = registry
	worker.DeliveryTimeout = time.Second
	processed, err := worker.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("worker RunOnce() processed=%t err=%v", processed, err)
	}
	if delivered.Recipient != "delivery-recipient@example.test" || delivered.Subject != "Delivery test" || delivered.Body != "hello from the queue" || delivered.Data["kind"] != "integration" {
		t.Fatalf("worker did not decrypt delivery payload correctly: %+v", delivered)
	}

	var messageAfter struct {
		Message struct {
			Status         string `json:"status"`
			SucceededCount int64  `json:"succeeded_count"`
			FailedCount    int64  `json:"failed_count"`
		} `json:"message"`
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/messaging/messages/"+first.Message.ID, nil, http.StatusOK, &messageAfter)
	if messageAfter.Message.Status != "succeeded" || messageAfter.Message.SucceededCount != 1 || messageAfter.Message.FailedCount != 0 {
		t.Fatalf("message status was not refreshed after worker delivery: %+v", messageAfter.Message)
	}
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/messaging/messages/"+first.Message.ID+"/deliveries", nil, http.StatusOK, &deliveries)
	if len(deliveries.Deliveries) != 1 || deliveries.Deliveries[0].Status != "succeeded" || deliveries.Deliveries[0].AttemptCount != 1 {
		t.Fatalf("delivery status was not refreshed after worker delivery: %+v", deliveries)
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/messaging/messages/"+first.Message.ID+"/cancel", map[string]any{}, http.StatusConflict, nil)

	secondBody := requestJSONRawWithHeaders(t, ownerClient, http.MethodPost, projectURL+"/messaging/messages", messageInput, http.StatusAccepted, map[string]string{"Idempotency-Key": idempotencyKey + "-cancel"})
	var second struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	if err := json.Unmarshal(secondBody, &second); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/messaging/messages/"+second.Message.ID+"/cancel", map[string]any{}, http.StatusOK, nil)
	requestJSON(t, ownerClient, http.MethodPost, projectURL+"/messaging/messages/"+second.Message.ID+"/cancel", map[string]any{}, http.StatusOK, nil)
	requestJSON(t, ownerClient, http.MethodGet, projectURL+"/messaging/messages/"+second.Message.ID+"/deliveries", nil, http.StatusOK, &deliveries)
	if len(deliveries.Deliveries) != 1 || deliveries.Deliveries[0].Status != "cancelled" {
		t.Fatalf("cancel did not propagate to pending delivery: %+v", deliveries)
	}
}
