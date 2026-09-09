package repository

import "testing"

func TestFunctionDeploymentTransitionsAreExplicit(t *testing.T) {
	tests := []struct {
		current string
		next    string
		allowed bool
	}{
		{current: "queued", next: "building", allowed: true},
		{current: "queued", next: "failed", allowed: true},
		{current: "building", next: "ready", allowed: true},
		{current: "building", next: "cancelled", allowed: true},
		{current: "ready", next: "active", allowed: true},
		{current: "active", next: "superseded", allowed: true},
		{current: "active", next: "ready", allowed: false},
		{current: "failed", next: "queued", allowed: false},
		{current: "cancelled", next: "active", allowed: false},
		{current: "unknown", next: "building", allowed: false},
	}
	for _, test := range tests {
		if got := validFunctionDeploymentTransition(test.current, test.next); got != test.allowed {
			t.Errorf("transition %q -> %q = %v, want %v", test.current, test.next, got, test.allowed)
		}
	}
}

func TestFunctionExecutionTransitionsAreExplicit(t *testing.T) {
	tests := []struct {
		current string
		next    string
		allowed bool
	}{
		{current: "accepted", next: "running", allowed: true},
		{current: "accepted", next: "failed", allowed: true},
		{current: "running", next: "succeeded", allowed: true},
		{current: "running", next: "cancelled", allowed: true},
		{current: "succeeded", next: "failed", allowed: false},
		{current: "failed", next: "running", allowed: false},
		{current: "cancelled", next: "accepted", allowed: false},
	}
	for _, test := range tests {
		if got := validFunctionExecutionTransition(test.current, test.next); got != test.allowed {
			t.Errorf("transition %q -> %q = %v, want %v", test.current, test.next, got, test.allowed)
		}
	}
}
