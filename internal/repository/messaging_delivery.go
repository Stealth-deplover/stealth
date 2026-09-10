package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrMessagingNoRecipients        = errors.New("messaging topic has no active recipients")
	ErrMessagingProviderUnavailable = errors.New("messaging provider is unavailable")
	ErrMessagingTooManyRecipients   = errors.New("messaging topic has too many recipients")
	ErrMessagingMessageTerminal     = errors.New("messaging message is already terminal")
	ErrNoMessagingDelivery          = errors.New("no messaging delivery available")
	ErrMessagingDeliveryNotFound    = errors.New("messaging delivery was not found")
)

const (
	maxMessagingMessageSubject     = 998
	maxMessagingMessageBody        = 64 << 10
	maxMessagingMessageDataKeys    = 32
	maxMessagingMessageDataValue   = 2048
	maxMessagingMessageDataBytes   = 16 << 10
	maxMessagingMessageRecipients  = 10000
	maxMessagingMessageIdempotency = 128
)

type MessagingMessageInput struct {
	TopicID        uuid.UUID
	Channel        string
	Subject        string
	Body           string
	Data           map[string]string
	IdempotencyKey string
}

// MessagingMessagePayload is encrypted as one unit. Keeping the complete
// content out of domain.MessagingMessage prevents a handler from accidentally
// returning message text or push data to a management API caller.
type MessagingMessagePayload struct {
	Subject string            `json:"subject,omitempty"`
	Body    string            `json:"body"`
	Data    map[string]string `json:"data,omitempty"`
}

type MessagingMessageCreateResult struct {
	Message domain.MessagingMessage
	Created bool
}

// MessagingDeliveryJob contains only the ciphertext needed by a trusted
// worker. The worker owns decryption and provider network calls; this type is
// never returned by an HTTP handler.
type MessagingDeliveryJob struct {
	DeliveryID                    uuid.UUID
	MessageID                     uuid.UUID
	ProjectID                     uuid.UUID
	SubscriberID                  *uuid.UUID
	ProviderID                    *uuid.UUID
	Channel                       string
	AddressPreview                string
	AddressCiphertext             []byte
	PayloadCiphertext             []byte
	Provider                      *string
	ProviderEnabled               *bool
	ProviderCredentialsCiphertext []byte
	AttemptCount                  int
}

const messagingMessageProjection = `id,project_id,topic_id,channel,status,recipient_count,succeeded_count,failed_count,cancelled_at,created_at,updated_at`
const messagingDeliveryProjection = `id,project_id,message_id,subscriber_id,provider_id,channel,address_preview,status,attempt_count,last_status_code,last_error,delivered_at,created_at,updated_at`

type messagingMessageScanner interface {
	Scan(dest ...any) error
}

type messagingDeliveryScanner interface {
	Scan(dest ...any) error
}

func scanMessagingMessage(row messagingMessageScanner) (domain.MessagingMessage, error) {
	var item domain.MessagingMessage
	var id, projectID uuid.UUID
	var topicID *uuid.UUID
	err := row.Scan(&id, &projectID, &topicID, &item.Channel, &item.Status, &item.RecipientCount, &item.SucceededCount, &item.FailedCount, &item.CancelledAt, &item.CreatedAt, &item.UpdatedAt)
	item.ID = id.String()
	item.ProjectID = projectID.String()
	if topicID != nil {
		value := topicID.String()
		item.TopicID = &value
	}
	return item, err
}

func scanMessagingDelivery(row messagingDeliveryScanner) (domain.MessagingDelivery, error) {
	var item domain.MessagingDelivery
	var id, projectID, messageID uuid.UUID
	var subscriberID, providerID *uuid.UUID
	err := row.Scan(&id, &projectID, &messageID, &subscriberID, &providerID, &item.Channel, &item.AddressPreview, &item.Status, &item.AttemptCount, &item.LastStatusCode, &item.LastError, &item.DeliveredAt, &item.CreatedAt, &item.UpdatedAt)
	item.ID = id.String()
	item.ProjectID = projectID.String()
	item.MessageID = messageID.String()
	if subscriberID != nil {
		value := subscriberID.String()
		item.SubscriberID = &value
	}
	if providerID != nil {
		value := providerID.String()
		item.ProviderID = &value
	}
	return item, err
}

