package adminnotification

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/mailer"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type fakePersistence struct {
	job       repository.AdminNotificationDeliveryJob
	claimed   bool
	success   bool
	lastError string
	retryAt   *time.Time
}

func (f *fakePersistence) RequeueStaleAdminNotificationDeliveries(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (f *fakePersistence) ClaimNextAdminNotificationDelivery(context.Context, string, time.Duration) (repository.AdminNotificationDeliveryJob, error) {
	if f.claimed {
		return repository.AdminNotificationDeliveryJob{}, repository.ErrNoAdminNotification
	}
	f.claimed = true
	return f.job, nil
}

func (f *fakePersistence) FinishAdminNotificationDelivery(_ context.Context, _ uuid.UUID, _ string, success bool, lastError string, retryAt *time.Time) error {
	f.success = success
	f.lastError = lastError
	f.retryAt = retryAt
	return nil
}

type fakeSender struct {
	err     error
	message mailer.Message
}

func (f *fakeSender) Send(_ context.Context, message mailer.Message) error {
	f.message = message
	return f.err
}

func newTestWorker(t *testing.T, sender mailer.Sender, persistence *fakePersistence) *Worker {
	t.Helper()
	cipher, err := functionsecret.New(bytesOfLength(functionsecret.KeySize, 7))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := New(persistence, cipher, sender, "worker-test", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestRunOnceDeliversEmailWithoutPersistingSecrets(t *testing.T) {
	persistence := &fakePersistence{job: repository.AdminNotificationDeliveryJob{
		DeliveryID: uuid.New(), ChannelName: "owner email", Kind: "email", RuleName: "API failure", Severity: "critical", State: "firing", Message: "API is unavailable", OccurredAt: time.Now().UTC(), Attempts: 1,
	}}
	cipher, err := functionsecret.New(bytesOfLength(functionsecret.KeySize, 9))
	if err != nil {
		t.Fatal(err)
	}
	config, err := cipher.Encrypt([]byte(`{"recipient":"owner@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	persistence.job.ConfigEncrypted = config
	sender := &fakeSender{}
	worker := newTestWorker(t, sender, persistence)
	worker.Cipher = cipher

	processed, err := worker.RunOnce(context.Background(), time.Minute)
	if err != nil || !processed || !persistence.success {
		t.Fatalf("processed=%v err=%v success=%v", processed, err, persistence.success)
	}
	if sender.message.To != "owner@example.com" || sender.message.TextBody == "" {
		t.Fatalf("message = %+v", sender.message)
	}
}

func TestRunOnceRetriesTransientWebhookFailureWithBoundedDelay(t *testing.T) {
	persistence := &fakePersistence{job: repository.AdminNotificationDeliveryJob{
		DeliveryID: uuid.New(), Kind: "webhook", RuleName: "API failure", Severity: "warning", State: "firing", Message: "request failed", OccurredAt: time.Now().UTC(), Attempts: 1,
	}}
	cipher, err := functionsecret.New(bytesOfLength(functionsecret.KeySize, 8))
	if err != nil {
		t.Fatal(err)
	}
	config, err := cipher.Encrypt([]byte(`{"url":"https://example.com/hooks/test"}`))
	if err != nil {
		t.Fatal(err)
	}
	persistence.job.ConfigEncrypted = config
	worker := newTestWorker(t, mailer.DisabledSender{}, persistence)
	worker.Cipher = cipher
	worker.HTTPClient = &http.Client{Transport: httpRoundTripperError{}}

	processed, err := worker.RunOnce(context.Background(), time.Minute)
	if err != nil || !processed || persistence.success {
		t.Fatalf("processed=%v err=%v success=%v", processed, err, persistence.success)
	}
	if persistence.retryAt == nil || !persistence.retryAt.After(time.Now().UTC()) {
		t.Fatalf("retryAt = %v", persistence.retryAt)
	}
	if persistence.lastError != "notification request failed" {
		t.Fatalf("lastError = %q", persistence.lastError)
	}
}

type httpRoundTripperError struct{}

func (httpRoundTripperError) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("request failed password=do-not-return")
}

func bytesOfLength(length int, value byte) []byte {
	output := make([]byte, length)
	for index := range output {
		output[index] = value
	}
	return output
}
