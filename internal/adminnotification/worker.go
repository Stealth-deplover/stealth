// Package adminnotification delivers owner alert transitions from the durable
// PostgreSQL queue. Network delivery is kept in the trusted worker process;
// the API only stores encrypted channel configuration and creates outbox rows
// in the same transaction as an alert event.
package adminnotification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/mailer"
	"github.com/Stealth-deplover/stealth/internal/monitoring"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

const (
	defaultPollInterval = time.Second
	defaultLeaseAge     = 2 * time.Minute
	defaultTimeout      = 10 * time.Second
	defaultMaxAttempts  = 8
	maxResponseBytes    = 4096
)

type Persistence interface {
	RequeueStaleAdminNotificationDeliveries(context.Context, time.Duration) (int64, error)
	ClaimNextAdminNotificationDelivery(context.Context, string, time.Duration) (repository.AdminNotificationDeliveryJob, error)
	FinishAdminNotificationDelivery(context.Context, uuid.UUID, string, bool, string, *time.Time) error
}

type Worker struct {
	Store        Persistence
	Cipher       *functionsecret.Cipher
	Email        mailer.Sender
	WorkerID     string
	HTTPClient   *http.Client
	PollInterval time.Duration
	LeaseAge     time.Duration
	Timeout      time.Duration
	MaxAttempts  int
	Logger       *slog.Logger
}

func New(store Persistence, cipher *functionsecret.Cipher, email mailer.Sender, workerID string, logger *slog.Logger) (*Worker, error) {
	if store == nil || cipher == nil || strings.TrimSpace(workerID) == "" {
		return nil, errors.New("invalid admin notification worker dependencies")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if email == nil {
		email = mailer.DisabledSender{}
	}
	return &Worker{
		Store: store, Cipher: cipher, Email: email, WorkerID: workerID,
		HTTPClient:   monitoring.NewSafeHTTPSClient(defaultTimeout),
		PollInterval: defaultPollInterval, LeaseAge: defaultLeaseAge,
		Timeout: defaultTimeout, MaxAttempts: defaultMaxAttempts, Logger: logger,
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Store == nil || w.Cipher == nil || strings.TrimSpace(w.WorkerID) == "" {
		return errors.New("admin notification worker is not configured")
	}
	poll := w.PollInterval
	if poll <= 0 {
		poll = defaultPollInterval
	}
	leaseAge := w.LeaseAge
	if leaseAge <= 0 {
		leaseAge = defaultLeaseAge
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if _, err := w.Store.RequeueStaleAdminNotificationDeliveries(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			w.Logger.Error("admin notification lease recovery failed", "error", safeError(err))
		}
		processed, err := w.RunOnce(ctx, leaseAge)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			w.Logger.Error("admin notification delivery failed", "error", safeError(err))
		}
		if processed && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context, leaseAge time.Duration) (bool, error) {
	job, err := w.Store.ClaimNextAdminNotificationDelivery(ctx, w.WorkerID, leaseAge)
	if errors.Is(err, repository.ErrNoAdminNotification) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	if job.Attempts > maxAttempts {
		return true, w.Store.FinishAdminNotificationDelivery(ctx, job.DeliveryID, w.WorkerID, false, "maximum notification delivery attempts exceeded", nil)
	}
	config, err := w.Cipher.Decrypt(job.ConfigEncrypted)
	if err != nil {
		return true, w.Store.FinishAdminNotificationDelivery(ctx, job.DeliveryID, w.WorkerID, false, "notification configuration could not be decrypted", nil)
	}
	if err := w.deliver(ctx, job, config); err != nil {
		retryAt := notificationRetryAt(job.Attempts, maxAttempts)
		return true, w.Store.FinishAdminNotificationDelivery(ctx, job.DeliveryID, w.WorkerID, false, safeError(err), retryAt)
	}
	return true, w.Store.FinishAdminNotificationDelivery(ctx, job.DeliveryID, w.WorkerID, true, "", nil)
}

func (w *Worker) deliver(ctx context.Context, job repository.AdminNotificationDeliveryJob, raw []byte) error {
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil || config == nil {
		return errors.New("notification configuration is invalid")
	}
	text := notificationText(job)
	switch job.Kind {
	case "email":
		recipient, ok := config["recipient"].(string)
		if !ok || strings.TrimSpace(recipient) == "" {
			return errors.New("notification recipient is missing")
		}
		if err := w.Email.Send(ctx, mailer.Message{To: recipient, Subject: "Stealth alert: " + job.RuleName, TextBody: text}); err != nil {
			return errors.New("email notification delivery failed")
		}
		return nil
	case "telegram":
		token, tokenOK := config["bot_token"].(string)
		chatID, chatOK := config["chat_id"].(string)
		if !tokenOK || !chatOK || strings.TrimSpace(token) == "" || strings.TrimSpace(chatID) == "" {
			return errors.New("Telegram configuration is incomplete")
		}
		endpoint := "https://api.telegram.org/bot" + url.PathEscape(token) + "/sendMessage"
		return w.sendJSON(ctx, endpoint, map[string]any{"chat_id": chatID, "text": text})
	case "webhook", "slack", "discord":
		endpoint, ok := config["url"].(string)
		if !ok || strings.TrimSpace(endpoint) == "" {
			return errors.New("notification webhook URL is missing")
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || monitoring.ValidatePublicHTTPSURL(parsed) != nil {
			return errors.New("notification webhook URL is invalid")
		}
		payload := map[string]any{"source": "stealth", "alert": map[string]any{
			"name": job.RuleName, "severity": job.Severity, "state": job.State, "message": job.Message,
			"occurred_at": job.OccurredAt.UTC().Format(time.RFC3339),
		}}
		if job.Kind == "slack" || job.Kind == "discord" {
			payload = map[string]any{"content": text}
		}
		return w.sendJSON(ctx, endpoint, payload)
	default:
		return errors.New("notification channel type is unsupported")
	}
}

func (w *Worker) sendJSON(ctx context.Context, endpoint string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("notification payload could not be encoded")
	}
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(deliveryCtx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return errors.New("notification request could not be prepared")
	}
	request.Header.Set("Content-Type", "application/json")
	client := w.HTTPClient
	if client == nil {
		client = monitoring.NewSafeHTTPSClient(timeout)
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("notification request failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("notification endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func notificationRetryAt(attempt, maxAttempts int) *time.Time {
	if attempt >= maxAttempts {
		return nil
	}
	delay := time.Duration(1<<minInt(attempt, 6)) * time.Second
	if delay > time.Hour {
		delay = time.Hour
	}
	value := time.Now().UTC().Add(delay)
	return &value
}

func notificationText(job repository.AdminNotificationDeliveryJob) string {
	return "Stealth alert\n" +
		"Rule: " + safeText(job.RuleName, 120) + "\n" +
		"Severity: " + safeText(job.Severity, 32) + "\n" +
		"State: " + safeText(job.State, 32) + "\n" +
		"Message: " + safeText(job.Message, 1000) + "\n" +
		"Occurred: " + job.OccurredAt.UTC().Format(time.RFC3339)
}

func safeText(value string, maximum int) string {
	value = strings.Map(func(character rune) rune {
		if character == '\x00' || character == '\r' || character == '\n' {
			return ' '
		}
		return character
	}, strings.TrimSpace(value))
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}

func safeError(err error) string {
	if err == nil {
		return "notification delivery failed"
	}
	return safeText(err.Error(), 240)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

var _ Persistence = (*repository.Repository)(nil)
