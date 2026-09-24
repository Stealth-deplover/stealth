package repository

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRuntimeCleanupAcceptsPersistedContainerNameForms(t *testing.T) {
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	containerID := strings.Repeat("a", 64)
	name := AppRuntimeContainerName(appID)
	for _, candidate := range []string{name, "/" + name} {
		if !validCleanupTarget(appID, containerID, candidate) {
			t.Errorf("valid persisted Docker name %q was rejected", candidate)
		}
	}
	if !validCleanupTarget(appID, "", "/"+name) {
		t.Fatal("deterministic name without a stored container ID was rejected")
	}
}

func TestRuntimeCleanupRejectsUnsafeContainerTargets(t *testing.T) {
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	containerID := strings.Repeat("a", 64)
	for _, name := range []string{"", "/", "/two/names", "../escape", "/with space"} {
		if validCleanupTarget(appID, containerID, name) {
			t.Errorf("unsafe cleanup target %q was accepted", name)
		}
	}
	if validCleanupTarget(appID, "bad-id", AppRuntimeContainerName(appID)) {
		t.Fatal("malformed container ID was accepted")
	}
}
