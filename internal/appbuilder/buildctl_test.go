package appbuilder

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
)

type recordingRunner struct {
	program string
	args    []string
	env     []string
	err     error
}

func (r *recordingRunner) Run(_ context.Context, program string, args, env []string, _, _ io.Writer) error {
	r.program = program
	r.args = append([]string(nil), args...)
	r.env = append([]string(nil), env...)
	return r.err
}

func TestBuildKitClientUsesPinnedDockerfileFrontendAndSafeExecArguments(t *testing.T) {
	runner := &recordingRunner{}
	target := "release"
	client := &BuildKitClient{Address: "tcp://buildkit:1234", Runner: runner}
	err := client.Build(context.Background(), BuildRequest{
		Definition:  appbuildspec.Spec{DockerfilePath: "docker/App.Dockerfile", ContextDirectory: "src", Target: &target, Platform: "linux/amd64"},
		ContextPath: "/private/work/src", DockerfileRoot: "/private/work/source", OutputPath: "/private/work/image.oci.tar", MetadataPath: "/private/work/metadata.json",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if runner.program != "/usr/local/bin/buildctl" {
		t.Fatalf("program = %q", runner.program)
	}
	want := []string{
		"--addr", "tcp://buildkit:1234", "build", "--frontend", "dockerfile.v0",
		"--local", "context=/private/work/src", "--local", "dockerfile=/private/work/source",
		"--opt", "filename=docker/App.Dockerfile", "--opt", "platform=linux/amd64",
		"--progress=plain", "--output", "type=oci,dest=/private/work/image.oci.tar",
		"--metadata-file", "/private/work/metadata.json", "--opt", "target=release",
	}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("buildctl arguments = %#v, want %#v", runner.args, want)
	}
	joined := strings.Join(runner.args, " ")
	for _, forbidden := range []string{"--allow", "--secret", "--ssh", "build-arg", "gateway.v0", "sh -c"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("buildctl arguments unexpectedly contain %q: %s", forbidden, joined)
		}
	}
	if strings.Join(runner.env, "\n") != "PATH=/usr/local/bin:/usr/bin:/bin\nHOME=/tmp\nLANG=C.UTF-8" {
		t.Fatalf("buildctl environment is not minimal: %#v", runner.env)
	}
}

func TestBuildKitReadinessUsesClientProbeAndFailsClosed(t *testing.T) {
	runner := &recordingRunner{}
	client := &BuildKitClient{Address: "tcp://buildkit:1234", Runner: runner}
	if err := client.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"--addr", "tcp://buildkit:1234", "debug", "workers"}; !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("readiness arguments = %#v, want %#v", runner.args, want)
	}
	runner.err = errors.New("connection refused")
	if err := client.Ready(context.Background()); !errors.Is(err, ErrBuildKitUnavailable) {
		t.Fatalf("Readiness error = %v", err)
	}
}

func TestValidateBuildContextRejectsMissingDockerfileFrontendOverrideAndSymlink(t *testing.T) {
	workspace := t.TempDir()
	writeDockerfile := func(contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := appbuildspec.Spec{DockerfilePath: "Dockerfile", ContextDirectory: ".", Platform: "linux/amd64"}
	writeDockerfile("FROM scratch\n")
	if got, err := ValidateBuildContext(workspace, spec); err != nil || got != workspace {
		t.Fatalf("ValidateBuildContext = %q, %v", got, err)
	}
	writeDockerfile("# syntax=example.invalid/untrusted:latest\nFROM scratch\n")
	if _, err := ValidateBuildContext(workspace, spec); !errors.Is(err, ErrUnsafeDockerfileFrontend) {
		t.Fatalf("frontend override error = %v", err)
	}
	if err := os.Remove(filepath.Join(workspace, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(workspace, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateBuildContext(workspace, spec); err == nil {
		t.Fatal("symlink Dockerfile was accepted")
	}
}
