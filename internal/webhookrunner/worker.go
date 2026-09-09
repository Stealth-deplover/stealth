// Package webhookrunner delivers project webhooks from the PostgreSQL
// transactional outbox. It runs in the trusted worker process, never in the
// public API process, so network retries cannot hold an HTTP request open.
package webhookrunner

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/nazxf/stealth-api/internal/functionsecret"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/retry"
)

const (
	defaultPollInterval = 500 * time.Millisecond
	defaultLeaseAge     = 20 * time.Minute
	defaultTimeout      = 35 * time.Second
	defaultMaxAttempts  = 12
	maxResponseBytes    = 4096
	maxRetryDelay       = 24 * time.Hour
)

type Worker struct {
	Repository      *repository.Repository
	Cipher          *functionsecret.Cipher
	WorkerID        string
	HTTPClient      *http.Client
	PollInterval    time.Duration
	LeaseAge        time.Duration
	DeliveryTimeout time.Duration
	MaxAttempts     int
	Logger          *slog.Logger
}

func New(repo *repository.Repository, cipher *functionsecret.Cipher, workerID string, logger *slog.Logger) (*Worker, error) {
	if repo == nil || cipher == nil || strings.TrimSpace(workerID) == "" {
		return nil, errors.New("invalid webhook worker dependencies")
	}
	if len(workerID) > 128 || !validWorkerID(workerID) {
		return nil, errors.New("invalid webhook worker id")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		Repository:      repo,
		Cipher:          cipher,
		WorkerID:        workerID,
		HTTPClient:      newSafeHTTPClient(defaultTimeout),
		PollInterval:    defaultPollInterval,
		LeaseAge:        defaultLeaseAge,
		DeliveryTimeout: defaultTimeout,
		MaxAttempts:     defaultMaxAttempts,
		Logger:          logger,
	}, nil
}

