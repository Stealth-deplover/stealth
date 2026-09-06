package agentrunner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nazxf/stealth-api/internal/repository"
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