func normalizeMessagingMessageInput(input MessagingMessageInput) (string, MessagingMessagePayload, []byte, error) {
	channel, err := normalizeMessagingChannel(input.Channel)
	if err != nil {
		return "", MessagingMessagePayload{}, nil, err
	}
	subject := strings.TrimSpace(input.Subject)
	if len(subject) > maxMessagingMessageSubject || strings.ContainsAny(subject, "\x00\r\n") {
		return "", MessagingMessagePayload{}, nil, fmt.Errorf("%w: subject is invalid", ErrInvalidMessaging)
	}
	body := input.Body
	if len(body) == 0 || len(body) > maxMessagingMessageBody || strings.ContainsRune(body, '\x00') || strings.TrimSpace(body) == "" {
		return "", MessagingMessagePayload{}, nil, fmt.Errorf("%w: body is invalid", ErrInvalidMessaging)
	}
	if channel == "email" && subject == "" {
		return "", MessagingMessagePayload{}, nil, fmt.Errorf("%w: email messages require a subject", ErrInvalidMessaging)
	}
	data, err := normalizeMessagingMessageData(input.Data)
	if err != nil {
		return "", MessagingMessagePayload{}, nil, err
	}
	payload := MessagingMessagePayload{Subject: subject, Body: body, Data: data}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > maxMessagingMessageBody+maxMessagingMessageDataBytes {
		return "", MessagingMessagePayload{}, nil, fmt.Errorf("%w: message payload is too large", ErrInvalidMessaging)
	}
	hashInput, err := json.Marshal(struct {
		TopicID string          `json:"topic_id"`
		Channel string          `json:"channel"`
		Payload json.RawMessage `json:"payload"`
	}{TopicID: input.TopicID.String(), Channel: channel, Payload: encoded})
	if err != nil {
		return "", MessagingMessagePayload{}, nil, fmt.Errorf("%w: message request could not be hashed", ErrInvalidMessaging)
	}
	hash := sha256.Sum256(hashInput)
	return channel, payload, hash[:], nil
}

func normalizeMessagingMessageData(raw map[string]string) (map[string]string, error) {
	if len(raw) > maxMessagingMessageDataKeys {
		return nil, fmt.Errorf("%w: at most %d data fields are allowed", ErrInvalidMessaging, maxMessagingMessageDataKeys)
	}
	data := make(map[string]string, len(raw))
	for key, value := range raw {
		if !messagingCredentialKeyPattern.MatchString(key) || len(value) > maxMessagingMessageDataValue || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("%w: message data fields are invalid", ErrInvalidMessaging)
		}
		data[key] = value
	}
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > maxMessagingMessageDataBytes {
		return nil, fmt.Errorf("%w: message data is too large", ErrInvalidMessaging)
	}
	return data, nil
}

func normalizeMessagingIdempotencyKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > maxMessagingMessageIdempotency || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", fmt.Errorf("%w: idempotency key is invalid", ErrInvalidMessaging)
	}
	return value, nil
}

