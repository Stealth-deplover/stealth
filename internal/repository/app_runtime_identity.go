package repository

import (
	"strings"

	"github.com/google/uuid"
)

const appRuntimeRouteTokenHexLength = 24

// NewAppRuntimeRouteIdentity creates the trusted identity for one runtime
// incarnation. It is persisted with the expected container DNS name and is
// never accepted from tenant input.
func NewAppRuntimeRouteIdentity() (uuid.UUID, error) {
	return uuid.NewRandom()
}

// AppRuntimeContainerNameForIncarnation returns a Docker/DNS label that is
// unique to an App runtime incarnation and remains below the DNS label limit.
func AppRuntimeContainerNameForIncarnation(appID, identity uuid.UUID) string {
	if appID == uuid.Nil || identity == uuid.Nil {
		return ""
	}
	app := strings.ReplaceAll(appID.String(), "-", "")
	token := strings.ReplaceAll(identity.String(), "-", "")
	return "st-" + app + "-" + token[:appRuntimeRouteTokenHexLength]
}

// ValidAppRuntimeContainerName accepts the current incarnation-specific
// target or the one legacy name shape so cleanup can safely remove containers
// left by an upgrade that began before the route identity migration.
func ValidAppRuntimeContainerName(appID uuid.UUID, name string) bool {
	if appID == uuid.Nil {
		return false
	}
	if name == AppRuntimeContainerName(appID) {
		return true
	}
	prefix := "st-" + strings.ReplaceAll(appID.String(), "-", "") + "-"
	if len(name) != len(prefix)+appRuntimeRouteTokenHexLength || !strings.HasPrefix(name, prefix) {
		return false
	}
	for _, character := range name[len(prefix):] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

// AppRuntimeContainerNameForRouteIdentity returns the only accepted route
// target for this persisted identity. Keeping this helper in the repository
// package lets the database projection and ingress renderer share validation.
func AppRuntimeContainerNameForRouteIdentity(appID uuid.UUID, identity string) (string, bool) {
	parsed, err := uuid.Parse(identity)
	if err != nil || parsed == uuid.Nil || parsed.String() != identity {
		return "", false
	}
	name := AppRuntimeContainerNameForIncarnation(appID, parsed)
	return name, name != "" && ValidAppRuntimeContainerName(appID, name)
}
