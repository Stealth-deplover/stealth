// Package messagingrunner delivers encrypted project messages from PostgreSQL.
// It is deliberately separate from the public API process: provider network
// calls, retries, and recipient decryption happen only in this trusted worker.
package messagingrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nazxf/stealth-api/internal/functionsecret"
	"github.com/nazxf/stealth-api/internal/mailer"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/nazxf/stealth-api/internal/retry"
)

const (
	defaultPollInterval = 500 * time.Millisecond
	defaultLeaseAge     = 20 * time.Minute
	defaultTimeout      = 35 * time.Second
	defaultMaxAttempts  = 12
	maxRetryDelay       = 24 * time.Hour
	maxProviderResponse = 4096
)

// Message is the decrypted message payload passed to one provider adapter.
// Recipient is used only inside the worker and must never be logged.
type Message struct {
	Channel          string
	Recipient        string
	RecipientPreview string
	Subject          string
	Body             string
	Data             map[string]string
}

// Provider is the decrypted provider configuration passed to an adapter.
// Credentials are process-local and are never part of a domain/API response.
type Provider struct {
	Channel     string
	Name        string
	Enabled     bool
	Credentials map[string]string
}

// SendError lets an adapter distinguish transient provider/network failures
// from permanent configuration or validation failures without exposing a raw
// response body to the API.
type SendError struct {
	Retryable  bool
	StatusCode int
	Message    string
}

func (e *SendError) Error() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "messaging delivery failed"
	}
	return e.Message
}

// Adapter sends one recipient through one configured provider.
type Adapter interface {
	Send(context.Context, Provider, Message) error
}

// AdapterFunc makes deterministic adapters easy to inject in tests.
type AdapterFunc func(context.Context, Provider, Message) error

func (f AdapterFunc) Send(ctx context.Context, provider Provider, message Message) error {
	return f(ctx, provider, message)
}

