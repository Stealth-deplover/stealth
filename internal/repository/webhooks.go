package repository

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/apikey"
	"github.com/nazxf/stealth-api/internal/domain"
)

var (
	ErrWebhookNotReady          = errors.New("webhook service is not ready")
	ErrInvalidWebhook           = errors.New("invalid webhook")
	ErrNoWebhookDelivery        = errors.New("no webhook delivery available")
	ErrWebhookDeliveryNotFound  = errors.New("webhook delivery not found")
	ErrWebhookPayloadTooLarge   = errors.New("webhook payload is too large")
	ErrWebhookSecretUnavailable = errors.New("webhook secret encryption is unavailable")
)

// WebhookActor deliberately aliases the management actor used by the other
// project control planes. Application users can consume data APIs but cannot
// register or inspect delivery endpoints.
type WebhookActor = DatabaseActor

const (
	WebhookConsoleActor     = DatabaseConsoleActor
	WebhookAPIKeyActor      = DatabaseAPIKeyActor
	WebhookApplicationActor = DatabaseApplicationActor
	WebhookAnonymousActor   = DatabaseAnonymousActor
)

type WebhookInput struct {
	Name    string
	URL     string
	Events  []string
	Enabled bool
}

type WebhookPatch struct {
	Name    *string
	URL     *string
	Events  *[]string
	Enabled *bool
}

// WebhookDeliveryJob is an internal worker projection. SecretCiphertext and
// EventPayload never leave the trusted worker process.
type WebhookDeliveryJob struct {
	DeliveryID       uuid.UUID
	EventID          uuid.UUID
	WebhookID        uuid.UUID
	ProjectID        uuid.UUID
	EventName        string
	URL              string
	SecretCiphertext []byte
	EventPayload     []byte
	AttemptCount     int
}

const webhookProjection = `id,project_id,name,url,events,enabled,failure_count,last_delivery_at,last_failure_at,created_at,updated_at`
const webhookDeliveryProjection = `d.id,d.webhook_id,d.event_id,e.event_name,d.status,d.attempt_count,d.last_status_code,d.last_error,d.delivered_at,d.created_at,d.updated_at`

type webhookScanner interface {
	Scan(dest ...any) error
}

func scanWebhook(row webhookScanner) (domain.Webhook, error) {
	var item domain.Webhook
	var id, projectID uuid.UUID
	err := row.Scan(&id, &projectID, &item.Name, &item.URL, &item.Events, &item.Enabled, &item.FailureCount, &item.LastDeliveryAt, &item.LastFailureAt, &item.CreatedAt, &item.UpdatedAt)
	item.ID = id.String()
	item.ProjectID = projectID.String()
	return item, err
}

func scanWebhookDelivery(row webhookScanner) (domain.WebhookDelivery, error) {
	var item domain.WebhookDelivery
	var id, webhookID, eventID uuid.UUID
	err := row.Scan(&id, &webhookID, &eventID, &item.EventName, &item.Status, &item.AttemptCount, &item.LastStatusCode, &item.LastError, &item.DeliveredAt, &item.CreatedAt, &item.UpdatedAt)
	item.ID = id.String()
	item.WebhookID = webhookID.String()
	item.EventID = eventID.String()
	return item, err
}

// NormalizeWebhookURL validates a public configuration URL. The delivery
// worker performs a second, network-level SSRF check after DNS resolution.
func NormalizeWebhookURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) < len("https://a.co") || len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n\t ") {
		return "", ErrInvalidWebhook
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", ErrInvalidWebhook
	}
	if strings.ContainsAny(u.Host, "\x00\r\n\t @") || u.Hostname() == "" {
		return "", ErrInvalidWebhook
	}
	if port := u.Port(); port != "" {
		parsed, parseErr := strconv.Atoi(port)
		if parseErr != nil || parsed < 1 || parsed > 65535 {
			return "", ErrInvalidWebhook
		}
	}
	// url.Parse accepts some malformed percent escapes in path/query only by
	// returning an error; ensure a canonical round trip before persisting.
	if u.String() != value {
		return "", ErrInvalidWebhook
	}
	return value, nil
}

