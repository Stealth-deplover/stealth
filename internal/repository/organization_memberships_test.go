package repository

import "testing"

func TestCanManageOrganizationMembership(t *testing.T) {
	tests := []struct {
		name       string
		actorRole  string
		targetRole string
		nextRole   string
		want       bool
	}{
		{name: "owner promotes viewer", actorRole: "owner", targetRole: "viewer", nextRole: "admin", want: true},
		{name: "owner demotes admin", actorRole: "owner", targetRole: "admin", nextRole: "developer", want: true},
		{name: "admin updates viewer", actorRole: "admin", targetRole: "viewer", nextRole: "developer", want: true},
		{name: "admin cannot update admin", actorRole: "admin", targetRole: "admin", nextRole: "developer", want: false},
		{name: "admin cannot grant admin", actorRole: "admin", targetRole: "viewer", nextRole: "admin", want: false},
		{name: "owner cannot alter owner", actorRole: "owner", targetRole: "owner", nextRole: "admin", want: false},
		{name: "no role transfer", actorRole: "owner", targetRole: "viewer", nextRole: "owner", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canManageOrganizationMembership(test.actorRole, test.targetRole, test.nextRole); got != test.want {
				t.Fatalf("canManageOrganizationMembership(%q, %q, %q) = %v, want %v", test.actorRole, test.targetRole, test.nextRole, got, test.want)
			}
		})
	}
}

func TestOrganizationMembershipRole(t *testing.T) {
	for _, role := range []string{"admin", "developer", "viewer", "billing"} {
		if !OrganizationMembershipRole(role) {
			t.Fatalf("role %q should be assignable", role)
		}
	}
	for _, role := range []string{"", "owner", "member", "ADMIN"} {
		if OrganizationMembershipRole(role) {
			t.Fatalf("role %q should not be assignable", role)
		}
	}
}