// Registry resolves adapters by channel and provider name. Keys are fixed
// provider identifiers, so a tenant cannot select an arbitrary network URL.
type Registry struct {
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

func (r *Registry) Register(channel, provider string, adapter Adapter) error {
	if r == nil || adapter == nil {
		return errors.New("messaging adapter registry is not configured")
	}
	if strings.ContainsAny(channel+provider, "\x00\r\n") {
		return errors.New("messaging adapter identity is invalid")
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	provider = strings.ToLower(strings.TrimSpace(provider))
	if channel == "" || provider == "" {
		return errors.New("messaging adapter identity is invalid")
	}
	if r.adapters == nil {
		r.adapters = make(map[string]Adapter)
	}
	r.adapters[adapterKey(channel, provider)] = adapter
	return nil
}

func (r *Registry) Resolve(channel, provider string) Adapter {
	if r == nil {
		return nil
	}
	if strings.ContainsAny(channel+provider, "\x00\r\n") {
		return nil
	}
	return r.adapters[adapterKey(strings.ToLower(strings.TrimSpace(channel)), strings.ToLower(strings.TrimSpace(provider)))]
}

func adapterKey(channel, provider string) string { return channel + "\x00" + provider }

// NewDefaultRegistry enables explicit, well-known adapters. "log" is useful
// for local verification and never logs the plaintext recipient or body.
func NewDefaultRegistry(logger *slog.Logger, client *http.Client) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	if client == nil {
		client = newSafeHTTPClient(defaultTimeout)
	}
	registry := NewRegistry()
	_ = registry.Register("email", "smtp", SMTPAdapter{Timeout: defaultTimeout})
	_ = registry.Register("email", "log", LogAdapter{Logger: logger})
	_ = registry.Register("sms", "twilio", TwilioAdapter{Client: client})
	_ = registry.Register("sms", "log", LogAdapter{Logger: logger})
	_ = registry.Register("push", "log", LogAdapter{Logger: logger})
	return registry
}

type Worker struct {
	Repository      *repository.Repository
	Cipher          *functionsecret.Cipher
	WorkerID        string
	Adapters        *Registry
	PollInterval    time.Duration
	LeaseAge        time.Duration
	DeliveryTimeout time.Duration
	MaxAttempts     int
	Logger          *slog.Logger
}

func New(repo *repository.Repository, cipher *functionsecret.Cipher, workerID string, logger *slog.Logger) (*Worker, error) {
	if repo == nil || cipher == nil || !validWorkerID(workerID) {
		return nil, errors.New("invalid messaging worker dependencies")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		Repository:      repo,
		Cipher:          cipher,
		WorkerID:        workerID,
		Adapters:        NewDefaultRegistry(logger, nil),
		PollInterval:    defaultPollInterval,
		LeaseAge:        defaultLeaseAge,
		DeliveryTimeout: defaultTimeout,
		MaxAttempts:     defaultMaxAttempts,
		Logger:          logger,
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Repository == nil || w.Cipher == nil || w.Adapters == nil {
		return errors.New("messaging worker is not configured")
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
		if _, err := w.Repository.RequeueStaleMessagingDeliveries(ctx, leaseAge); err != nil && !errors.Is(err, context.Canceled) {
			w.Logger.Error("requeue stale messaging deliveries failed", "error", err)
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			w.Logger.Error("messaging delivery failed", "error", err)
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

func (w *Worker) RunOnce(ctx context.Context) (processed bool, runErr error) {
	if w == nil || w.Repository == nil || w.Cipher == nil || w.Adapters == nil {
		return false, errors.New("messaging worker is not configured")
	}
	job, err := w.Repository.ClaimNextMessagingDelivery(ctx, w.WorkerID)
	if errors.Is(err, repository.ErrNoMessagingDelivery) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) && w.Logger != nil {
			w.Logger.Error("messaging delivery failed", "delivery_id", job.DeliveryID, "message_id", job.MessageID, "subscriber_id", job.SubscriberID, "project_id", job.ProjectID, "error", runErr)
		}
	}()
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	if job.AttemptCount > maxAttempts {
		return true, w.Repository.FinishMessagingDelivery(ctx, job.DeliveryID, w.WorkerID, false, nil, "maximum delivery attempts exceeded", nil)
	}
	provider, err := w.Repository.MessagingProviderCredentialsForDelivery(ctx, job)
	if err != nil {
		return true, w.finishFailure(ctx, job, maxAttempts, err, false, 0)
	}
	if !provider.Enabled {
		return true, w.finishFailure(ctx, job, maxAttempts, errors.New("messaging provider is disabled"), true, 0)
	}
	address, err := w.Repository.MessagingDeliveryAddress(ctx, job)
	if err != nil {
		return true, w.finishFailure(ctx, job, maxAttempts, err, false, 0)
	}
	payload, err := w.Repository.MessagingDeliveryPayload(ctx, job)
	if err != nil {
		return true, w.finishFailure(ctx, job, maxAttempts, err, false, 0)
	}
	adapter := w.Adapters.Resolve(provider.Channel, provider.Provider)
	if adapter == nil {
		return true, w.finishFailure(ctx, job, maxAttempts, fmt.Errorf("no adapter registered for %s/%s", provider.Channel, provider.Provider), false, 0)
	}
	deliveryTimeout := w.DeliveryTimeout
	if deliveryTimeout <= 0 {
		deliveryTimeout = defaultTimeout
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	message := Message{Channel: job.Channel, Recipient: address, RecipientPreview: job.AddressPreview, Subject: payload.Subject, Body: payload.Body, Data: payload.Data}
	err = adapter.Send(deliveryCtx, providerAdapterInput(provider), message)
	if err == nil {
		return true, w.Repository.FinishMessagingDelivery(ctx, job.DeliveryID, w.WorkerID, true, nil, "", nil)
	}
	var sendErr *SendError
	if errors.As(err, &sendErr) {
		return true, w.finishFailure(ctx, job, maxAttempts, sendErr, sendErr.Retryable, sendErr.StatusCode)
	}
	return true, w.finishFailure(ctx, job, maxAttempts, err, true, 0)
}

func providerAdapterInput(provider repository.MessagingProviderCredentials) Provider {
	return Provider{Channel: provider.Channel, Name: provider.Provider, Enabled: provider.Enabled, Credentials: provider.Values}
}

func (w *Worker) finishFailure(ctx context.Context, job repository.MessagingDeliveryJob, maxAttempts int, reason error, retryable bool, statusCode int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	errorText := safeError(reason)
	var retryAt *time.Time
	if retryable && job.AttemptCount < maxAttempts {
		next := w.retryAt(job.AttemptCount)
		retryAt = &next
	}
	var code *int
	if statusCode >= 100 && statusCode <= 599 {
		code = &statusCode
	}
	return w.Repository.FinishMessagingDelivery(ctx, job.DeliveryID, w.WorkerID, false, code, errorText, retryAt)
}

func (w *Worker) retryAt(attempt int) time.Time {
	if attempt < 1 {
		attempt = 1
	}
	return time.Now().UTC().Add(retry.Exponential(attempt, 30*time.Second, maxRetryDelay))
}

type LogAdapter struct{ Logger *slog.Logger }

func (a LogAdapter) Send(_ context.Context, provider Provider, message Message) error {
	logger := a.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("messaging delivery in log mode", "channel", provider.Channel, "provider", provider.Name, "recipient_preview", message.RecipientPreview, "subject", message.Subject, "body_bytes", len(message.Body))
	return nil
}

type SMTPAdapter struct{ Timeout time.Duration }

func (a SMTPAdapter) Send(ctx context.Context, provider Provider, message Message) error {
	if provider.Channel != "email" {
		return &SendError{Message: "smtp adapter only supports email"}
	}
	credentials := provider.Credentials
	host := strings.TrimSpace(credentials["host"])
	from := strings.TrimSpace(credentials["from"])
	if host == "" || from == "" {
		return &SendError{Message: "smtp provider requires host and from"}
	}
	port := 587
	if raw := strings.TrimSpace(credentials["port"]); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return &SendError{Message: "smtp provider port is invalid"}
		}
		port = parsed
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	sender := &mailer.SMTP{Host: host, Port: port, Username: credentials["username"], Password: credentials["password"], From: from, Timeout: timeout, DialContext: restrictedDialContext}
	if err := sender.Send(ctx, mailer.Message{To: message.Recipient, Subject: message.Subject, TextBody: message.Body}); err != nil {
		return &SendError{Retryable: true, Message: safeError(err)}
	}
	return nil
}

type TwilioAdapter struct{ Client *http.Client }

func (a TwilioAdapter) Send(ctx context.Context, provider Provider, message Message) error {
	if provider.Channel != "sms" {
		return &SendError{Message: "twilio adapter only supports sms"}
	}
	credentials := provider.Credentials
	accountSID := strings.TrimSpace(credentials["account_sid"])
	authToken := credentials["auth_token"]
	from := strings.TrimSpace(credentials["from"])
	serviceSID := strings.TrimSpace(credentials["messaging_service_sid"])
	if !validTwilioToken(accountSID) || strings.TrimSpace(authToken) == "" || strings.ContainsAny(authToken, "\x00\r\n") || (from == "" && serviceSID == "") {
		return &SendError{Message: "twilio provider requires account_sid, auth_token, and from or messaging_service_sid"}
	}
	form := url.Values{"To": {message.Recipient}, "Body": {message.Body}}
	if serviceSID != "" {
		form.Set("MessagingServiceSid", serviceSID)
	} else {
		form.Set("From", from)
	}
	endpoint := "https://api.twilio.com/2010-04-01/Accounts/" + url.PathEscape(accountSID) + "/Messages.json"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return &SendError{Message: "twilio request could not be prepared"}
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "stealth-messaging/1.0")
	request.SetBasicAuth(accountSID, authToken)
	client := a.Client
	if client == nil {
		client = newSafeHTTPClient(defaultTimeout)
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return &SendError{Retryable: true, Message: safeError(err)}
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxProviderResponse))
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		return nil
	}
	messageText := fmt.Sprintf("twilio returned HTTP %d", response.StatusCode)
	if text := strings.TrimSpace(string(body)); text != "" {
		messageText += ": " + safeError(errors.New(text))
	}
	return &SendError{Retryable: retryableStatus(response.StatusCode), StatusCode: response.StatusCode, Message: messageText}
}