func NormalizeWebhookEvents(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{"*"}, nil
	}
	if len(raw) > 64 {
		return nil, fmt.Errorf("%w: at most 64 events", ErrInvalidWebhook)
	}
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		event := strings.TrimSpace(value)
		if len(event) < 1 || len(event) > 160 || event == "*" {
			if event != "*" {
				return nil, fmt.Errorf("%w: invalid event name", ErrInvalidWebhook)
			}
		} else {
			for index, ch := range event {
				alphaNumeric := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
				if index == 0 && !alphaNumeric {
					return nil, fmt.Errorf("%w: invalid event name", ErrInvalidWebhook)
				}
				if index > 0 && !(alphaNumeric || ch == '.' || ch == '_' || ch == '-') {
					return nil, fmt.Errorf("%w: invalid event name", ErrInvalidWebhook)
				}
			}
		}
		if _, exists := seen[event]; exists {
			return nil, fmt.Errorf("%w: duplicate event name", ErrInvalidWebhook)
		}
		seen[event] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for event := range seen {
		result = append(result, event)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeWebhookName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || len(value) > 120 || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", ErrInvalidWebhook
	}
	return value, nil
}

func newWebhookSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (r *Repository) requireWebhookRead(ctx context.Context, projectID uuid.UUID, actor WebhookActor) (bool, error) {
	switch actor.Kind {
	case WebhookConsoleActor:
		role, err := r.projectRole(ctx, projectID, actor.AccountID)
		if err != nil {
			return false, err
		}
		return role == "owner" || role == "admin", nil
	case WebhookAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "webhooks.read") {
			return false, ErrForbidden
		}
		var active bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM project_api_keys
			WHERE id=$1 AND project_id=$2 AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at>now())
			  AND 'webhooks.read'=ANY(scopes)
		)`, actor.APIKeyID, projectID).Scan(&active); err != nil {
			return false, err
		}
		if !active {
			return false, ErrNotFound
		}
		return apikey.HasScope(actor.APIKeyScopes, "webhooks.write"), nil
	default:
		return false, ErrForbidden
	}
}

func (r *Repository) requireWebhookWriteTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor WebhookActor) error {
	switch actor.Kind {
	case WebhookConsoleActor:
		return requireProjectRoleTx(ctx, tx, projectID, actor.AccountID, "owner", "admin")
	case WebhookAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "webhooks.write") {
			return ErrForbidden
		}
		return requireActiveProjectAPIKeyTx(ctx, tx, projectID, actor.APIKeyID, "webhooks.write")
	default:
		return ErrForbidden
	}
}

func (r *Repository) CreateWebhook(ctx context.Context, id, projectID uuid.UUID, actor WebhookActor, input WebhookInput) (domain.Webhook, string, error) {
	if r.webhookCipher == nil {
		return domain.Webhook{}, "", ErrWebhookNotReady
	}
	name, err := normalizeWebhookName(input.Name)
	if err != nil {
		return domain.Webhook{}, "", err
	}
	webhookURL, err := NormalizeWebhookURL(input.URL)
	if err != nil {
		return domain.Webhook{}, "", err
	}
	events, err := NormalizeWebhookEvents(input.Events)
	if err != nil {
		return domain.Webhook{}, "", err
	}
	secret, err := newWebhookSecret()
	if err != nil {
		return domain.Webhook{}, "", err
	}
	ciphertext, err := r.webhookCipher.Encrypt([]byte(secret))
	if err != nil {
		return domain.Webhook{}, "", fmt.Errorf("%w: %v", ErrWebhookSecretUnavailable, err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Webhook{}, "", err
	}
	defer tx.Rollback(ctx)
	if err := r.requireWebhookWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.Webhook{}, "", err
	}
	item, err := scanWebhook(tx.QueryRow(ctx, `INSERT INTO project_webhooks (id,project_id,name,url,secret_ciphertext,events,enabled) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+webhookProjection, id, projectID, name, webhookURL, ciphertext, events, input.Enabled))
	if err != nil {
		return domain.Webhook{}, "", mapError(err)
	}
	if err := r.auditWebhook(ctx, tx, projectID, actor, "webhook.create", "webhook", id, map[string]any{"name": name, "url": webhookURL, "events": events, "enabled": input.Enabled}); err != nil {
		return domain.Webhook{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Webhook{}, "", err
	}
	return item, secret, nil
}

