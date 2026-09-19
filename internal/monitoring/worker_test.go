package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type fakeMonitoringPersistence struct {
	job         repository.AdminMonitorJob
	claimed     int
	completed   int
	lastResult  repository.AdminMonitorCheckInput
	completeErr error
}

func (f *fakeMonitoringPersistence) RequeueStaleAdminMonitors(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (f *fakeMonitoringPersistence) ClaimNextAdminMonitor(context.Context, string, time.Duration) (repository.AdminMonitorJob, error) {
	if f.claimed > 0 {
		return repository.AdminMonitorJob{}, repository.ErrNoAdminMonitor
	}
	f.claimed++
	return f.job, nil
}

func (f *fakeMonitoringPersistence) CompleteAdminMonitorCheck(_ context.Context, _ uuid.UUID, _ string, result repository.AdminMonitorCheckInput) error {
	f.completed++
	f.lastResult = result
	return f.completeErr
}

func TestWorkerProcessesOneClaimedMonitorWithoutDuplicateInvocation(t *testing.T) {
	cipher, err := functionsecret.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{
		"token_hash":    strings.Repeat("0", 64),
		"grace_seconds": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt(config)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &fakeMonitoringPersistence{job: repository.AdminMonitorJob{
		ID:              uuid.Must(uuid.NewV7()),
		Kind:            "heartbeat",
		Target:          "backup",
		IntervalSeconds: 60,
		TimeoutMS:       1000,
		EncryptedConfig: encrypted,
		LastHeartbeatAt: timePtr(time.Now().UTC()),
	}}
	worker, err := NewWorker(persistence, cipher, "worker-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background(), time.Minute)
	if err != nil || !processed {
		t.Fatalf("RunOnce() = processed=%v err=%v", processed, err)
	}
	if persistence.claimed != 1 || persistence.completed != 1 {
		t.Fatalf("claim/completion counts = %d/%d", persistence.claimed, persistence.completed)
	}
	processed, err = worker.RunOnce(context.Background(), time.Minute)
	if err != nil || processed {
		t.Fatalf("second RunOnce() = processed=%v err=%v, want idle", processed, err)
	}
}

func TestWorkerDoesNotHotLoopAfterCompletionPersistenceFailure(t *testing.T) {
	cipher, err := functionsecret.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{
		"token_hash":    strings.Repeat("0", 64),
		"grace_seconds": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt(config)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &fakeMonitoringPersistence{
		completeErr: errors.New("database temporarily unavailable"),
		job: repository.AdminMonitorJob{
			ID:              uuid.Must(uuid.NewV7()),
			Kind:            "heartbeat",
			Target:          "backup",
			IntervalSeconds: 60,
			TimeoutMS:       1000,
			EncryptedConfig: encrypted,
			LastHeartbeatAt: timePtr(time.Now().UTC()),
		},
	}
	worker, err := NewWorker(persistence, cipher, "worker-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	worker.PollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if persistence.claimed != 1 {
		t.Fatalf("claimed=%d, want one claim after completion persistence failure", persistence.claimed)
	}
}
