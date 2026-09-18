package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	adminNotificationMaxLimit  = 100
	adminNotificationMaxConfig = 32 << 10
)

var (
	ErrInvalidAdminNotification = errors.New("invalid admin notification channel")
	ErrNoAdminNotification      = errors.New("no admin notification delivery available")
)

type AdminNotificationChannelInput struct {
	Name    string
	Kind    string
	Enabled bool
	Config  []byte
}

type AdminNotificationDeliveryJob struct {
	DeliveryID      uuid.UUID
	ChannelID       uuid.UUID
	ChannelName     string
	Kind            string
	ConfigEncrypted []byte
	AlertEventID    *uuid.UUID
	RuleName        string
	Severity        string
	State           string
	Message         string
	OccurredAt      time.Time
	Attempts        int
}

const adminNotificationTestMessage = "This is a test notification from the Stealth admin control room."

const adminNotificationProjection = `
	c.id::text,c.name,c.kind,c.enabled,
	(c.config_encrypted IS NOT NULL AND octet_length(c.config_encrypted) > 0),
	c.last_delivery_at,c.last_delivery_status,c.last_error,
	c.created_by_account_id::text,c.created_at,c.updated_at`

func (r *Repository) ListAdminNotificationChannels(ctx context.Context, limit int) ([]domain.AdminNotificationChannel, error) {
	if r == nil || r.pool == nil || limit < 1 || limit > adminNotificationMaxLimit {
		return nil, ErrInvalidAdminNotification
	}
	rows, err := r.pool.Query(ctx, `SELECT `+adminNotificationProjection+` FROM admin_notification_channels c ORDER BY c.updated_at DESC,c.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AdminNotificationChannel, 0, limit)
	for rows.Next() {
		item, err := scanAdminNotificationChannel(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) AdminNotificationChannelByID(ctx context.Context, id uuid.UUID) (domain.AdminNotificationChannel, error) {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return domain.AdminNotificationChannel{}, ErrNotFound
	}
	item, err := scanAdminNotificationChannel(r.pool.QueryRow(ctx, `SELECT `+adminNotificationProjection+` FROM admin_notification_channels c WHERE c.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminNotificationChannel{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateAdminNotificationChannel(ctx context.Context, accountID, id uuid.UUID, input AdminNotificationChannelInput) (domain.AdminNotificationChannel, error) {
	normalized, err := normalizeAdminNotificationChannelInput(input)
	if err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	if r == nil || r.pool == nil || r.adminCipher == nil {
		return domain.AdminNotificationChannel{}, errors.New("notification secret encryption is unavailable")
	}
	ciphertext, err := r.adminCipher.Encrypt(normalized.Config)
	if err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO admin_notification_channels (id,name,kind,enabled,config_encrypted,created_by_account_id) VALUES ($1,$2,$3,$4,$5,$6)`, id, normalized.Name, normalized.Kind, normalized.Enabled, ciphertext, accountID); err != nil {
		return domain.AdminNotificationChannel{}, mapError(err)
	}
	item, err := scanAdminNotificationChannel(tx.QueryRow(ctx, `SELECT `+adminNotificationProjection+` FROM admin_notification_channels c WHERE c.id=$1`, id))
	if err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.notification_channel.create", "admin_notification_channel", id, map[string]any{"kind": normalized.Kind}); err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	return item, nil
}

func (r *Repository) UpdateAdminNotificationChannel(ctx context.Context, accountID, id uuid.UUID, input AdminNotificationChannelInput) (domain.AdminNotificationChannel, error) {
	normalized, err := normalizeAdminNotificationChannelInput(input)
	if err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	if r == nil || r.pool == nil || r.adminCipher == nil || id == uuid.Nil {
		return domain.AdminNotificationChannel{}, ErrInvalidAdminNotification
	}
	ciphertext, err := r.adminCipher.Encrypt(normalized.Config)
	if err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE admin_notification_channels SET name=$2,kind=$3,enabled=$4,config_encrypted=$5,updated_at=now() WHERE id=$1`, id, normalized.Name, normalized.Kind, normalized.Enabled, ciphertext)
	if err != nil {
		return domain.AdminNotificationChannel{}, mapError(err)
	}
	if result.RowsAffected() == 0 {
		return domain.AdminNotificationChannel{}, ErrNotFound
	}
	item, err := scanAdminNotificationChannel(tx.QueryRow(ctx, `SELECT `+adminNotificationProjection+` FROM admin_notification_channels c WHERE c.id=$1`, id))
	if err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.notification_channel.update", "admin_notification_channel", id, map[string]any{"kind": normalized.Kind}); err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	return item, nil
}

func (r *Repository) DeleteAdminNotificationChannel(ctx context.Context, accountID, id uuid.UUID) error {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM admin_notification_channels WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.notification_channel.delete", "admin_notification_channel", id, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) EnqueueAdminNotificationTest(ctx context.Context, accountID, channelID uuid.UUID) (uuid.UUID, error) {
	if r == nil || r.pool == nil || accountID == uuid.Nil || channelID == uuid.Nil {
		return uuid.Nil, ErrInvalidAdminNotification
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	if err := requireInstanceAdminTx(ctx, tx, accountID); err != nil {
		return uuid.Nil, err
	}
	var enabled, configured bool
	if err := tx.QueryRow(ctx, `
		SELECT enabled,(config_encrypted IS NOT NULL AND octet_length(config_encrypted) > 0)
		FROM admin_notification_channels WHERE id=$1`, channelID).Scan(&enabled, &configured); errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	} else if err != nil {
		return uuid.Nil, err
	}
	if !enabled || !configured {
		return uuid.Nil, fmt.Errorf("%w: channel must be enabled and configured", ErrInvalidAdminNotification)
	}
	deliveryID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO admin_notification_deliveries (id,channel_id,test_message)
		VALUES ($1,$2,$3)`, deliveryID, channelID, adminNotificationTestMessage); err != nil {
		return uuid.Nil, mapError(err)
	}
	if err := writeInstanceAuditTx(ctx, tx, accountID, "admin.notification_channel.test", "admin_notification_channel", channelID, map[string]any{}); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return deliveryID, nil
}

func (r *Repository) RequeueStaleAdminNotificationDeliveries(ctx context.Context, leaseAge time.Duration) (int64, error) {
	if r == nil || r.pool == nil || leaseAge <= 0 {
		return 0, ErrInvalidAdminNotification
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE admin_notification_deliveries
		SET status='pending',leased_at=NULL,worker_id=NULL,available_at=LEAST(available_at,now())
		WHERE status='running' AND leased_at < now() - ($1::double precision * interval '1 second')`, leaseAge.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (r *Repository) ClaimNextAdminNotificationDelivery(ctx context.Context, workerID string, leaseAge time.Duration) (AdminNotificationDeliveryJob, error) {
	if r == nil || r.pool == nil || !validFunctionWorkerID(workerID) || leaseAge <= 0 {
		return AdminNotificationDeliveryJob{}, ErrInvalidAdminNotification
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AdminNotificationDeliveryJob{}, err
	}
	defer tx.Rollback(ctx)
	var job AdminNotificationDeliveryJob
	err = tx.QueryRow(ctx, `
		SELECT d.id,d.channel_id,c.name,c.kind,c.config_encrypted,d.alert_event_id,
		       COALESCE(r.name,'Stealth notification test'),
		       COALESCE(r.severity,'info'),
		       COALESCE(e.state,'test'),
		       COALESCE(e.message,d.test_message),
		       COALESCE(e.occurred_at,d.created_at),d.attempts
		FROM admin_notification_deliveries d
		JOIN admin_notification_channels c ON c.id=d.channel_id
		LEFT JOIN admin_alert_events e ON e.id=d.alert_event_id
		LEFT JOIN admin_alert_rules r ON r.id=e.rule_id
		WHERE d.status='pending' AND d.available_at<=now() AND c.enabled
		  AND (d.alert_event_id IS NOT NULL OR d.test_message IS NOT NULL)
		  AND (d.leased_at IS NULL OR d.leased_at < now() - ($1::double precision * interval '1 second'))
		ORDER BY d.available_at,d.id
		LIMIT 1 FOR UPDATE OF d SKIP LOCKED`, leaseAge.Seconds()).Scan(
		&job.DeliveryID, &job.ChannelID, &job.ChannelName, &job.Kind, &job.ConfigEncrypted,
		&job.AlertEventID, &job.RuleName, &job.Severity, &job.State, &job.Message, &job.OccurredAt, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminNotificationDeliveryJob{}, ErrNoAdminNotification
	}
	if err != nil {
		return AdminNotificationDeliveryJob{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE admin_notification_deliveries SET status='running',attempts=attempts+1,leased_at=now(),worker_id=$2 WHERE id=$1 AND status='pending'`, job.DeliveryID, workerID); err != nil {
		return AdminNotificationDeliveryJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminNotificationDeliveryJob{}, err
	}
	job.Attempts++
	return job, nil
}

func (r *Repository) FinishAdminNotificationDelivery(ctx context.Context, deliveryID uuid.UUID, workerID string, success bool, lastError string, retryAt *time.Time) error {
	if r == nil || r.pool == nil || deliveryID == uuid.Nil || !validFunctionWorkerID(workerID) {
		return ErrInvalidAdminNotification
	}
	lastError = normalizeAdminMonitorError(lastError)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status := "failed"
	if success {
		status = "delivered"
	} else if retryAt != nil {
		status = "pending"
	}
	result, err := tx.Exec(ctx, `
		UPDATE admin_notification_deliveries
		SET status=$2,last_error=$3,available_at=COALESCE($4,available_at),delivered_at=CASE WHEN $2='delivered' THEN now() ELSE delivered_at END,leased_at=NULL,worker_id=NULL
		WHERE id=$1 AND status='running' AND worker_id=$5`, deliveryID, status, nullableNotificationError(lastError), retryAt, workerID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	channelStatus := "failed"
	if success {
		channelStatus = "success"
	}
	_, err = tx.Exec(ctx, `
		UPDATE admin_notification_channels c
		SET last_delivery_at=now(),last_delivery_status=$2,last_error=$3,updated_at=now()
		WHERE c.id=(SELECT channel_id FROM admin_notification_deliveries WHERE id=$1)`, deliveryID, channelStatus, nullableNotificationError(lastError))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func enqueueAdminNotificationDeliveriesTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT id FROM admin_notification_channels WHERE enabled ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID uuid.UUID
		if err := rows.Scan(&channelID); err != nil {
			return err
		}
		deliveryID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO admin_notification_deliveries (id,channel_id,alert_event_id) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, deliveryID, channelID, eventID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scanAdminNotificationChannel(row interface{ Scan(...any) error }) (domain.AdminNotificationChannel, error) {
	var item domain.AdminNotificationChannel
	if err := row.Scan(&item.ID, &item.Name, &item.Kind, &item.Enabled, &item.SecretConfigured, &item.LastDeliveryAt, &item.LastDeliveryStatus, &item.LastError, &item.CreatedByAccountID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.AdminNotificationChannel{}, err
	}
	return item, nil
}

func normalizeAdminNotificationChannelInput(input AdminNotificationChannelInput) (AdminNotificationChannelInput, error) {
	name, err := normalizeAdminControlText(input.Name, 1, 120)
	if err != nil {
		return AdminNotificationChannelInput{}, fmt.Errorf("%w: channel name is invalid", ErrInvalidAdminNotification)
	}
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	if kind != "email" && kind != "webhook" && kind != "slack" && kind != "discord" && kind != "telegram" {
		return AdminNotificationChannelInput{}, fmt.Errorf("%w: channel kind is unsupported", ErrInvalidAdminNotification)
	}
	if len(input.Config) == 0 || len(input.Config) > adminNotificationMaxConfig || !json.Valid(input.Config) {
		return AdminNotificationChannelInput{}, fmt.Errorf("%w: channel configuration is invalid", ErrInvalidAdminNotification)
	}
	var config map[string]any
	if err := json.Unmarshal(input.Config, &config); err != nil || config == nil {
		return AdminNotificationChannelInput{}, fmt.Errorf("%w: channel configuration must be an object", ErrInvalidAdminNotification)
	}
	for key, value := range config {
		if len(key) > 80 || strings.ContainsAny(key, "\x00\r\n") || !notificationValueSafe(value) {
			return AdminNotificationChannelInput{}, fmt.Errorf("%w: channel configuration contains an invalid value", ErrInvalidAdminNotification)
		}
	}
	switch kind {
	case "email":
		recipient, ok := config["recipient"].(string)
		if !ok || !validNotificationEmail(recipient) {
			return AdminNotificationChannelInput{}, fmt.Errorf("%w: email recipient is invalid", ErrInvalidAdminNotification)
		}
	case "telegram":
		if !notificationString(config, "bot_token", 512) || !notificationString(config, "chat_id", 128) {
			return AdminNotificationChannelInput{}, fmt.Errorf("%w: Telegram configuration is incomplete", ErrInvalidAdminNotification)
		}
	default:
		endpoint, ok := config["url"].(string)
		parsed, parseErr := url.Parse(strings.TrimSpace(endpoint))
		if !ok || parseErr != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || len(endpoint) > 2048 {
			return AdminNotificationChannelInput{}, fmt.Errorf("%w: HTTPS webhook URL is invalid", ErrInvalidAdminNotification)
		}
	}
	return AdminNotificationChannelInput{Name: name, Kind: kind, Enabled: input.Enabled, Config: append([]byte(nil), input.Config...)}, nil
}

func notificationValueSafe(value any) bool {
	switch typed := value.(type) {
	case string:
		return len(typed) <= 1<<20 && !strings.ContainsAny(typed, "\x00\r\n")
	case bool, float64:
		return true
	case []any:
		if len(typed) > 64 {
			return false
		}
		for _, item := range typed {
			if !notificationValueSafe(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func notificationString(config map[string]any, key string, max int) bool {
	value, ok := config[key].(string)
	return ok && strings.TrimSpace(value) != "" && len(value) <= max
}

func validNotificationEmail(value string) bool {
	address, err := mail.ParseAddress(strings.TrimSpace(value))
	return err == nil && address.Address == strings.TrimSpace(value) && len(value) <= 320
}

func nullableNotificationError(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
