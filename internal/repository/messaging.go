package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/apikey"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MessagingActor deliberately reuses the management actor shape used by the
// other project control planes. Application sessions can consume application
// data, but provider and subscriber configuration is a management operation.
type MessagingActor = DatabaseActor

const (
	MessagingConsoleActor = DatabaseConsoleActor
	MessagingAPIKeyActor  = DatabaseAPIKeyActor
)

var (
	ErrMessagingNotReady       = errors.New("messaging encryption is not ready")
	ErrInvalidMessaging        = errors.New("invalid messaging configuration")
	ErrMessagingAddressInvalid = errors.New("invalid messaging subscriber address")
)

const (
	maxMessagingCredentialKeys  = 32
	maxMessagingCredentialBytes = 16 << 10
	maxMessagingSubscriberBytes = 2048
)

var messagingCredentialKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
var messagingSMSAddressPattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

type MessagingProviderInput struct {
	Name        string
	Channel     string
	Provider    string
	Credentials map[string]string
	Enabled     bool
}

type MessagingProviderPatch struct {
	Name        *string
	Channel     *string
	Provider    *string
	Credentials *map[string]string
	Enabled     *bool
}

type MessagingTopicInput struct {
	Name        string
	Description string
	Enabled     bool
}

type MessagingTopicPatch struct {
	Name        *string
	Description *string
	Enabled     *bool
}

type MessagingSubscriberInput struct {
	Channel string
	Address string
	Enabled bool
}

type messagingProviderScanner interface {
	Scan(dest ...any) error
}

type messagingTopicScanner interface {
	Scan(dest ...any) error
}

type messagingSubscriberScanner interface {
	Scan(dest ...any) error
}

const messagingProviderProjection = `id,project_id,name,channel,provider,credentials_present,enabled,created_at,updated_at`
const messagingTopicProjection = `id,project_id,name,description,enabled,(SELECT count(*) FROM project_messaging_subscribers s WHERE s.topic_id=project_messaging_topics.id AND s.enabled),created_at,updated_at`
const messagingTopicListProjection = `t.id,t.project_id,t.name,t.description,t.enabled,(SELECT count(*) FROM project_messaging_subscribers s WHERE s.topic_id=t.id AND s.enabled),t.created_at,t.updated_at`
const messagingSubscriberProjection = `id,project_id,topic_id,channel,address_preview,enabled,created_at,updated_at`

func scanMessagingProvider(row messagingProviderScanner) (domain.MessagingProvider, error) {
	var item domain.MessagingProvider
	var id, projectID uuid.UUID
	err := row.Scan(&id, &projectID, &item.Name, &item.Channel, &item.Provider, &item.CredentialsPresent, &item.Enabled, &item.CreatedAt, &item.UpdatedAt)
	item.ID = id.String()
	item.ProjectID = projectID.String()
	return item, err
}

func scanMessagingTopic(row messagingTopicScanner) (domain.MessagingTopic, error) {
	var item domain.MessagingTopic
	var id, projectID uuid.UUID
	err := row.Scan(&id, &projectID, &item.Name, &item.Description, &item.Enabled, &item.SubscriberCount, &item.CreatedAt, &item.UpdatedAt)
	item.ID = id.String()
	item.ProjectID = projectID.String()
	return item, err
}

func scanMessagingSubscriber(row messagingSubscriberScanner) (domain.MessagingSubscriber, error) {
	var item domain.MessagingSubscriber
	var id, projectID, topicID uuid.UUID
	err := row.Scan(&id, &projectID, &topicID, &item.Channel, &item.AddressPreview, &item.Enabled, &item.CreatedAt, &item.UpdatedAt)
	item.ID = id.String()
	item.ProjectID = projectID.String()
	item.TopicID = topicID.String()
	return item, err
}

func normalizeMessagingName(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || len(value) > 120 || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", fmt.Errorf("%w: %s is invalid", ErrInvalidMessaging, field)
	}
	return value, nil
}

