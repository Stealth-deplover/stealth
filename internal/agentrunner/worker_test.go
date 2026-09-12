package agentrunner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

func TestRegistryNormalizesFixedProviderIdentity(t *testing.T) {
	registry := NewRegistry()
	adapter := AdapterFunc(func(context.Context, Job) (repository.AgentRunResult, error) {
		return repository.AgentRunResult{Status: "completed"}, nil
	})
	if err := registry.Register(" OpenAI ", adapter); err != nil {
		t.Fatal(err)
	}
	if registry.Resolve("openai") == nil || registry.Resolve(" OPENAI ") == nil {
		t.Fatal("normalized provider was not resolved")
	}
	if got := registry.Providers(); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("providers = %#v", got)
	}
	if err := registry.Register("openai\n", adapter); !errors.Is(err, ErrInvalidAdapter) {
		t.Fatalf("control character returned %v", err)
	}
	if err := registry.Register("anthropic", nil); !errors.Is(err, ErrInvalidAdapter) {
		t.Fatalf("nil adapter returned %v", err)
	}
}

func TestRegistryProvidersAreSorted(t *testing.T) {
	registry := NewRegistry()
	adapter := AdapterFunc(func(context.Context, Job) (repository.AgentRunResult, error) {
		return repository.AgentRunResult{Status: "completed"}, nil
	})
	for _, provider := range []string{"zeta", "openai", "anthropic"} {
		if err := registry.Register(provider, adapter); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(registry.Providers(), ",")
	if got != "anthropic,openai,zeta" {
		t.Fatalf("providers = %q", got)
	}
}

func TestWorkerDoesNotClaimWithoutProviderAdapter(t *testing.T) {
	worker, err := New(&repository.Repository{}, "agent-worker-1", NewRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("RunOnce() = processed=%v err=%v, want false/nil", processed, err)
	}
}

func TestWorkerConstructionAndPublicFailureBounds(t *testing.T) {
	for _, workerID := range []string{"", "agent worker", "../agent"} {
		if _, err := New(&repository.Repository{}, workerID, NewRegistry(), nil); !errors.Is(err, ErrInvalidWorker) {
			t.Errorf("worker ID %q returned %v", workerID, err)
		}
	}
	message := publicFailure(&PublicError{Message: strings.Repeat("x", 5000)})
	if len([]rune(message)) != maxPublicError {
		t.Fatalf("publicFailure length = %d, want %d", len([]rune(message)), maxPublicError)
	}
	if got := publicFailure(nil); got != "agent provider execution failed" {
		t.Fatalf("publicFailure(nil) = %q", got)
	}
	if got := publicFailure(errors.New("provider secret should not be persisted")); got != "agent provider execution failed" {
		t.Fatalf("arbitrary provider error = %q", got)
	}
}

type fakeAgentPersistence struct {
	job          repository.AgentRunJob
	transitioned int
	logs         int
}

func (f *fakeAgentPersistence) RequeueStaleAgentRuns(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (f *fakeAgentPersistence) ClaimNextAgentRunForProviders(context.Context, string, []string) (repository.AgentRunJob, error) {
	return f.job, nil
}

func (f *fakeAgentPersistence) TransitionAgentRun(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ string, result repository.AgentRunResult) (domain.AgentRun, error) {
	f.transitioned++
	return domain.AgentRun{Status: result.Status}, nil
}

func (f *fakeAgentPersistence) AppendAgentRunLog(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, uuid.UUID, int64, string, string) (domain.AgentRunLog, error) {
	f.logs++
	return domain.AgentRunLog{}, nil
}

func TestWorkerUsesPersistenceSeamToPersistProviderResult(t *testing.T) {
	runID := uuid.New()
	agentID := uuid.New()
	projectID := uuid.New()
	store := &fakeAgentPersistence{job: repository.AgentRunJob{
		Run:   domain.AgentRun{ID: runID.String(), AgentID: agentID.String(), ProjectID: projectID.String()},
		Agent: domain.Agent{Provider: "openai"},
	}}
	registry := NewRegistry()
	if err := registry.Register("openai", AdapterFunc(func(context.Context, Job) (repository.AgentRunResult, error) {
		return repository.AgentRunResult{Status: "completed"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	worker, err := New(store, "agent-worker-1", registry, nil)
	if err != nil {
		t.Fatal(err)
	}

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = processed=%v err=%v, want true/nil", processed, err)
	}
	if store.transitioned != 1 || store.logs != 2 {
		t.Fatalf("persistence calls = transitioned=%d logs=%d, want 1/2", store.transitioned, store.logs)
	}
}
