// Package appbuildspec owns the tenant-controlled Dockerfile build definition.
// It is separate from WorkloadSpec because build provenance and runtime intent
// have different lifecycles and identities.
package appbuildspec

import (
	"errors"
	"regexp"
	"runtime"
	"strings"
)

const MaxRelativePathBytes = 512

var (
	ErrInvalidBuildSpec = errors.New("invalid App build definition")
	targetPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type Spec struct {
	DockerfilePath   string
	ContextDirectory string
	Target           *string
	Platform         string
}

func Normalize(spec Spec) (Spec, error) {
	if strings.TrimSpace(spec.DockerfilePath) == "" {
		spec.DockerfilePath = "Dockerfile"
	}
	if strings.TrimSpace(spec.ContextDirectory) == "" {
		spec.ContextDirectory = "."
	}
	if !validRelativePath(spec.DockerfilePath, false) || !validRelativePath(spec.ContextDirectory, true) {
		return Spec{}, ErrInvalidBuildSpec
	}
	if spec.Target != nil {
		target := strings.TrimSpace(*spec.Target)
		if !targetPattern.MatchString(target) {
			return Spec{}, ErrInvalidBuildSpec
		}
		spec.Target = &target
	}
	if spec.Platform != "linux/amd64" && spec.Platform != "linux/arm64" {
		return Spec{}, ErrInvalidBuildSpec
	}
	return spec, nil
}

func validRelativePath(value string, allowDot bool) bool {
	if value == "" || len([]byte(value)) > MaxRelativePathBytes || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") || strings.TrimSpace(value) != value {
		return false
	}
	if allowDot && value == "." {
		return true
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func HostPlatform(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "linux/amd64", nil
	case "arm64":
		return "linux/arm64", nil
	default:
		return "", ErrInvalidBuildSpec
	}
}

func CurrentHostPlatform() (string, error) { return HostPlatform(runtime.GOARCH) }

func ValidTarget(value string) bool { return targetPattern.MatchString(value) }
