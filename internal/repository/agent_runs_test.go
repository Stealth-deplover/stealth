package repository

import (
	"errors"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/domain"
)

func TestNormalizeAgentRunPrompt(t *testing.T) {
	if got, err := normalizeAgentRunPrompt("  inspect the repository  "); err != nil || got != "inspect the repository" {
		t.Fatalf("normalizeAgentRunPrompt() = %q, %v", got, err)
	}
	for _, prompt := range []string{"", "   ", "bad\x00prompt", strings.Repeat("x", agentRunMaxPrompt+1)} {
		if _, err := normalizeAgentRunPrompt(prompt); !errors.Is(err, ErrInvalidAgentRun) {
			t.Fatalf("normalizeAgentRunPrompt(%q) error = %v; want ErrInvalidAgentRun", prompt, err)
		}
	}
}

func TestNormalizeAgentRunResultUsesArraysAndValidatesWorkerOutput(t *testing.T) {
	result, steps, changes, err := normalizeAgentRunResult(AgentRunResult{Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Steps == nil || result.Changes == nil || string(steps) != "[]" || string(changes) != "[]" {
		t.Fatalf("empty result = %#v, %s, %s", result, steps, changes)
	}
	_, _, _, err = normalizeAgentRunResult(AgentRunResult{Status: "running"})
	if !errors.Is(err, ErrInvalidAgentRunTransition) {
		t.Fatalf("non-terminal status error = %v; want ErrInvalidAgentRunTransition", err)
	}
	_, _, _, err = normalizeAgentRunResult(AgentRunResult{
		Status: "completed",
		Steps:  []domain.AgentRunStep{{ID: "step", Type: "unsupported", Label: "bad", Target: "x", Status: "done"}},
	})
	if !errors.Is(err, ErrInvalidAgentRun) {
		t.Fatalf("invalid step error = %v; want ErrInvalidAgentRun", err)
	}
}

func TestNormalizeAgentRunWorkerID(t *testing.T) {
	if got, err := normalizeAgentRunWorkerID(" worker-1 "); err != nil || got != "worker-1" {
		t.Fatalf("normalizeAgentRunWorkerID() = %q, %v", got, err)
	}
	if _, err := normalizeAgentRunWorkerID(strings.Repeat("w", agentRunMaxWorkerID+1)); !errors.Is(err, ErrInvalidAgentRun) {
		t.Fatalf("oversized worker id error = %v; want ErrInvalidAgentRun", err)
	}
}