func (r *Repository) CreateMessagingMessage(ctx context.Context, id, projectID uuid.UUID, actor MessagingActor, input MessagingMessageInput) (MessagingMessageCreateResult, error) {
	channel, payload, requestHash, err := normalizeMessagingMessageInput(input)
	if err != nil {
		return MessagingMessageCreateResult{}, err
	}
	idempotencyKey, err := normalizeMessagingIdempotencyKey(input.IdempotencyKey)
	if err != nil {
		return MessagingMessageCreateResult{}, err
	}
	if r.messagingCipher == nil {
		return MessagingMessageCreateResult{}, ErrMessagingNotReady
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return MessagingMessageCreateResult{}, fmt.Errorf("%w: message payload could not be encoded", ErrInvalidMessaging)
	}
	payloadCiphertext, err := r.messagingCipher.Encrypt(payloadBytes)
	if err != nil {
		return MessagingMessageCreateResult{}, fmt.Errorf("%w: message payload could not be encrypted", ErrMessagingNotReady)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return MessagingMessageCreateResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return MessagingMessageCreateResult{}, err
	}
	if idempotencyKey != "" {
		// Serialize requests sharing a tenant/key pair. Without a transaction
		// advisory lock, concurrent first attempts could both miss the lookup
		// and turn a valid idempotent replay into a unique-constraint conflict.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, messagingIdempotencyLockKey(projectID, idempotencyKey)); err != nil {
			return MessagingMessageCreateResult{}, err
		}
		var existingHash []byte
		var existing domain.MessagingMessage
		row := tx.QueryRow(ctx, `SELECT `+messagingMessageProjection+`,request_hash FROM project_messaging_messages WHERE project_id=$1 AND idempotency_key=$2`, projectID, idempotencyKey)
		var scanErr error
		existing, scanErr = scanMessagingMessageWithHash(row, &existingHash)
		if scanErr == nil {
			if !bytes.Equal(existingHash, requestHash) {
				return MessagingMessageCreateResult{}, ErrConflict
			}
			if err := tx.Commit(ctx); err != nil {
				return MessagingMessageCreateResult{}, err
			}
			return MessagingMessageCreateResult{Message: existing, Created: false}, nil
		}
		if !errors.Is(scanErr, pgx.ErrNoRows) {
			return MessagingMessageCreateResult{}, scanErr
		}
	}
	var topicEnabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM project_messaging_topics WHERE project_id=$1 AND id=$2 FOR SHARE`, projectID, input.TopicID).Scan(&topicEnabled); errors.Is(err, pgx.ErrNoRows) {
		return MessagingMessageCreateResult{}, ErrNotFound
	} else if err != nil {
		return MessagingMessageCreateResult{}, err
	}
	if !topicEnabled {
		return MessagingMessageCreateResult{}, fmt.Errorf("%w: topic is disabled", ErrInvalidMessaging)
	}
	var providerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM project_messaging_providers WHERE project_id=$1 AND channel=$2 AND enabled ORDER BY id LIMIT 1`, projectID, channel).Scan(&providerID); errors.Is(err, pgx.ErrNoRows) {
		return MessagingMessageCreateResult{}, ErrMessagingProviderUnavailable
	} else if err != nil {
		return MessagingMessageCreateResult{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,address_ciphertext,address_preview FROM project_messaging_subscribers WHERE project_id=$1 AND topic_id=$2 AND channel=$3 AND enabled ORDER BY id LIMIT $4`, projectID, input.TopicID, channel, maxMessagingMessageRecipients+1)
	if err != nil {
		return MessagingMessageCreateResult{}, err
	}
	type recipient struct {
		id      uuid.UUID
		address []byte
		preview string
	}
	recipients := make([]recipient, 0)
	for rows.Next() {
		var item recipient
		if err := rows.Scan(&item.id, &item.address, &item.preview); err != nil {
			rows.Close()
			return MessagingMessageCreateResult{}, err
		}
		recipients = append(recipients, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MessagingMessageCreateResult{}, err
	}
	rows.Close()
	if len(recipients) == 0 {
		return MessagingMessageCreateResult{}, ErrMessagingNoRecipients
	}
	if len(recipients) > maxMessagingMessageRecipients {
		return MessagingMessageCreateResult{}, ErrMessagingTooManyRecipients
	}
	item, err := scanMessagingMessage(tx.QueryRow(ctx, `
		INSERT INTO project_messaging_messages (id,project_id,topic_id,channel,payload_ciphertext,request_hash,idempotency_key,status,recipient_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'queued',$8)
		RETURNING `+messagingMessageProjection, id, projectID, input.TopicID, channel, payloadCiphertext, requestHash, nullableMessagingIdempotencyKey(idempotencyKey), len(recipients)))
	if err != nil {
		return MessagingMessageCreateResult{}, mapError(err)
	}
	for _, recipient := range recipients {
		if _, err := tx.Exec(ctx, `INSERT INTO project_messaging_deliveries (id,project_id,message_id,subscriber_id,provider_id,channel,address_ciphertext,address_preview,status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'pending')`, uuid.Must(uuid.NewV7()), projectID, id, recipient.id, providerID, channel, recipient.address, recipient.preview); err != nil {
			return MessagingMessageCreateResult{}, mapError(err)
		}
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.message.create", "messaging_message", id, map[string]any{"topic_id": input.TopicID.String(), "channel": channel, "recipient_count": len(recipients)}); err != nil {
		return MessagingMessageCreateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MessagingMessageCreateResult{}, err
	}
	return MessagingMessageCreateResult{Message: item, Created: true}, nil
}

func messagingIdempotencyLockKey(projectID uuid.UUID, idempotencyKey string) int64 {
	digest := sha256.Sum256([]byte(projectID.String() + "\x00" + idempotencyKey))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func scanMessagingMessageWithHash(row messagingMessageScanner, hash *[]byte) (domain.MessagingMessage, error) {
	var item domain.MessagingMessage
	var id, projectID uuid.UUID
	var topicID *uuid.UUID
	err := row.Scan(&id, &projectID, &topicID, &item.Channel, &item.Status, &item.RecipientCount, &item.SucceededCount, &item.FailedCount, &item.CancelledAt, &item.CreatedAt, &item.UpdatedAt, hash)
	item.ID = id.String()
	item.ProjectID = projectID.String()
	if topicID != nil {
		value := topicID.String()
		item.TopicID = &value
	}
	return item, err
}

func nullableMessagingIdempotencyKey(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (r *Repository) ListMessagingMessages(ctx context.Context, projectID uuid.UUID, actor MessagingActor, limit int, cursor *uuid.UUID) ([]domain.MessagingMessage, string, bool, error) {
	canManage, err := r.requireMessagingRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if limit < 1 || limit > 100 {
		return nil, "", false, ErrInvalidMessaging
	}
	rows, err := r.pool.Query(ctx, `SELECT `+messagingMessageProjection+` FROM project_messaging_messages WHERE project_id=$1 AND ($3::uuid IS NULL OR id<$3) ORDER BY id DESC LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.MessagingMessage, 0, limit)
	for rows.Next() {
		item, scanErr := scanMessagingMessage(rows)
		if scanErr != nil {
			return nil, "", false, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, canManage, nil
}

func (r *Repository) GetMessagingMessage(ctx context.Context, projectID, messageID uuid.UUID, actor MessagingActor) (domain.MessagingMessage, error) {
	if _, err := r.requireMessagingRead(ctx, projectID, actor); err != nil {
		return domain.MessagingMessage{}, err
	}
	item, err := scanMessagingMessage(r.pool.QueryRow(ctx, `SELECT `+messagingMessageProjection+` FROM project_messaging_messages WHERE project_id=$1 AND id=$2`, projectID, messageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MessagingMessage{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) ListMessagingDeliveries(ctx context.Context, projectID, messageID uuid.UUID, actor MessagingActor, limit int, cursor *uuid.UUID) ([]domain.MessagingDelivery, string, error) {
	if _, err := r.requireMessagingRead(ctx, projectID, actor); err != nil {
		return nil, "", err
	}
	if limit < 1 || limit > 100 {
		return nil, "", ErrInvalidMessaging
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_messaging_messages WHERE project_id=$1 AND id=$2)`, projectID, messageID).Scan(&exists); err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT `+messagingDeliveryProjection+` FROM project_messaging_deliveries WHERE project_id=$1 AND message_id=$2 AND ($3::uuid IS NULL OR id<$3) ORDER BY id DESC LIMIT $4`, projectID, messageID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.MessagingDelivery, 0, limit)
	for rows.Next() {
		item, scanErr := scanMessagingDelivery(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func (r *Repository) CancelMessagingMessage(ctx context.Context, projectID, messageID uuid.UUID, actor MessagingActor) (domain.MessagingMessage, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.MessagingMessage{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.MessagingMessage{}, err
	}
	// Delivery rows are updated before the message row so cancellation and the
	// worker's delivery claim acquire locks in the same order (delivery, then
	// message), avoiding a tenant-wide queue deadlock.
	if _, err := tx.Exec(ctx, `UPDATE project_messaging_deliveries SET status='cancelled',updated_at=now() WHERE project_id=$1 AND message_id=$2 AND status='pending'`, projectID, messageID); err != nil {
		return domain.MessagingMessage{}, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM project_messaging_messages WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, messageID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return domain.MessagingMessage{}, ErrNotFound
	} else if err != nil {
		return domain.MessagingMessage{}, err
	}
	if status == "succeeded" || status == "failed" {
		return domain.MessagingMessage{}, ErrMessagingMessageTerminal
	}
	if status != "cancelled" {
		if _, err := tx.Exec(ctx, `UPDATE project_messaging_messages SET status='cancelled',cancelled_at=now(),updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, messageID); err != nil {
			return domain.MessagingMessage{}, err
		}
		if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.message.cancel", "messaging_message", messageID, nil); err != nil {
			return domain.MessagingMessage{}, err
		}
	}
	item, err := scanMessagingMessage(tx.QueryRow(ctx, `SELECT `+messagingMessageProjection+` FROM project_messaging_messages WHERE project_id=$1 AND id=$2`, projectID, messageID))
	if err != nil {
		return domain.MessagingMessage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MessagingMessage{}, err
	}
	return item, nil
}

// ClaimNextMessagingDelivery atomically leases one pending recipient. Provider
// and message predicates are tenant-bound in the same query; provider secrets
// remain ciphertext until the trusted worker decrypts them.
func (r *Repository) ClaimNextMessagingDelivery(ctx context.Context, workerID string) (MessagingDeliveryJob, error) {
	if !validFunctionWorkerID(workerID) {
		return MessagingDeliveryJob{}, ErrInvalidMessaging
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return MessagingDeliveryJob{}, err
	}
	defer tx.Rollback(ctx)
	var job MessagingDeliveryJob
	err = tx.QueryRow(ctx, `
		SELECT d.id,d.message_id,d.project_id,d.subscriber_id,d.provider_id,d.channel,d.address_preview,d.address_ciphertext,m.payload_ciphertext,p.provider,p.enabled,p.credentials_ciphertext,d.attempt_count
		FROM project_messaging_deliveries d
		JOIN project_messaging_messages m ON m.id=d.message_id AND m.project_id=d.project_id
		LEFT JOIN project_messaging_providers p ON p.id=d.provider_id AND p.project_id=d.project_id
		WHERE d.status='pending' AND d.next_attempt_at<=now() AND m.status IN ('queued','processing')
		ORDER BY d.next_attempt_at,d.created_at,d.id
		LIMIT 1
		FOR UPDATE OF d SKIP LOCKED`).Scan(&job.DeliveryID, &job.MessageID, &job.ProjectID, &job.SubscriberID, &job.ProviderID, &job.Channel, &job.AddressPreview, &job.AddressCiphertext, &job.PayloadCiphertext, &job.Provider, &job.ProviderEnabled, &job.ProviderCredentialsCiphertext, &job.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return MessagingDeliveryJob{}, ErrNoMessagingDelivery
	}
	if err != nil {
		return MessagingDeliveryJob{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_messaging_deliveries SET status='running',attempt_count=attempt_count+1,leased_at=now(),worker_id=$2,updated_at=now() WHERE id=$1 AND status='pending'`, job.DeliveryID, workerID); err != nil {
		return MessagingDeliveryJob{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_messaging_messages SET status='processing',updated_at=now() WHERE project_id=$1 AND id=$2 AND status='queued'`, job.ProjectID, job.MessageID); err != nil {
		return MessagingDeliveryJob{}, err
	}
	if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, job.ProjectID, "messaging.delivery.updated", "messaging_delivery", job.DeliveryID, map[string]any{"message_id": job.MessageID.String(), "status": "running"}); err != nil {
		return MessagingDeliveryJob{}, err
	}
	job.AttemptCount++
	if err := tx.Commit(ctx); err != nil {
		return MessagingDeliveryJob{}, err
	}
	return job, nil
}

func (r *Repository) RequeueStaleMessagingDeliveries(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, ErrInvalidMessaging
	}
	result, err := r.pool.Exec(ctx, `UPDATE project_messaging_deliveries SET status='pending',leased_at=NULL,worker_id=NULL,next_attempt_at=LEAST(next_attempt_at,now()),updated_at=now() WHERE status='running' AND leased_at IS NOT NULL AND leased_at < now() - ($1::double precision * interval '1 second')`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	if _, err := r.pool.Exec(ctx, `UPDATE project_messaging_messages m SET status='queued',updated_at=now() WHERE m.status='processing' AND EXISTS(SELECT 1 FROM project_messaging_deliveries d WHERE d.message_id=m.id AND d.project_id=m.project_id AND d.status='pending') AND NOT EXISTS(SELECT 1 FROM project_messaging_deliveries d WHERE d.message_id=m.id AND d.project_id=m.project_id AND d.status='running')`); err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func truncateMessagingError(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > 4000 {
		value = value[:4000]
	}
	return &value
}

func (r *Repository) FinishMessagingDelivery(ctx context.Context, deliveryID uuid.UUID, workerID string, success bool, statusCode *int, lastError string, retryAt *time.Time) error {
	if !validFunctionWorkerID(workerID) {
		return ErrInvalidMessaging
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status, owner string
	var projectID, messageID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(worker_id,''),project_id,message_id FROM project_messaging_deliveries WHERE id=$1 FOR UPDATE`, deliveryID).Scan(&status, &owner, &projectID, &messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMessagingDeliveryNotFound
	}
	if err != nil {
		return err
	}
	if status != "running" || owner != workerID {
		return ErrMessagingDeliveryNotFound
	}
	if statusCode != nil && (*statusCode < 100 || *statusCode > 599) {
		statusCode = nil
	}
	errValue := truncateMessagingError(lastError)
	finalStatus := "failed"
	switch {
	case success:
		finalStatus = "succeeded"
		_, err = tx.Exec(ctx, `UPDATE project_messaging_deliveries SET status='succeeded',leased_at=NULL,worker_id=NULL,last_status_code=$2,last_error=NULL,delivered_at=now(),updated_at=now() WHERE id=$1`, deliveryID, statusCode)
	case retryAt != nil:
		finalStatus = "pending"
		_, err = tx.Exec(ctx, `UPDATE project_messaging_deliveries SET status='pending',leased_at=NULL,worker_id=NULL,last_status_code=$2,last_error=$3,next_attempt_at=$4,updated_at=now() WHERE id=$1`, deliveryID, statusCode, errValue, retryAt.UTC())
	default:
		_, err = tx.Exec(ctx, `UPDATE project_messaging_deliveries SET status='failed',leased_at=NULL,worker_id=NULL,last_status_code=$2,last_error=$3,updated_at=now() WHERE id=$1`, deliveryID, statusCode, errValue)
	}
	if err != nil {
		return err
	}
	if err := refreshMessagingMessageTx(ctx, tx, projectID, messageID); err != nil {
		return err
	}
	if err := r.enqueueRealtimeOnlyEventTx(ctx, tx, projectID, "messaging.delivery.updated", "messaging_delivery", deliveryID, map[string]any{"message_id": messageID.String(), "status": finalStatus}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func refreshMessagingMessageTx(ctx context.Context, tx pgx.Tx, projectID, messageID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE project_messaging_messages m
		SET succeeded_count=c.succeeded_count,failed_count=c.failed_count,
			status=CASE
				WHEN m.status='cancelled' THEN 'cancelled'
				WHEN c.pending_count+c.running_count>0 AND c.running_count>0 THEN 'processing'
				WHEN c.pending_count+c.running_count>0 THEN 'queued'
				WHEN c.failed_count>0 THEN 'failed'
				ELSE 'succeeded'
			END,
			updated_at=now()
		FROM (
			SELECT message_id,
				count(*) FILTER (WHERE status='succeeded')::integer AS succeeded_count,
				count(*) FILTER (WHERE status='failed')::integer AS failed_count,
				count(*) FILTER (WHERE status='pending')::integer AS pending_count,
				count(*) FILTER (WHERE status='running')::integer AS running_count
			FROM project_messaging_deliveries
			WHERE project_id=$1 AND message_id=$2
			GROUP BY message_id
		) c
		WHERE m.project_id=$1 AND m.id=c.message_id`, projectID, messageID)
	return err
}

// MessagingProviderCredentialsForDelivery decrypts a provider row only for a
// worker-owned delivery job. It is separate from the safe provider projection.
func (r *Repository) MessagingProviderCredentialsForDelivery(ctx context.Context, job MessagingDeliveryJob) (MessagingProviderCredentials, error) {
	if job.Provider == nil || job.ProviderEnabled == nil || job.ProviderID == nil {
		return MessagingProviderCredentials{}, ErrMessagingProviderUnavailable
	}
	if r.messagingCipher == nil {
		return MessagingProviderCredentials{}, ErrMessagingNotReady
	}
	plaintext, err := r.messagingCipher.Decrypt(job.ProviderCredentialsCiphertext)
	if err != nil {
		return MessagingProviderCredentials{}, fmt.Errorf("%w: decrypt provider credentials", ErrMessagingNotReady)
	}
	var values map[string]string
	if err := json.Unmarshal(plaintext, &values); err != nil || values == nil {
		return MessagingProviderCredentials{}, fmt.Errorf("%w: provider credentials are corrupt", ErrMessagingNotReady)
	}
	return MessagingProviderCredentials{ProviderID: *job.ProviderID, ProjectID: job.ProjectID, Channel: job.Channel, Provider: *job.Provider, Enabled: *job.ProviderEnabled, Values: values}, nil
}

func (r *Repository) MessagingDeliveryAddress(_ context.Context, job MessagingDeliveryJob) (string, error) {
	if r.messagingCipher == nil {
		return "", ErrMessagingNotReady
	}
	plaintext, err := r.messagingCipher.Decrypt(job.AddressCiphertext)
	if err != nil || strings.TrimSpace(string(plaintext)) == "" {
		return "", fmt.Errorf("%w: decrypt subscriber address", ErrMessagingNotReady)
	}
	return string(plaintext), nil
}

func (r *Repository) MessagingDeliveryPayload(_ context.Context, job MessagingDeliveryJob) (MessagingMessagePayload, error) {
	if r.messagingCipher == nil {
		return MessagingMessagePayload{}, ErrMessagingNotReady
	}
	plaintext, err := r.messagingCipher.Decrypt(job.PayloadCiphertext)
	if err != nil {
		return MessagingMessagePayload{}, fmt.Errorf("%w: decrypt message payload", ErrMessagingNotReady)
	}
	var payload MessagingMessagePayload
	if err := json.Unmarshal(plaintext, &payload); err != nil || strings.TrimSpace(payload.Body) == "" {
		return MessagingMessagePayload{}, fmt.Errorf("%w: message payload is corrupt", ErrMessagingNotReady)
	}
	return payload, nil
}