func normalizeMessagingDescription(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 2000 || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%w: description is invalid", ErrInvalidMessaging)
	}
	return value, nil
}

func normalizeMessagingChannel(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "email", "sms", "push":
		return value, nil
	default:
		return "", fmt.Errorf("%w: channel must be email, sms, or push", ErrInvalidMessaging)
	}
}

func normalizeMessagingProvider(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 1 || len(value) > 64 || strings.ContainsAny(value, "\x00\r\n\t ") {
		return "", fmt.Errorf("%w: provider is invalid", ErrInvalidMessaging)
	}
	return value, nil
}

func normalizeMessagingCredentials(raw map[string]string) ([]byte, bool, error) {
	if len(raw) > maxMessagingCredentialKeys {
		return nil, false, fmt.Errorf("%w: at most %d credential fields are allowed", ErrInvalidMessaging, maxMessagingCredentialKeys)
	}
	normalized := make(map[string]string, len(raw))
	keys := make([]string, 0, len(raw))
	for key, value := range raw {
		if !messagingCredentialKeyPattern.MatchString(key) || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, false, fmt.Errorf("%w: credential fields are invalid", ErrInvalidMessaging)
		}
		normalized[key] = value
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// encoding/json sorts string map keys, but explicitly building the map
	// above ensures callers cannot mutate the input while it is being encoded.
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, false, fmt.Errorf("%w: credentials could not be encoded", ErrInvalidMessaging)
	}
	if len(encoded) > maxMessagingCredentialBytes {
		return nil, false, fmt.Errorf("%w: credentials exceed %d bytes", ErrInvalidMessaging, maxMessagingCredentialBytes)
	}
	return encoded, len(normalized) > 0, nil
}

func normalizeMessagingAddress(channel, raw string) (string, string, []byte, error) {
	channel, err := normalizeMessagingChannel(channel)
	if err != nil {
		return "", "", nil, err
	}
	address := strings.TrimSpace(raw)
	if address == "" || len(address) > maxMessagingSubscriberBytes || strings.ContainsAny(address, "\x00\r\n\t ") {
		return "", "", nil, ErrMessagingAddressInvalid
	}
	switch channel {
	case "email":
		parsed, parseErr := mail.ParseAddress(address)
		if parseErr != nil || parsed.Address != address || !strings.Contains(address, "@") {
			return "", "", nil, ErrMessagingAddressInvalid
		}
		address = strings.ToLower(address)
	case "sms":
		if !messagingSMSAddressPattern.MatchString(address) {
			return "", "", nil, ErrMessagingAddressInvalid
		}
	case "push":
		if len(address) < 8 || strings.ContainsAny(address, "\x00\r\n") {
			return "", "", nil, ErrMessagingAddressInvalid
		}
	}
	digest := sha256.Sum256([]byte(address))
	return address, messagingAddressPreview(channel, address), digest[:], nil
}

func messagingAddressPreview(channel, address string) string {
	if channel == "email" {
		parts := strings.SplitN(address, "@", 2)
		local := parts[0]
		if len(local) <= 2 {
			local = local[:1] + "•••"
		} else {
			local = local[:1] + "•••" + local[len(local)-1:]
		}
		return local + "@" + parts[1]
	}
	if len(address) <= 4 {
		return "••••"
	}
	return address[:2] + "…" + address[len(address)-2:]
}

func (r *Repository) requireMessagingRead(ctx context.Context, projectID uuid.UUID, actor MessagingActor) (bool, error) {
	switch actor.Kind {
	case MessagingConsoleActor:
		role, err := r.projectRole(ctx, projectID, actor.AccountID)
		if err != nil {
			return false, err
		}
		return role == "owner" || role == "admin", nil
	case MessagingAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "messaging.read") {
			return false, ErrForbidden
		}
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, projectID).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			return false, ErrNotFound
		}
		return apikey.HasScope(actor.APIKeyScopes, "messaging.write"), nil
	default:
		return false, ErrForbidden
	}
}

