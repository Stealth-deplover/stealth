package repository

import (
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
)

func TestInvitationStatus(t *testing.T) {
	now := time.Now().UTC()
	base := domain.OrganizationInvitation{ExpiresAt: now.Add(time.Hour)}
	if got := invitationStatus(base, now); got != "pending" {
		t.Fatalf("pending status = %q", got)
	}
	base.ExpiresAt = now.Add(-time.Second)
	if got := invitationStatus(base, now); got != "expired" {
		t.Fatalf("expired status = %q", got)
	}
	accepted := now.Add(-time.Minute)
	base.AcceptedAt = &accepted
	if got := invitationStatus(base, now); got != "accepted" {
		t.Fatalf("accepted status = %q", got)
	}
	base.AcceptedAt = nil
	base.RevokedAt = &accepted
	if got := invitationStatus(base, now); got != "revoked" {
		t.Fatalf("revoked status = %q", got)
	}
}

func TestInvitationRolesExcludeOwner(t *testing.T) {
	for _, role := range []string{"admin", "developer", "viewer", "billing"} {
		if !OrganizationMembershipRole(role) {
			t.Fatalf("role %q should be assignable", role)
		}
	}
	if OrganizationMembershipRole("owner") {
		t.Fatal("owner should not be assignable by invitation")
	}
}
