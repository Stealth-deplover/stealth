package functionrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/repository"
)

var (
	ErrRuntimeUnavailable  = errors.New("function runtime is unavailable")
	ErrOutputTooLarge      = errors.New("function output exceeded the configured limit")
	containerNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	envKeyPattern          = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,119}$`)
	buildOutputPathPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*$`)
)

const (
	defaultHelperImage = "alpine:3.22"
	defaultNodeImage   = "node:22-alpine"
	defaultPythonImage = "python:3.13-alpine"
	defaultGoImage     = "golang:1.24-alpine"
	defaultMemory      = "256m"
	defaultPIDs        = "128"
	defaultCPUs        = "1"
	defaultTmpfs       = "/tmp:rw,nosuid,nodev,noexec,size=64m"
	maxCapturedOutput  = int64(1 << 20)
)

// CommandRunner is injectable so security flags and command construction can
// be tested without requiring a Docker daemon. The production implementation
// runs only the Docker CLI and never shells out through a user-controlled
// command string.
type CommandRunner interface {
	Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error
}

type OSCommandRunner struct {
	Binary string
}

func (r OSCommandRunner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	binary := strings.TrimSpace(r.Binary)
	if binary == "" {
		binary = "docker"
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

type ExecutionResult struct {
	Stdout    string
	Stderr    string
	Truncated bool
}

// Build prepares one deployment volume, runs the configured build command,
// and streams a tar archive of the resulting files to sink. The volume is
// removed before Build returns; the caller persists the streamed archive in
// the private Function store instead of relying on Docker volume lifetime.
func (e *DockerExecutor) Build(ctx context.Context, job repository.FunctionBuildJob, stagingSubpath string, variables []repository.FunctionRuntimeVariable, sink io.Writer) error {
	if e == nil || e.Command == nil || strings.TrimSpace(e.StagingVolume) == "" || sink == nil {
		return ErrRuntimeUnavailable
	}
	if !safeVolumeSubpath(stagingSubpath) || !containerNamePattern.MatchString(e.StagingVolume) {
		return ErrRuntimeUnavailable
	}
	volume := e.buildVolumeName(job.Deployment.ID)
	if !containerNamePattern.MatchString(volume) {
		return ErrRuntimeUnavailable
	}
	if err := e.run(ctx, []string{"volume", "create", "--label", "stealth.managed=true", "--label", "stealth.deployment_id=" + job.Deployment.ID, volume}, nil, nil, nil); err != nil {
		return fmt.Errorf("create build volume: %w", err)
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.run(cleanupCtx, []string{"volume", "rm", "--", volume}, nil, nil, nil)
	}
	defer cleanup()
	if err := e.copyStaging(ctx, volume, stagingSubpath); err != nil {
		return err
	}
	image, _, err := e.runtimeCommand(job.Function.Runtime, job.Function.Entrypoint)
	if err != nil {
		return err
	}
	env, err := runtimeEnvironment(variables)
	if err != nil {
		return err
	}
	if strings.TrimSpace(job.Function.Commands) != "" {
		buildArgs := e.containerArgs(volume, image, env, false)
		buildArgs = append(buildArgs, "sh", "-c", job.Function.Commands)
		var buildStdout, buildStderr boundedBuffer
		if err := e.run(ctx, buildArgs, nil, &buildStdout, &buildStderr); err != nil {
			return fmt.Errorf("function build failed: %w", usefulCommandError(ctx, err, buildStderr.String()))
		}
		if buildStdout.Truncated() || buildStderr.Truncated() {
			return ErrOutputTooLarge
		}
	}
	return e.exportBuild(ctx, volume, sink)
}

// BuildSite runs an uploaded Site source archive inside the same hardened
// Docker boundary as Function builds. The command can only see its copied
// workspace; the resulting output directory is exported as an opaque tar
// stream for the Site worker to validate and publish.
func (e *DockerExecutor) BuildSite(ctx context.Context, job repository.SiteBuildJob, stagingSubpath string, sink io.Writer) error {
	if e == nil || e.Command == nil || strings.TrimSpace(e.StagingVolume) == "" || sink == nil {
		return ErrRuntimeUnavailable
	}
	if !safeVolumeSubpath(stagingSubpath) || !containerNamePattern.MatchString(e.StagingVolume) || !safeSiteOutputDirectory(job.Deployment.OutputDirectory) {
		return ErrRuntimeUnavailable
	}
	volume := e.buildVolumeName(job.Deployment.ID)
	if !containerNamePattern.MatchString(volume) {
		return ErrRuntimeUnavailable
	}
	if err := e.run(ctx, []string{"volume", "create", "--label", "stealth.managed=true", "--label", "stealth.site_deployment_id=" + job.Deployment.ID, volume}, nil, nil, nil); err != nil {
		return fmt.Errorf("create site build volume: %w", err)
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.run(cleanupCtx, []string{"volume", "rm", "--", volume}, nil, nil, nil)
	}
	defer cleanup()
	if err := e.copyStaging(ctx, volume, stagingSubpath); err != nil {
		return err
	}
	image := e.siteBuildImage(job.Deployment.BuildRuntime)
	if image == "" || strings.TrimSpace(job.Deployment.BuildCommand) == "" {
		return ErrRuntimeUnavailable
	}
	buildArgs := e.containerArgs(volume, image, nil, false)
	buildArgs = append(buildArgs, "sh", "-c", job.Deployment.BuildCommand)
	var buildStdout, buildStderr boundedBuffer
	if err := e.run(ctx, buildArgs, nil, &buildStdout, &buildStderr); err != nil {
		return fmt.Errorf("site build failed: %w", usefulCommandError(ctx, err, buildStderr.String()))
	}
	if buildStdout.Truncated() || buildStderr.Truncated() {
		return ErrOutputTooLarge
	}
	return e.exportSiteBuild(ctx, volume, job.Deployment.OutputDirectory, sink)
}

// DockerExecutor provisions one short-lived, per-execution volume. The
// staging volume is mounted only into a trusted copy helper; user code sees a
// separate volume and never sees the artifact store or Docker socket.
type DockerExecutor struct {
	Command       CommandRunner
	StagingVolume string
	HelperImage   string
	NodeImage     string
	PythonImage   string
	GoImage       string
	VolumePrefix  string
}

func NewDockerExecutor(stagingVolume string) *DockerExecutor {
	return &DockerExecutor{
		Command:       OSCommandRunner{},
		StagingVolume: strings.TrimSpace(stagingVolume),
		HelperImage:   defaultHelperImage,
		NodeImage:     defaultNodeImage,
		PythonImage:   defaultPythonImage,
		GoImage:       defaultGoImage,
		VolumePrefix:  "stealth-function-execution-",
	}
}

func (e *DockerExecutor) Execute(ctx context.Context, job repository.FunctionExecutionJob, stagingSubpath string, variables []repository.FunctionRuntimeVariable) (ExecutionResult, error) {
	if e == nil || e.Command == nil || strings.TrimSpace(e.StagingVolume) == "" {
		return ExecutionResult{}, ErrRuntimeUnavailable
	}
	if !safeVolumeSubpath(stagingSubpath) || !containerNamePattern.MatchString(e.StagingVolume) {
		return ExecutionResult{}, ErrRuntimeUnavailable
	}
	volume := e.volumeName(job.Execution.ID)
	if !containerNamePattern.MatchString(volume) {
		return ExecutionResult{}, ErrRuntimeUnavailable
	}
	if err := e.run(ctx, []string{"volume", "create", "--label", "stealth.managed=true", "--label", "stealth.execution_id=" + job.Execution.ID, volume}, nil, nil, nil); err != nil {
		return ExecutionResult{}, fmt.Errorf("create execution volume: %w", err)
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.run(cleanupCtx, []string{"volume", "rm", "--", volume}, nil, nil, nil)
	}
	defer cleanup()

	if err := e.copyStaging(ctx, volume, stagingSubpath); err != nil {
		return ExecutionResult{}, err
	}
	env, err := runtimeEnvironment(variables)
	if err != nil {
		return ExecutionResult{}, err
	}
	image, command, err := e.runtimeCommand(job.Function.Runtime, job.Function.Entrypoint)
	if err != nil {
		return ExecutionResult{}, err
	}
	if strings.TrimSpace(job.Function.Commands) != "" {
		buildArgs := e.containerArgs(volume, image, env, false)
		buildArgs = append(buildArgs, "sh", "-c", job.Function.Commands)
		var buildStdout, buildStderr boundedBuffer
		if err := e.run(ctx, buildArgs, nil, &buildStdout, &buildStderr); err != nil {
			return ExecutionResult{Stdout: buildStdout.String(), Stderr: buildStderr.String(), Truncated: buildStdout.Truncated() || buildStderr.Truncated()}, fmt.Errorf("function build failed: %w", usefulCommandError(ctx, err, buildStderr.String()))
		}
		if buildStdout.Truncated() || buildStderr.Truncated() {
			return ExecutionResult{Stdout: buildStdout.String(), Stderr: buildStderr.String(), Truncated: true}, ErrOutputTooLarge
		}
	}
	args := e.containerArgs(volume, image, env, true)
	args = append(args, command...)
	var stdout, stderr boundedBuffer
	if err := e.run(ctx, args, bytes.NewReader(job.Execution.InputJSON), &stdout, &stderr); err != nil {
		return ExecutionResult{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.Truncated() || stderr.Truncated()}, usefulCommandError(ctx, err, stderr.String())
	}
	if stdout.Truncated() || stderr.Truncated() {
		return ExecutionResult{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: true}, ErrOutputTooLarge
	}
	return ExecutionResult{Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

func (e *DockerExecutor) copyStaging(ctx context.Context, volume, stagingSubpath string) error {
	if !safeVolumeSubpath(stagingSubpath) {
		return ErrRuntimeUnavailable
	}
	helperImage := strings.TrimSpace(e.HelperImage)
	if helperImage == "" {
		helperImage = defaultHelperImage
	}
	args := []string{
		"run", "--rm", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt", "no-new-privileges:true",
		"--pids-limit", defaultPIDs, "--memory", "64m", "--memory-swap", "64m", "--cpus", defaultCPUs, "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=16m",
		"--mount", "type=volume,source=" + e.StagingVolume + ",destination=/src,readonly",
		"--mount", "type=volume,source=" + volume + ",destination=/dest",
		"--user", "0:0", "--workdir", "/src", "--entrypoint", "sh", helperImage,
		"-c", "set -eu; test -d /src/" + stagingSubpath + "; cp -a -- /src/" + stagingSubpath + "/. /dest/; chmod -R a+rwX /dest",
	}
	var stderr boundedBuffer
	if err := e.run(ctx, args, nil, nil, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("copy function workspace: %w: %s", err, trimError(message))
		}
		return fmt.Errorf("copy function workspace: %w", err)
	}
	return nil
}

func (e *DockerExecutor) exportBuild(ctx context.Context, volume string, sink io.Writer) error {
	helperImage := strings.TrimSpace(e.HelperImage)
	if helperImage == "" {
		helperImage = defaultHelperImage
	}
	args := []string{
		"run", "--rm", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt", "no-new-privileges:true",
		"--pids-limit", defaultPIDs, "--memory", "64m", "--memory-swap", "64m", "--cpus", defaultCPUs, "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=16m",
		"--mount", "type=volume,source=" + volume + ",destination=/src,readonly",
		"--user", "0:0", "--workdir", "/src", "--entrypoint", "sh", helperImage,
		"-c", "set -eu; cd /src; tar -cf - .",
	}
	var stderr boundedBuffer
	if err := e.run(ctx, args, nil, sink, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("export function build artifact: %w: %s", err, trimError(message))
		}
		return fmt.Errorf("export function build artifact: %w", err)
	}
	if stderr.Truncated() {
		return ErrOutputTooLarge
	}
	return nil
}

func (e *DockerExecutor) exportSiteBuild(ctx context.Context, volume, outputDirectory string, sink io.Writer) error {
	if !safeSiteOutputDirectory(outputDirectory) {
		return ErrRuntimeUnavailable
	}
	helperImage := strings.TrimSpace(e.HelperImage)
	if helperImage == "" {
		helperImage = defaultHelperImage
	}
	command := "set -eu; test -d /src/" + outputDirectory + "; cd /src/" + outputDirectory + "; tar -cf - ."
	args := []string{
		"run", "--rm", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt", "no-new-privileges:true",
		"--pids-limit", defaultPIDs, "--memory", "64m", "--memory-swap", "64m", "--cpus", defaultCPUs, "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=16m",
		"--mount", "type=volume,source=" + volume + ",destination=/src,readonly",
		"--user", "0:0", "--workdir", "/src", "--entrypoint", "sh", helperImage,
		"-c", command,
	}
	var stderr boundedBuffer
	if err := e.run(ctx, args, nil, sink, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("export site build artifact: %w: %s", err, trimError(message))
		}
		return fmt.Errorf("export site build artifact: %w", err)
	}
	if stderr.Truncated() {
		return ErrOutputTooLarge
	}
	return nil
}

func (e *DockerExecutor) containerArgs(volume, image string, env []string, readonly bool) []string {
	args := []string{
		"run", "--rm", "--interactive", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt", "no-new-privileges:true",
		"--pids-limit", defaultPIDs, "--memory", defaultMemory, "--memory-swap", defaultMemory, "--cpus", defaultCPUs,
		"--ulimit", "nofile=1024:1024", "--tmpfs", defaultTmpfs, "--user", "65532:65532",
		"--mount", "type=volume,source=" + volume + ",destination=/workspace",
		"--workdir", "/workspace", "--env", "HOME=/tmp", "--env", "TMPDIR=/tmp",
	}
	if readonly {
		for index := range args {
			if args[index] == "type=volume,source="+volume+",destination=/workspace" {
				args[index] += ",readonly"
				break
			}
		}
	}
	for _, value := range env {
		args = append(args, "--env", value)
	}
	args = append(args, image)
	return args
}

func (e *DockerExecutor) runtimeCommand(runtime, entrypoint string) (string, []string, error) {
	if !safeRuntimeEntrypoint(entrypoint) {
		return "", nil, ErrRuntimeUnavailable
	}
	containerPath := "/workspace/" + strings.ReplaceAll(entrypoint, "\\", "")
	nodeImage, pythonImage, goImage := e.NodeImage, e.PythonImage, e.GoImage
	if strings.TrimSpace(nodeImage) == "" {
		nodeImage = defaultNodeImage
	}
	if strings.TrimSpace(pythonImage) == "" {
		pythonImage = defaultPythonImage
	}
	if strings.TrimSpace(goImage) == "" {
		goImage = defaultGoImage
	}
	switch runtime {
	case "node-22":
		return nodeImage, []string{"node", containerPath}, nil
	case "python-3.13":
		return pythonImage, []string{"python", containerPath}, nil
	case "go-1.24":
		return goImage, []string{"go", "run", containerPath}, nil
	default:
		return "", nil, ErrRuntimeUnavailable
	}
}

func (e *DockerExecutor) siteBuildImage(runtime string) string {
	switch runtime {
	case "node-22":
		if strings.TrimSpace(e.NodeImage) != "" {
			return e.NodeImage
		}
		return defaultNodeImage
	case "python-3.13":
		if strings.TrimSpace(e.PythonImage) != "" {
			return e.PythonImage
		}
		return defaultPythonImage
	case "go-1.24":
		if strings.TrimSpace(e.GoImage) != "" {
			return e.GoImage
		}
		return defaultGoImage
	default:
		return ""
	}
}

func (e *DockerExecutor) volumeName(executionID string) string {
	return strings.TrimSuffix(e.VolumePrefix, "-") + "-" + strings.ReplaceAll(executionID, "-", "")
}

func (e *DockerExecutor) buildVolumeName(deploymentID string) string {
	prefix := strings.TrimSuffix(e.VolumePrefix, "-")
	if prefix == "" {
		prefix = "stealth-function-execution"
	}
	return prefix + "-build-" + strings.ReplaceAll(deploymentID, "-", "")
}

func (e *DockerExecutor) run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return e.Command.Run(ctx, args, stdin, stdout, stderr)
}