func (r *Repository) requireMessagingWriteTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor MessagingActor) error {
	switch actor.Kind {
	case MessagingConsoleActor:
		return requireProjectRoleTx(ctx, tx, projectID, actor.AccountID, "owner", "admin")
	case MessagingAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "messaging.write") {
			return ErrForbidden
		}
		return requireActiveProjectAPIKeyTx(ctx, tx, projectID, actor.APIKeyID, "messaging.write")
	default:
		return ErrForbidden
	}
}

func (r *Repository) encryptMessagingCredentials(raw map[string]string) ([]byte, bool, error) {
	if r.messagingCipher == nil {
		return nil, false, ErrMessagingNotReady
	}
	encoded, present, err := normalizeMessagingCredentials(raw)
	if err != nil {
		return nil, false, err
	}
	ciphertext, err := r.messagingCipher.Encrypt(encoded)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrMessagingNotReady, err)
	}
	return ciphertext, present, nil
}

func messagingAuditAccount(actor MessagingActor) uuid.UUID {
	if actor.Kind == MessagingConsoleActor {
		return actor.AccountID
	}
	return uuid.Nil
}

func messagingAuditMetadata(actor MessagingActor, metadata map[string]any) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if actor.Kind == MessagingAPIKeyActor {
		metadata["actor"] = "api_key"
		metadata["api_key_id"] = actor.APIKeyID.String()
	}
	return metadata
}

func (r *Repository) auditMessaging(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor MessagingActor, action, targetType string, target uuid.UUID, metadata map[string]any) error {
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	metadata = messagingAuditMetadata(actor, metadata)
	if err := writeAuditMetadata(ctx, tx, orgID, messagingAuditAccount(actor), action, targetType, target, metadata); err != nil {
		return err
	}
	return r.enqueueWebhookEventTx(ctx, tx, projectID, action, targetType, target, metadata)
}