func (r *Repository) ListWebhooks(ctx context.Context, projectID uuid.UUID, actor WebhookActor, limit int, cursor *uuid.UUID) ([]domain.Webhook, string, bool, error) {
	canManage, err := r.requireWebhookRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if limit < 1 || limit > 100 {
		return nil, "", false, ErrInvalidWebhook
	}
	rows, err := r.pool.Query(ctx, `SELECT `+webhookProjection+` FROM project_webhooks WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.Webhook, 0, limit)
	for rows.Next() {
		item, scanErr := scanWebhook(rows)
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

func (r *Repository) GetWebhook(ctx context.Context, projectID, webhookID uuid.UUID, actor WebhookActor) (domain.Webhook, error) {
	if _, err := r.requireWebhookRead(ctx, projectID, actor); err != nil {
		return domain.Webhook{}, err
	}
	item, err := scanWebhook(r.pool.QueryRow(ctx, `SELECT `+webhookProjection+` FROM project_webhooks WHERE project_id=$1 AND id=$2`, projectID, webhookID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Webhook{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) UpdateWebhook(ctx context.Context, projectID, webhookID uuid.UUID, actor WebhookActor, patch WebhookPatch) (domain.Webhook, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Webhook{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireWebhookWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.Webhook{}, err
	}
	current, err := scanWebhook(tx.QueryRow(ctx, `SELECT `+webhookProjection+` FROM project_webhooks WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, webhookID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Webhook{}, ErrNotFound
	}
	if err != nil {
		return domain.Webhook{}, err
	}
	name, webhookURL, events := current.Name, current.URL, append([]string(nil), current.Events...)
	enabled := current.Enabled
	changed := make([]string, 0, 4)
	if patch.Name != nil {
		name, err = normalizeWebhookName(*patch.Name)
		if err != nil {
			return domain.Webhook{}, err
		}
		if name != current.Name {
			changed = append(changed, "name")
		}
	}
	if patch.URL != nil {
		webhookURL, err = NormalizeWebhookURL(*patch.URL)
		if err != nil {
			return domain.Webhook{}, err
		}
		if webhookURL != current.URL {
			changed = append(changed, "url")
		}
	}
	if patch.Events != nil {
		events, err = NormalizeWebhookEvents(*patch.Events)
		if err != nil {
			return domain.Webhook{}, err
		}
		if strings.Join(events, "\x00") != strings.Join(current.Events, "\x00") {
			changed = append(changed, "events")
		}
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
		if enabled != current.Enabled {
			changed = append(changed, "enabled")
		}
	}
	item := current
	if len(changed) > 0 {
		item, err = scanWebhook(tx.QueryRow(ctx, `UPDATE project_webhooks SET name=$3,url=$4,events=$5,enabled=$6,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING `+webhookProjection, projectID, webhookID, name, webhookURL, events, enabled))
		if err != nil {
			return domain.Webhook{}, mapError(err)
		}
		if err := r.auditWebhook(ctx, tx, projectID, actor, "webhook.update", "webhook", webhookID, map[string]any{"changed_fields": changed}); err != nil {
			return domain.Webhook{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Webhook{}, err
	}
	return item, nil
}

func (r *Repository) DeleteWebhook(ctx context.Context, projectID, webhookID uuid.UUID, actor WebhookActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireWebhookWriteTx(ctx, tx, projectID, actor); err != nil {
		return err
	}
	item, err := scanWebhook(tx.QueryRow(ctx, `SELECT `+webhookProjection+` FROM project_webhooks WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, webhookID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_webhooks WHERE project_id=$1 AND id=$2`, projectID, webhookID); err != nil {
		return err
	}
	if err := r.auditWebhook(ctx, tx, projectID, actor, "webhook.delete", "webhook", webhookID, map[string]any{"name": item.Name, "url": item.URL}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) RotateWebhookSecret(ctx context.Context, projectID, webhookID uuid.UUID, actor WebhookActor) (domain.Webhook, string, error) {
	if r.webhookCipher == nil {
		return domain.Webhook{}, "", ErrWebhookNotReady
	}
	secret, err := newWebhookSecret()
	if err != nil {
		return domain.Webhook{}, "", err
	}
	ciphertext, err := r.webhookCipher.Encrypt([]byte(secret))
	if err != nil {
		return domain.Webhook{}, "", fmt.Errorf("%w: %v", ErrWebhookSecretUnavailable, err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Webhook{}, "", err
	}
	defer tx.Rollback(ctx)
	if err := r.requireWebhookWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.Webhook{}, "", err
	}
	if _, err := scanWebhook(tx.QueryRow(ctx, `SELECT `+webhookProjection+` FROM project_webhooks WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, webhookID)); errors.Is(err, pgx.ErrNoRows) {
		return domain.Webhook{}, "", ErrNotFound
	} else if err != nil {
		return domain.Webhook{}, "", err
	}
	item, err := scanWebhook(tx.QueryRow(ctx, `UPDATE project_webhooks SET secret_ciphertext=$3,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING `+webhookProjection, projectID, webhookID, ciphertext))
	if err != nil {
		return domain.Webhook{}, "", err
	}
	if err := r.auditWebhook(ctx, tx, projectID, actor, "webhook.secret_rotate", "webhook", webhookID, nil); err != nil {
		return domain.Webhook{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Webhook{}, "", err
	}
	return item, secret, nil
}

func (r *Repository) ListWebhookDeliveries(ctx context.Context, projectID, webhookID uuid.UUID, actor WebhookActor, limit int, cursor *uuid.UUID) ([]domain.WebhookDelivery, string, error) {
	if _, err := r.requireWebhookRead(ctx, projectID, actor); err != nil {
		return nil, "", err
	}
	if limit < 1 || limit > 100 {
		return nil, "", ErrInvalidWebhook
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_webhooks WHERE project_id=$1 AND id=$2)`, projectID, webhookID).Scan(&exists); err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT `+webhookDeliveryProjection+` FROM webhook_deliveries d JOIN webhook_events e ON e.id=d.event_id JOIN project_webhooks w ON w.id=d.webhook_id WHERE w.project_id=$1 AND d.webhook_id=$2 AND ($3::uuid IS NULL OR d.id<$3) ORDER BY d.id DESC LIMIT $4`, projectID, webhookID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.WebhookDelivery, 0, limit)
	for rows.Next() {
		item, scanErr := scanWebhookDelivery(rows)
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

// ClaimNextWebhookDelivery atomically leases a pending delivery. The event,
// webhook and project predicates are repeated in the same query so a stale
// row can never cross a tenant boundary.
func (r *Repository) ClaimNextWebhookDelivery(ctx context.Context, workerID string) (WebhookDeliveryJob, error) {
	if !validFunctionWorkerID(workerID) {
		return WebhookDeliveryJob{}, ErrInvalidWebhook
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return WebhookDeliveryJob{}, err
	}
	defer tx.Rollback(ctx)
	var job WebhookDeliveryJob
	err = tx.QueryRow(ctx, `
		SELECT d.id,d.event_id,d.webhook_id,e.project_id,e.event_name,w.url,w.secret_ciphertext,e.payload,d.attempt_count
		FROM webhook_deliveries d
		JOIN webhook_events e ON e.id=d.event_id
		JOIN project_webhooks w ON w.id=d.webhook_id AND w.project_id=e.project_id
		WHERE d.status='pending' AND d.next_attempt_at<=now() AND w.enabled AND e.expires_at>now()
		ORDER BY d.next_attempt_at,d.created_at,d.id
		LIMIT 1
		FOR UPDATE OF d SKIP LOCKED`).Scan(&job.DeliveryID, &job.EventID, &job.WebhookID, &job.ProjectID, &job.EventName, &job.URL, &job.SecretCiphertext, &job.EventPayload, &job.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookDeliveryJob{}, ErrNoWebhookDelivery
	}
	if err != nil {
		return WebhookDeliveryJob{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET status='running',attempt_count=attempt_count+1,leased_at=now(),worker_id=$2,updated_at=now() WHERE id=$1 AND status='pending'`, job.DeliveryID, workerID); err != nil {
		return WebhookDeliveryJob{}, err
	}
	job.AttemptCount++
	if err := tx.Commit(ctx); err != nil {
		return WebhookDeliveryJob{}, err
	}
	return job, nil
}

func (r *Repository) RequeueStaleWebhookDeliveries(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, ErrInvalidWebhook
	}
	result, err := r.pool.Exec(ctx, `UPDATE webhook_deliveries SET status='pending',leased_at=NULL,worker_id=NULL,next_attempt_at=LEAST(next_attempt_at,now()),updated_at=now() WHERE status='running' AND leased_at IS NOT NULL AND leased_at < now() - ($1 * interval '1 second')`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (r *Repository) ExpireWebhookDeliveries(ctx context.Context) (int64, error) {
	result, err := r.pool.Exec(ctx, `UPDATE webhook_deliveries d SET status='failed',last_error='event expired before delivery',updated_at=now() FROM webhook_events e WHERE e.id=d.event_id AND d.status='pending' AND e.expires_at<=now()`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func truncateWebhookError(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > 4000 {
		value = value[:4000]
	}
	return &value
}

func (r *Repository) FinishWebhookDelivery(ctx context.Context, deliveryID uuid.UUID, workerID string, success bool, statusCode *int, lastError string, retryAt *time.Time) error {
	if !validFunctionWorkerID(workerID) {
		return ErrInvalidWebhook
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status, owner string
	var webhookID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(worker_id,''),webhook_id FROM webhook_deliveries WHERE id=$1 FOR UPDATE`, deliveryID).Scan(&status, &owner, &webhookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWebhookDeliveryNotFound
	}
	if err != nil {
		return err
	}
	if status != "running" || owner != workerID {
		return ErrWebhookDeliveryNotFound
	}
	if statusCode != nil && (*statusCode < 100 || *statusCode > 599) {
		statusCode = nil
	}
	errValue := truncateWebhookError(lastError)
	if success {
		if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET status='succeeded',leased_at=NULL,worker_id=NULL,last_status_code=$2,last_error=NULL,delivered_at=now(),updated_at=now() WHERE id=$1`, deliveryID, statusCode); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE project_webhooks SET failure_count=0,last_delivery_at=now(),updated_at=now() WHERE id=$1`, webhookID)
	} else if retryAt != nil {
		if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET status='pending',leased_at=NULL,worker_id=NULL,last_status_code=$2,last_error=$3,next_attempt_at=$4,updated_at=now() WHERE id=$1`, deliveryID, statusCode, errValue, retryAt.UTC()); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE project_webhooks SET failure_count=failure_count+1,last_failure_at=now(),updated_at=now() WHERE id=$1`, webhookID)
	} else {
		if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET status='failed',leased_at=NULL,worker_id=NULL,last_status_code=$2,last_error=$3,updated_at=now() WHERE id=$1`, deliveryID, statusCode, errValue); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE project_webhooks SET failure_count=failure_count+1,last_failure_at=now(),updated_at=now() WHERE id=$1`, webhookID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// auditWebhook appends to both the existing audit stream and the transactional
// webhook outbox. It is also used by the other project control planes.
func (r *Repository) auditWebhook(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor WebhookActor, action, targetType string, target uuid.UUID, metadata map[string]any) error {
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["project_id"] = projectID.String()
	switch actor.Kind {
	case WebhookAPIKeyActor:
		metadata["actor"] = "api_key"
		metadata["api_key_id"] = actor.APIKeyID.String()
	case WebhookApplicationActor:
		metadata["actor"] = "project_user"
		metadata["project_user_id"] = actor.ProjectUserID.String()
		metadata["source"] = "application"
	case WebhookAnonymousActor:
		metadata["actor"] = "anonymous"
		metadata["source"] = "application"
	}
	actorID := uuid.Nil
	if actor.Kind == WebhookConsoleActor {
		actorID = actor.AccountID
	}
	if err := writeAuditMetadata(ctx, tx, orgID, actorID, action, targetType, target, metadata); err != nil {
		return err
	}
	return r.enqueueWebhookEventTx(ctx, tx, projectID, action, targetType, target, metadata)
}

// enqueueWebhookEventTx records every project event in the short-lived outbox
// and creates delivery rows only for matching enabled webhooks. Keeping the
// event independent from integration configuration lets the same transactional
// stream power Realtime subscribers without making webhook configuration a
// prerequisite for observing a project mutation.
func (r *Repository) enqueueWebhookEventTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, eventName, targetType string, target uuid.UUID, metadata map[string]any) error {
	if len(eventName) < 3 || len(eventName) > 160 || len(targetType) < 3 || len(targetType) > 80 {
		return ErrInvalidWebhook
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	eventID := uuid.Must(uuid.NewV7())
	targetValue := any(nil)
	if target != uuid.Nil {
		targetValue = target
	}
	payloadValue := map[string]any{
		"id":         eventID.String(),
		"event":      eventName,
		"project_id": projectID.String(),
		"target": map[string]any{
			"type": targetType,
			"id": func() any {
				if target == uuid.Nil {
					return nil
				}
				return target.String()
			}(),
		},
		"data":       metadata,
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return err
	}
	if len(payload) > 262144 {
		return ErrWebhookPayloadTooLarge
	}
	var insertedID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO webhook_events (id,project_id,event_name,target_type,target_id,payload) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, eventID, projectID, eventName, targetType, targetValue, payload).Scan(&insertedID)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM project_webhooks WHERE project_id=$1 AND enabled AND (events @> ARRAY['*']::text[] OR $2=ANY(events))`, projectID, eventName)
	if err != nil {
		return err
	}
	webhookIDs := make([]uuid.UUID, 0, 4)
	for rows.Next() {
		var webhookID uuid.UUID
		if scanErr := rows.Scan(&webhookID); scanErr != nil {
			rows.Close()
			return scanErr
		}
		webhookIDs = append(webhookIDs, webhookID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, webhookID := range webhookIDs {
		if _, insertErr := tx.Exec(ctx, `INSERT INTO webhook_deliveries (id,event_id,webhook_id) VALUES ($1,$2,$3) ON CONFLICT (event_id,webhook_id) DO NOTHING`, uuid.Must(uuid.NewV7()), insertedID, webhookID); insertErr != nil {
			return insertErr
		}
	}
	return nil
}
