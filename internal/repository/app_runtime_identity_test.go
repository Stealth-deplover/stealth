package repository

import (
	"testing"

	"github.com/google/uuid"
)

func TestAppRuntimeRouteIdentityNamesAreIncarnationSpecificAndValidated(t *testing.T) {
	appID := uuid.MustParse("018f0d5e-7c19-7abc-8d1e-1234567890ab")
	first := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	second := uuid.MustParse("11111111-2222-4333-8444-666666666666")
	firstName := AppRuntimeContainerNameForIncarnation(appID, first)
	secondName := AppRuntimeContainerNameForIncarnation(appID, second)
	if firstName == secondName {
		t.Fatal("different runtime incarnations reused a routing target")
	}
	if len(firstName) > 63 || !ValidAppRuntimeContainerName(appID, firstName) || !ValidAppRuntimeContainerName(appID, secondName) {
		t.Fatalf("generated route names must be DNS-safe and app-scoped: first=%q second=%q", firstName, secondName)
	}
	for _, candidate := range []string{
		"st-018f0d5e7c197abc8d1e1234567890ac-111111112222433384445555",
		firstName + "-attacker",
		"st-018f0d5e7c197abc8d1e1234567890ab-zzzzzzzzzzzzzzzzzzzzzzzz",
		"stealth-route-018f0d5e7c197abc8d1e1234567890ab-111111112222433384445555",
	} {
		if ValidAppRuntimeContainerName(appID, candidate) {
			t.Errorf("invalid or tenant-crafted route identity %q was accepted", candidate)
		}
	}
}

func TestNewAppRuntimeRouteIdentitiesAreUnique(t *testing.T) {
	seen := make(map[string]struct{}, 256)
	appID := uuid.MustParse("018f0d5e-7c19-7abc-8d1e-1234567890ab")
	for range 256 {
		identity, err := NewAppRuntimeRouteIdentity()
		if err != nil {
			t.Fatal(err)
		}
		name := AppRuntimeContainerNameForIncarnation(appID, identity)
		if _, exists := seen[name]; exists {
			t.Fatalf("route target identity collision: %q", name)
		}
		seen[name] = struct{}{}
	}
}