func (r *Repository) CreateMessagingProvider(ctx context.Context, id, projectID uuid.UUID, actor MessagingActor, input MessagingProviderInput) (domain.MessagingProvider, error) {
	name, err := normalizeMessagingName(input.Name, "name")
	if err != nil {
		return domain.MessagingProvider{}, err
	}
	channel, err := normalizeMessagingChannel(input.Channel)
	if err != nil {
		return domain.MessagingProvider{}, err
	}
	provider, err := normalizeMessagingProvider(input.Provider)
	if err != nil {
		return domain.MessagingProvider{}, err
	}
	ciphertext, credentialsPresent, err := r.encryptMessagingCredentials(input.Credentials)
	if err != nil {
		return domain.MessagingProvider{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.MessagingProvider{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.MessagingProvider{}, err
	}
	item, err := scanMessagingProvider(tx.QueryRow(ctx, `
		INSERT INTO project_messaging_providers (id,project_id,name,channel,provider,credentials_ciphertext,credentials_present,enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+messagingProviderProjection, id, projectID, name, channel, provider, ciphertext, credentialsPresent, input.Enabled))
	if err != nil {
		return domain.MessagingProvider{}, mapError(err)
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.provider.create", "messaging_provider", id, map[string]any{
		"name": name, "channel": channel, "provider": provider, "credentials_present": credentialsPresent, "enabled": input.Enabled,
	}); err != nil {
		return domain.MessagingProvider{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MessagingProvider{}, err
	}
	return item, nil
}

func (r *Repository) ListMessagingProviders(ctx context.Context, projectID uuid.UUID, actor MessagingActor, limit int, cursor *uuid.UUID) ([]domain.MessagingProvider, string, bool, error) {
	canManage, err := r.requireMessagingRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if limit < 1 || limit > 100 {
		return nil, "", false, ErrInvalidMessaging
	}
	rows, err := r.pool.Query(ctx, `SELECT `+messagingProviderProjection+` FROM project_messaging_providers WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.MessagingProvider, 0, limit)
	for rows.Next() {
		item, scanErr := scanMessagingProvider(rows)
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

func (r *Repository) GetMessagingProvider(ctx context.Context, projectID, providerID uuid.UUID, actor MessagingActor) (domain.MessagingProvider, error) {
	if _, err := r.requireMessagingRead(ctx, projectID, actor); err != nil {
		return domain.MessagingProvider{}, err
	}
	item, err := scanMessagingProvider(r.pool.QueryRow(ctx, `SELECT `+messagingProviderProjection+` FROM project_messaging_providers WHERE project_id=$1 AND id=$2`, projectID, providerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MessagingProvider{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) UpdateMessagingProvider(ctx context.Context, projectID, providerID uuid.UUID, actor MessagingActor, patch MessagingProviderPatch) (domain.MessagingProvider, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.MessagingProvider{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.MessagingProvider{}, err
	}
	var name, channel, provider string
	var credentialsCiphertext []byte
	var credentialsPresent, enabled bool
	err = tx.QueryRow(ctx, `SELECT name,channel,provider,credentials_ciphertext,credentials_present,enabled FROM project_messaging_providers WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, providerID).Scan(&name, &channel, &provider, &credentialsCiphertext, &credentialsPresent, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MessagingProvider{}, ErrNotFound
	}
	if err != nil {
		return domain.MessagingProvider{}, err
	}
	originalName, originalChannel, originalProvider := name, channel, provider
	changed := make([]string, 0, 5)
	if patch.Name != nil {
		name, err = normalizeMessagingName(*patch.Name, "name")
		if err != nil {
			return domain.MessagingProvider{}, err
		}
		if name != originalName {
			changed = append(changed, "name")
		}
	}
	if patch.Channel != nil {
		channel, err = normalizeMessagingChannel(*patch.Channel)
		if err != nil {
			return domain.MessagingProvider{}, err
		}
		if channel != originalChannel {
			changed = append(changed, "channel")
		}
	}
	if patch.Provider != nil {
		provider, err = normalizeMessagingProvider(*patch.Provider)
		if err != nil {
			return domain.MessagingProvider{}, err
		}
		if provider != originalProvider {
			changed = append(changed, "provider")
		}
	}
	if patch.Credentials != nil {
		credentialsCiphertext, credentialsPresent, err = r.encryptMessagingCredentials(*patch.Credentials)
		if err != nil {
			return domain.MessagingProvider{}, err
		}
		changed = append(changed, "credentials")
	}
	if patch.Enabled != nil {
		if enabled != *patch.Enabled {
			changed = append(changed, "enabled")
		}
		enabled = *patch.Enabled
	}
	if len(changed) == 0 {
		item, scanErr := scanMessagingProvider(tx.QueryRow(ctx, `SELECT `+messagingProviderProjection+` FROM project_messaging_providers WHERE project_id=$1 AND id=$2`, projectID, providerID))
		if scanErr != nil {
			return domain.MessagingProvider{}, scanErr
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.MessagingProvider{}, err
		}
		return item, nil
	}
	item, err := scanMessagingProvider(tx.QueryRow(ctx, `
		UPDATE project_messaging_providers
		SET name=$3,channel=$4,provider=$5,credentials_ciphertext=$6,credentials_present=$7,enabled=$8,updated_at=now()
		WHERE project_id=$1 AND id=$2
		RETURNING `+messagingProviderProjection, projectID, providerID, name, channel, provider, credentialsCiphertext, credentialsPresent, enabled))
	if err != nil {
		return domain.MessagingProvider{}, mapError(err)
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.provider.update", "messaging_provider", providerID, map[string]any{"fields": changed}); err != nil {
		return domain.MessagingProvider{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MessagingProvider{}, err
	}
	return item, nil
}

func (r *Repository) DeleteMessagingProvider(ctx context.Context, projectID, providerID uuid.UUID, actor MessagingActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_messaging_providers WHERE project_id=$1 AND id=$2)`, projectID, providerID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_messaging_providers WHERE project_id=$1 AND id=$2`, projectID, providerID); err != nil {
		return err
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.provider.delete", "messaging_provider", providerID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateMessagingTopic(ctx context.Context, id, projectID uuid.UUID, actor MessagingActor, input MessagingTopicInput) (domain.MessagingTopic, error) {
	name, err := normalizeMessagingName(input.Name, "name")
	if err != nil {
		return domain.MessagingTopic{}, err
	}
	description, err := normalizeMessagingDescription(input.Description)
	if err != nil {
		return domain.MessagingTopic{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.MessagingTopic{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.MessagingTopic{}, err
	}
	item, err := scanMessagingTopic(tx.QueryRow(ctx, `
		INSERT INTO project_messaging_topics (id,project_id,name,description,enabled)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING `+messagingTopicProjection, id, projectID, name, description, input.Enabled))
	if err != nil {
		return domain.MessagingTopic{}, mapError(err)
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.topic.create", "messaging_topic", id, map[string]any{"name": name, "enabled": input.Enabled}); err != nil {
		return domain.MessagingTopic{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MessagingTopic{}, err
	}
	return item, nil
}

func (r *Repository) ListMessagingTopics(ctx context.Context, projectID uuid.UUID, actor MessagingActor, limit int, cursor *uuid.UUID) ([]domain.MessagingTopic, string, bool, error) {
	canManage, err := r.requireMessagingRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if limit < 1 || limit > 100 {
		return nil, "", false, ErrInvalidMessaging
	}
	rows, err := r.pool.Query(ctx, `SELECT `+messagingTopicListProjection+` FROM project_messaging_topics t WHERE t.project_id=$1 AND ($3::uuid IS NULL OR t.id>$3) ORDER BY t.id LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.MessagingTopic, 0, limit)
	for rows.Next() {
		item, scanErr := scanMessagingTopic(rows)
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

func (r *Repository) GetMessagingTopic(ctx context.Context, projectID, topicID uuid.UUID, actor MessagingActor) (domain.MessagingTopic, error) {
	if _, err := r.requireMessagingRead(ctx, projectID, actor); err != nil {
		return domain.MessagingTopic{}, err
	}
	item, err := scanMessagingTopic(r.pool.QueryRow(ctx, `SELECT `+messagingTopicProjection+` FROM project_messaging_topics t WHERE t.project_id=$1 AND t.id=$2`, projectID, topicID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MessagingTopic{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) UpdateMessagingTopic(ctx context.Context, projectID, topicID uuid.UUID, actor MessagingActor, patch MessagingTopicPatch) (domain.MessagingTopic, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.MessagingTopic{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.MessagingTopic{}, err
	}
	var name, description string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT name,description,enabled FROM project_messaging_topics WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, topicID).Scan(&name, &description, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MessagingTopic{}, ErrNotFound
	}
	if err != nil {
		return domain.MessagingTopic{}, err
	}
	changed := make([]string, 0, 3)
	if patch.Name != nil {
		normalized, normalizeErr := normalizeMessagingName(*patch.Name, "name")
		if normalizeErr != nil {
			return domain.MessagingTopic{}, normalizeErr
		}
		if normalized != name {
			changed = append(changed, "name")
		}
		name = normalized
	}
	if patch.Description != nil {
		normalized, normalizeErr := normalizeMessagingDescription(*patch.Description)
		if normalizeErr != nil {
			return domain.MessagingTopic{}, normalizeErr
		}
		if normalized != description {
			changed = append(changed, "description")
		}
		description = normalized
	}
	if patch.Enabled != nil {
		if enabled != *patch.Enabled {
			changed = append(changed, "enabled")
		}
		enabled = *patch.Enabled
	}
	if len(changed) == 0 {
		item, scanErr := scanMessagingTopic(tx.QueryRow(ctx, `SELECT `+messagingTopicProjection+` FROM project_messaging_topics t WHERE t.project_id=$1 AND t.id=$2`, projectID, topicID))
		if scanErr != nil {
			return domain.MessagingTopic{}, scanErr
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.MessagingTopic{}, err
		}
		return item, nil
	}
	item, err := scanMessagingTopic(tx.QueryRow(ctx, `
		UPDATE project_messaging_topics
		SET name=$3,description=$4,enabled=$5,updated_at=now()
		WHERE project_id=$1 AND id=$2
		RETURNING `+messagingTopicProjection, projectID, topicID, name, description, enabled))
	if err != nil {
		return domain.MessagingTopic{}, mapError(err)
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.topic.update", "messaging_topic", topicID, map[string]any{"fields": changed}); err != nil {
		return domain.MessagingTopic{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MessagingTopic{}, err
	}
	return item, nil
}

func (r *Repository) DeleteMessagingTopic(ctx context.Context, projectID, topicID uuid.UUID, actor MessagingActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_messaging_topics WHERE project_id=$1 AND id=$2)`, projectID, topicID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_messaging_topics WHERE project_id=$1 AND id=$2`, projectID, topicID); err != nil {
		return err
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.topic.delete", "messaging_topic", topicID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateMessagingSubscriber(ctx context.Context, id, projectID, topicID uuid.UUID, actor MessagingActor, input MessagingSubscriberInput) (domain.MessagingSubscriber, error) {
	channel, err := normalizeMessagingChannel(input.Channel)
	if err != nil {
		return domain.MessagingSubscriber{}, err
	}
	address, preview, addressHash, err := normalizeMessagingAddress(channel, input.Address)
	if err != nil {
		return domain.MessagingSubscriber{}, err
	}
	if r.messagingCipher == nil {
		return domain.MessagingSubscriber{}, ErrMessagingNotReady
	}
	addressCiphertext, err := r.messagingCipher.Encrypt([]byte(address))
	if err != nil {
		return domain.MessagingSubscriber{}, fmt.Errorf("%w: %v", ErrMessagingNotReady, err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.MessagingSubscriber{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.MessagingSubscriber{}, err
	}
	item, err := scanMessagingSubscriber(tx.QueryRow(ctx, `
		INSERT INTO project_messaging_subscribers (id,project_id,topic_id,channel,address_ciphertext,address_hash,address_preview,enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+messagingSubscriberProjection, id, projectID, topicID, channel, addressCiphertext, addressHash, preview, input.Enabled))
	if err != nil {
		return domain.MessagingSubscriber{}, mapError(err)
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.subscriber.create", "messaging_subscriber", id, map[string]any{"topic_id": topicID.String(), "channel": channel, "address_preview": preview, "enabled": input.Enabled}); err != nil {
		return domain.MessagingSubscriber{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MessagingSubscriber{}, err
	}
	return item, nil
}

func (r *Repository) ListMessagingSubscribers(ctx context.Context, projectID, topicID uuid.UUID, actor MessagingActor, limit int, cursor *uuid.UUID) ([]domain.MessagingSubscriber, string, bool, error) {
	canManage, err := r.requireMessagingRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if limit < 1 || limit > 100 {
		return nil, "", false, ErrInvalidMessaging
	}
	var topicExists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_messaging_topics WHERE project_id=$1 AND id=$2)`, projectID, topicID).Scan(&topicExists); err != nil {
		return nil, "", false, err
	}
	if !topicExists {
		return nil, "", false, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT `+messagingSubscriberProjection+` FROM project_messaging_subscribers WHERE project_id=$1 AND topic_id=$2 AND ($4::uuid IS NULL OR id>$4) ORDER BY id LIMIT $3`, projectID, topicID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.MessagingSubscriber, 0, limit)
	for rows.Next() {
		item, scanErr := scanMessagingSubscriber(rows)
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

func (r *Repository) GetMessagingSubscriber(ctx context.Context, projectID, topicID, subscriberID uuid.UUID, actor MessagingActor) (domain.MessagingSubscriber, error) {
	if _, err := r.requireMessagingRead(ctx, projectID, actor); err != nil {
		return domain.MessagingSubscriber{}, err
	}
	item, err := scanMessagingSubscriber(r.pool.QueryRow(ctx, `SELECT `+messagingSubscriberProjection+` FROM project_messaging_subscribers WHERE project_id=$1 AND topic_id=$2 AND id=$3`, projectID, topicID, subscriberID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MessagingSubscriber{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) DeleteMessagingSubscriber(ctx context.Context, projectID, topicID, subscriberID uuid.UUID, actor MessagingActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireMessagingWriteTx(ctx, tx, projectID, actor); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_messaging_subscribers WHERE project_id=$1 AND topic_id=$2 AND id=$3)`, projectID, topicID, subscriberID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_messaging_subscribers WHERE project_id=$1 AND topic_id=$2 AND id=$3`, projectID, topicID, subscriberID); err != nil {
		return err
	}
	if err := r.auditMessaging(ctx, tx, projectID, actor, "messaging.subscriber.delete", "messaging_subscriber", subscriberID, map[string]any{"topic_id": topicID.String()}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MessagingProviderCredentials is an internal worker projection. It is kept
// separate from domain.MessagingProvider so handlers cannot accidentally
// serialize the decrypted secret material.
type MessagingProviderCredentials struct {
	ProviderID uuid.UUID
	ProjectID  uuid.UUID
	Channel    string
	Provider   string
	Enabled    bool
	Values     map[string]string
}

func (r *Repository) MessagingProviderCredentials(ctx context.Context, projectID, providerID uuid.UUID) (MessagingProviderCredentials, error) {
	if r.messagingCipher == nil {
		return MessagingProviderCredentials{}, ErrMessagingNotReady
	}
	var result MessagingProviderCredentials
	var ciphertext []byte
	if err := r.pool.QueryRow(ctx, `SELECT id,project_id,channel,provider,enabled,credentials_ciphertext FROM project_messaging_providers WHERE project_id=$1 AND id=$2`, projectID, providerID).Scan(&result.ProviderID, &result.ProjectID, &result.Channel, &result.Provider, &result.Enabled, &ciphertext); errors.Is(err, pgx.ErrNoRows) {
		return MessagingProviderCredentials{}, ErrNotFound
	} else if err != nil {
		return MessagingProviderCredentials{}, err
	}
	plaintext, err := r.messagingCipher.Decrypt(ciphertext)
	if err != nil {
		return MessagingProviderCredentials{}, fmt.Errorf("%w: decrypt provider credentials", ErrMessagingNotReady)
	}
	if err := json.Unmarshal(plaintext, &result.Values); err != nil || result.Values == nil {
		return MessagingProviderCredentials{}, fmt.Errorf("%w: provider credentials are corrupt", ErrMessagingNotReady)
	}
	return result, nil
}

func (r *Repository) MessagingSubscriberAddress(ctx context.Context, projectID, topicID, subscriberID uuid.UUID) (string, error) {
	if r.messagingCipher == nil {
		return "", ErrMessagingNotReady
	}
	var ciphertext []byte
	if err := r.pool.QueryRow(ctx, `SELECT address_ciphertext FROM project_messaging_subscribers WHERE project_id=$1 AND topic_id=$2 AND id=$3`, projectID, topicID, subscriberID).Scan(&ciphertext); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	plaintext, err := r.messagingCipher.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("%w: decrypt subscriber address", ErrMessagingNotReady)
	}
	return string(plaintext), nil
}
