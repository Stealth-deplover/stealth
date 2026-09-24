package appbuildspec

import (
	"errors"
	"testing"
)

func TestNormalizeDefaultsAndValidatesBuildDefinition(t *testing.T) {
	spec, err := Normalize(Spec{Platform: "linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.DockerfilePath != "Dockerfile" || spec.ContextDirectory != "." {
		t.Fatalf("Normalize() = %#v", spec)
	}
	for _, invalid := range []Spec{
		{DockerfilePath: "../Dockerfile", ContextDirectory: ".", Platform: "linux/amd64"},
		{DockerfilePath: "/etc/passwd", ContextDirectory: ".", Platform: "linux/amd64"},
		{DockerfilePath: `src\\Dockerfile`, ContextDirectory: ".", Platform: "linux/amd64"},
		{DockerfilePath: "src//Dockerfile", ContextDirectory: ".", Platform: "linux/amd64"},
		{DockerfilePath: "Dockerfile", ContextDirectory: "src/../", Platform: "linux/amd64"},
		{DockerfilePath: "Dockerfile", ContextDirectory: ".", Target: stringPointer("--allow=security.insecure"), Platform: "linux/amd64"},
		{DockerfilePath: "Dockerfile", ContextDirectory: ".", Platform: "linux/ppc64le"},
	} {
		if _, err := Normalize(invalid); !errors.Is(err, ErrInvalidBuildSpec) {
			t.Errorf("Normalize(%#v) error = %v", invalid, err)
		}
	}
}

func TestHostPlatformMapping(t *testing.T) {
	for architecture, want := range map[string]string{"amd64": "linux/amd64", "arm64": "linux/arm64"} {
		got, err := HostPlatform(architecture)
		if err != nil || got != want {
			t.Errorf("HostPlatform(%q) = %q, %v", architecture, got, err)
		}
	}
	if _, err := HostPlatform("386"); !errors.Is(err, ErrInvalidBuildSpec) {
		t.Fatalf("unsupported architecture error = %v", err)
	}
}

func stringPointer(value string) *string { return &value }