func validTwilioToken(value string) bool {
	if len(value) < 6 || len(value) > 64 {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

func safeError(err error) string {
	if err == nil {
		return "messaging delivery failed"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "messaging delivery failed"
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	return message
}

func validWorkerID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
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
	return &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

// restrictedDialContext rejects private and link-local SMTP destinations and
// dials the resolved IP directly, preventing DNS rebinding to platform-local
// services during a provider send.
func restrictedDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("smtp address is invalid")
	}
	ips, err := resolvePublicIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: defaultTimeout}
	var lastErr error
	for _, ip := range ips {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("smtp host has no reachable address")
	}
	return nil, lastErr
}

func resolvePublicIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if !publicIP(literal) {
			return nil, errors.New("smtp host resolves to a private address")
		}
		return []netip.Addr{literal}, nil
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve smtp host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("smtp host has no address")
	}
	for _, address := range addresses {
		if !publicIP(address) {
			return nil, errors.New("smtp host resolves to a private address")
		}
	}
	return addresses, nil
}

func publicIP(address netip.Addr) bool {
	return address.IsValid() && !address.IsLoopback() && !address.IsPrivate() && !address.IsLinkLocalUnicast() && !address.IsLinkLocalMulticast() && !address.IsMulticast() && !address.IsUnspecified()
}