// Run polls until the process is cancelled. Stale leases and expired events
// are repaired on every cycle, so a killed worker cannot permanently strand a
// delivery.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Repository == nil || w.Cipher == nil {
		return errors.New("webhook worker is not configured")
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
	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()
	if _, err := w.Repository.PruneExpiredWebhookEvents(ctx); err != nil && !errors.Is(err, context.Canceled) {
		w.Logger.Error("prune expired webhook events failed", "error", err)
	}
	for {
		select {
		case <-pruneTicker.C:
			if _, err := w.Repository.PruneExpiredWebhookEvents(ctx); err != nil && !errors.Is(err, context.Canceled) {
				w.Logger.Error("prune expired webhook events failed", "error", err)
			}
		default:
		}
		if _, err := w.Repository.RequeueStaleWebhookDeliveries(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) {
			w.Logger.Error("requeue stale webhook deliveries failed", "error", err)
		}
		if _, err := w.Repository.ExpireWebhookDeliveries(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.Logger.Error("expire webhook deliveries failed", "error", err)
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			w.Logger.Error("webhook delivery failed", "error", err)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce claims and processes at most one delivery. It returns false when no
// due delivery exists, allowing callers to back off without busy-spinning.
func (w *Worker) RunOnce(ctx context.Context) (processed bool, runErr error) {
	if w == nil || w.Repository == nil || w.Cipher == nil {
		return false, errors.New("webhook worker is not configured")
	}
	job, err := w.Repository.ClaimNextWebhookDelivery(ctx, w.WorkerID)
	if errors.Is(err, repository.ErrNoWebhookDelivery) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) && w.Logger != nil {
			w.Logger.Error("webhook delivery failed", "delivery_id", job.DeliveryID, "webhook_id", job.WebhookID, "event_id", job.EventID, "project_id", job.ProjectID, "error", runErr)
		}
	}()
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	if job.AttemptCount > maxAttempts {
		finishErr := w.Repository.FinishWebhookDelivery(ctx, job.DeliveryID, w.WorkerID, false, nil, "maximum delivery attempts exceeded", nil)
		return true, finishErr
	}
	secret, err := w.Cipher.Decrypt(job.SecretCiphertext)
	if err != nil || len(secret) == 0 {
		finishErr := w.Repository.FinishWebhookDelivery(ctx, job.DeliveryID, w.WorkerID, false, nil, "webhook secret could not be decrypted", nil)
		if finishErr != nil {
			return true, finishErr
		}
		return true, nil
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	timeout := w.DeliveryTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(deliveryCtx, http.MethodPost, job.URL, strings.NewReader(string(job.EventPayload)))
	if err != nil {
		finishErr := w.Repository.FinishWebhookDelivery(ctx, job.DeliveryID, w.WorkerID, false, nil, "webhook URL could not be prepared", nil)
		return true, finishErr
	}
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(job.EventPayload)
	signature := hex.EncodeToString(mac.Sum(nil))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "stealth-webhook/1.0")
	request.Header.Set("X-Stealth-Webhook-ID", job.WebhookID.String())
	request.Header.Set("X-Stealth-Delivery", job.DeliveryID.String())
	request.Header.Set("X-Stealth-Event", job.EventName)
	request.Header.Set("X-Stealth-Timestamp", timestamp)
	request.Header.Set("X-Stealth-Signature", "v1="+signature)
	client := w.HTTPClient
	if client == nil {
		client = newSafeHTTPClient(timeout)
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		retryAt := w.retryAt(job.AttemptCount, 0)
		if job.AttemptCount >= maxAttempts {
			retryAt = time.Time{}
		}
		finishErr := w.Repository.FinishWebhookDelivery(ctx, job.DeliveryID, w.WorkerID, false, nil, safeError(err), nullableTime(retryAt))
		return true, finishErr
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	statusCode := response.StatusCode
	if statusCode >= 200 && statusCode <= 299 {
		return true, w.Repository.FinishWebhookDelivery(ctx, job.DeliveryID, w.WorkerID, true, &statusCode, "", nil)
	}
	errorText := fmt.Sprintf("remote endpoint returned HTTP %d", statusCode)
	if text := strings.TrimSpace(string(body)); text != "" {
		if len(text) > maxResponseBytes {
			text = text[:maxResponseBytes]
		}
		errorText += ": " + text
	}
	if retryableStatus(statusCode) && job.AttemptCount < maxAttempts {
		retryAt := w.retryAt(job.AttemptCount, parseRetryAfter(response.Header.Get("Retry-After")))
		return true, w.Repository.FinishWebhookDelivery(ctx, job.DeliveryID, w.WorkerID, false, &statusCode, errorText, nullableTime(retryAt))
	}
	return true, w.Repository.FinishWebhookDelivery(ctx, job.DeliveryID, w.WorkerID, false, &statusCode, errorText, nil)
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func (w *Worker) retryAt(attempt int, retryAfter time.Duration) time.Time {
	if retryAfter > 0 {
		if retryAfter > maxRetryDelay {
			retryAfter = maxRetryDelay
		}
		return time.Now().UTC().Add(retryAfter)
	}
	if attempt < 1 {
		attempt = 1
	}
	return time.Now().UTC().Add(retry.Exponential(attempt, 30*time.Second, maxRetryDelay))
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	if seconds > int64(maxRetryDelay/time.Second) {
		seconds = int64(maxRetryDelay / time.Second)
	}
	return time.Duration(seconds) * time.Second
}

func safeError(err error) string {
	if err == nil {
		return "delivery failed"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "delivery failed"
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	return message
}

func validWorkerID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || (index > 0 && (char == '-' || char == '_' || char == '.')) {
			continue
		}
		return false
	}
	return true
}

func newSafeHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	transport.DialContext = safeDialContext
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errors.New("invalid webhook destination")
	}
	if parsedPort, parseErr := strconv.Atoi(port); parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
		return nil, errors.New("invalid webhook destination port")
	}
	addresses := make([]netip.Addr, 0, 4)
	if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
		addresses = append(addresses, literal)
	} else {
		resolved, lookupErr := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if lookupErr != nil {
			return nil, fmt.Errorf("resolve webhook destination: %w", lookupErr)
		}
		addresses = append(addresses, resolved...)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, addressIP := range addresses {
		addressIP = addressIP.Unmap()
		if blockedAddress(addressIP) {
			lastErr = errors.New("webhook destination resolves to a private or local address")
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addressIP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("webhook destination has no usable address")
	}
	return nil, lastErr
}

func blockedAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return true
	}
	return address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified()
}