func runtimeEnvironment(variables []repository.FunctionRuntimeVariable) ([]string, error) {
	env := make([]string, 0, len(variables)+2)
	for _, variable := range variables {
		if !envKeyPattern.MatchString(variable.Key) || strings.IndexByte(variable.Value, 0) >= 0 {
			return nil, ErrRuntimeUnavailable
		}
		env = append(env, variable.Key+"="+variable.Value)
	}
	return env, nil
}

func safeVolumeSubpath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) {
		return false
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || !containerNamePattern.MatchString(part) {
			return false
		}
	}
	return true
}

func safeRuntimeEntrypoint(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func safeSiteOutputDirectory(value string) bool {
	value = strings.TrimSpace(value)
	if value == "." {
		return true
	}
	if value == "" || len(value) > 255 || !buildOutputPathPattern.MatchString(value) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func usefulCommandError(ctx context.Context, err error, stderr string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if strings.TrimSpace(stderr) != "" {
		return fmt.Errorf("%w: %s", err, trimError(stderr))
	}
	return err
}

func trimError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 4000 {
		return value[:4000]
	}
	return value
}

type boundedBuffer struct {
	bytes.Buffer
	limit     int64
	truncated bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	if b.limit == 0 {
		b.limit = maxCapturedOutput
	}
	remaining := b.limit - int64(b.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(value), nil
	}
	if int64(len(value)) > remaining {
		_, _ = b.Buffer.Write(value[:remaining])
		b.truncated = true
		return len(value), nil
	}
	return b.Buffer.Write(value)
}

func (b *boundedBuffer) Truncated() bool { return b.truncated }
