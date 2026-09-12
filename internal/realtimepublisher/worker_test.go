package realtimepublisher

import (
	"context"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/realtime"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type fakePersistence struct {
	job       repository.RealtimePublishJob
	finished  bool
	succeeded bool
}

func (f *fakePersistence) RequeueStaleRealtimeEvents(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (f *fakePersistence) PendingRealtimeEvents(context.Context) (int64, error) {
	return 0, nil
}

func (f *fakePersistence) ClaimNextRealtimeEvent(context.Context, string, time.Duration) (repository.RealtimePublishJob, error) {
	return f.job, nil
}

func (f *fakePersistence) FinishRealtimeEvent(_ context.Context, _ uuid.UUID, _ string, success bool, _ *time.Time, _ string) error {
	f.finished = true
	f.succeeded = success
	return nil
}

func TestRunOnceUsesDurablePersistenceSeam(t *testing.T) {
	store := &fakePersistence{job: repository.RealtimePublishJob{
		EventID:      uuid.New(),
		ProjectID:    uuid.New(),
		EventName:    "audit.recorded",
		Payload:      []byte(`{"event":"audit.recorded"}`),
		AttemptCount: 1,
	}}
	worker := &Worker{Store: store, Broker: &realtime.Broker{}, WorkerID: "realtime-worker"}

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = processed=%v err=%v, want true/nil", processed, err)
	}
	if !store.finished || !store.succeeded {
		t.Fatalf("persistence finish = finished=%v succeeded=%v, want true/true", store.finished, store.succeeded)
	}
}
