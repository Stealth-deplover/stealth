package functionrunner

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type fakeDockerCall struct {
	args string
}

type fakeDocker struct {
	calls []fakeDockerCall
}

func (f *fakeDocker) Run(_ context.Context, args []string, _ io.Reader, stdout, stderr io.Writer) error {
	f.calls = append(f.calls, fakeDockerCall{args: strings.Join(args, " ")})
	if strings.Contains(strings.Join(args, " "), "node /workspace/src/main.js") && stdout != nil {
		_, _ = io.WriteString(stdout, `{"ok":true}`)
	}
	_ = stderr
	return nil
}

func TestDockerExecutorUsesIsolationFlagsAndReadOnlyRuntimeMount(t *testing.T) {
	fake := &fakeDocker{}
	executor := NewDockerExecutor("stealth-function-runner-staging")
	executor.Command = fake
	job := repository.FunctionExecutionJob{
		Execution: domain.FunctionExecution{ID: "018f27e3-5d1a-7c44-ae35-1db4ea12e6d2", InputJSON: []byte(`{"name":"world"}`)},
		Function:  domain.Function{Runtime: "node-22", Entrypoint: "src/main.js"},
	}
	result, err := executor.Execute(context.Background(), job, "jobs/018f27e3-5d1a-7c44-ae35-1db4ea12e6d2", []repository.FunctionRuntimeVariable{{Key: "TOKEN", Value: "secret-value", IsSecret: true}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout == "" {
		t.Fatal("runtime did not capture stdout")
	}
	if len(fake.calls) != 4 {
		t.Fatalf("got %d docker calls, want volume create, copy, runtime, volume rm", len(fake.calls))
	}
	for index, call := range fake.calls[1:3] {
		for _, required := range []string{"--network=none", "--cap-drop=ALL", "no-new-privileges:true", "--read-only", "--pull=never"} {
			if !strings.Contains(call.args, required) {
				t.Errorf("call %d missing %q: %s", index, required, call.args)
			}
		}
	}
	if !strings.Contains(fake.calls[2].args, "destination=/workspace,readonly") {
		t.Fatalf("runtime workspace was not read-only: %s", fake.calls[2].args)
	}
	if !strings.Contains(fake.calls[2].args, "--interactive") {
		t.Fatalf("runtime container did not keep stdin attached: %s", fake.calls[2].args)
	}
}

func TestRuntimeCommandRejectsUnsafeEntrypoint(t *testing.T) {
	executor := NewDockerExecutor("staging")
	if _, _, err := executor.runtimeCommand("node-22", "../main.js"); err == nil {
		t.Fatal("unsafe entrypoint was accepted")
	}
}

func TestDockerExecutorBuildExportsArtifactWithWritableBuildMount(t *testing.T) {
	executor := NewDockerExecutor("stealth-function-runner-staging")
	deploymentID := uuid.Must(uuid.NewV7())
	job := repository.FunctionBuildJob{
		Deployment: domain.FunctionDeployment{ID: deploymentID.String()},
		Function:   domain.Function{Runtime: "node-22", Entrypoint: "src/main.js", Commands: "echo build"},
	}
	// The normal fake only records calls. Add the tar stream a trusted helper
	// would emit so Build also exercises its artifact sink contract.
	var calls []string
	executor.Command = commandRunnerFunc(func(_ context.Context, args []string, _ io.Reader, stdout, _ io.Writer) error {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		if strings.Contains(joined, "tar -cf - .") && stdout != nil {
			writer := tar.NewWriter(stdout)
			if err := writer.WriteHeader(&tar.Header{Name: "src/main.js", Mode: 0o644, Size: int64(len("console.log(1)"))}); err != nil {
				return err
			}
			if _, err := writer.Write([]byte("console.log(1)")); err != nil {
				return err
			}
			return writer.Close()
		}
		return nil
	})
	var artifact bytes.Buffer
	if err := executor.Build(context.Background(), job, "build/"+deploymentID.String(), nil, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Len() == 0 {
		t.Fatal("Build() returned an empty artifact")
	}
	if len(calls) != 5 {
		t.Fatalf("got %d docker calls, want create, copy, build, export, remove", len(calls))
	}
	if !strings.Contains(calls[2], "sh -c echo build") || strings.Contains(calls[2], "destination=/workspace,readonly") {
		t.Fatalf("build container did not receive a writable workspace: %s", calls[2])
	}
	if !strings.Contains(calls[3], "destination=/src,readonly") {
		t.Fatalf("export helper did not receive a read-only volume: %s", calls[3])
	}
}

func TestDockerExecutorBuildSiteExportsConfiguredOutputDirectory(t *testing.T) {
	executor := NewDockerExecutor("stealth-function-runner-staging")
	deploymentID := uuid.Must(uuid.NewV7())
	job := repository.SiteBuildJob{
		Deployment: domain.SiteDeployment{ID: deploymentID.String(), BuildRuntime: "node-22", BuildCommand: "mkdir -p dist; printf '<h1>ok</h1>' > dist/index.html", OutputDirectory: "dist"},
	}
	var calls []string
	executor.Command = commandRunnerFunc(func(_ context.Context, args []string, _ io.Reader, stdout, _ io.Writer) error {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		if strings.Contains(joined, "tar -cf - .") && stdout != nil {
			writer := tar.NewWriter(stdout)
			if err := writer.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: int64(len("<h1>ok</h1>"))}); err != nil {
				return err
			}
			if _, err := writer.Write([]byte("<h1>ok</h1>")); err != nil {
				return err
			}
			return writer.Close()
		}
		return nil
	})
	var artifact bytes.Buffer
	if err := executor.BuildSite(context.Background(), job, "site-builds/"+deploymentID.String(), &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Len() == 0 {
		t.Fatal("BuildSite() returned an empty artifact")
	}
	if len(calls) != 5 {
		t.Fatalf("got %d docker calls, want create, copy, build, export, remove", len(calls))
	}
	if !strings.Contains(calls[2], "sh -c mkdir -p dist") || strings.Contains(calls[2], "destination=/workspace,readonly") {
		t.Fatalf("site build container did not receive a writable workspace: %s", calls[2])
	}
	if !strings.Contains(calls[3], "cd /src/dist; tar -cf - .") || !strings.Contains(calls[3], "destination=/src,readonly") {
		t.Fatalf("site export did not use the configured output directory: %s", calls[3])
	}
}

func TestSafeSiteOutputDirectoryRejectsTraversal(t *testing.T) {
	for _, value := range []string{"../dist", "dist/../public", "/tmp", "dist\\public", "dist//public"} {
		if safeSiteOutputDirectory(value) {
			t.Fatalf("unsafe site output directory accepted: %q", value)
		}
	}
	for _, value := range []string{".", "dist", ".next/static", "build.v2"} {
		if !safeSiteOutputDirectory(value) {
			t.Fatalf("safe site output directory rejected: %q", value)
		}
	}
}

func TestBoundedBufferDoesNotBlockAfterLimit(t *testing.T) {
	var output boundedBuffer
	data := strings.Repeat("x", int(maxCapturedOutput)+1)
	written, err := output.Write([]byte(data))
	if err != nil || written != len(data) {
		t.Fatalf("Write() = (%d,%v), want all bytes accepted", written, err)
	}
	if !output.Truncated() || int64(output.Len()) != maxCapturedOutput {
		t.Fatalf("buffer state = truncated=%v len=%d", output.Truncated(), output.Len())
	}
}

type commandRunnerFunc func(context.Context, []string, io.Reader, io.Writer, io.Writer) error

func (f commandRunnerFunc) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return f(ctx, args, stdin, stdout, stderr)
}
