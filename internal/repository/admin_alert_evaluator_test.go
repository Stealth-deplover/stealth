package repository

import (
	"testing"
	"time"
)

func TestAdminAlertTransitionHonorsForDuration(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	first := transitionAdminAlert("normal", nil, now, true, 60)
	if first.State != "pending" || first.PendingSince == nil || first.EventState != "" {
		t.Fatalf("first transition = %+v", first)
	}
	second := transitionAdminAlert("pending", first.PendingSince, now.Add(30*time.Second), true, 60)
	if second.State != "pending" || second.EventState != "" {
		t.Fatalf("early transition = %+v", second)
	}
	third := transitionAdminAlert("pending", first.PendingSince, now.Add(60*time.Second), true, 60)
	if third.State != "firing" || third.EventState != "firing" {
		t.Fatalf("firing transition = %+v", third)
	}
}

func TestAdminAlertTransitionResolvesWithoutDuplicateEvent(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	resolved := transitionAdminAlert("firing", nil, now, false, 0)
	if resolved.State != "resolved" || resolved.EventState != "resolved" {
		t.Fatalf("resolve transition = %+v", resolved)
	}
	normal := transitionAdminAlert("normal", nil, now, false, 0)
	if normal.State != "normal" || normal.EventState != "" {
		t.Fatalf("normal transition = %+v", normal)
	}
}
