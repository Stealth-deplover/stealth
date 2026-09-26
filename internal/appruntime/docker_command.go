package appruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type CommandResult struct {
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

// CommandRunner executes one typed Docker argv vector and can stream an image
// archive to stdin. Implementations must not invoke a shell.
type CommandRunner interface {
	Run(context.Context, []string, io.Reader) (CommandResult, error)
}

type ExecCommandRunner struct {
	DockerPath  string
	OutputLimit int
}

type CommandFailure struct {
	ExitCode int
	Stderr   string
}

func (e *CommandFailure) Error() string { return "Docker command failed" }

type boundedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - len(b.data)
	if remaining <= 0 {
		b.truncated = b.truncated || original > 0
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, value...)
	return original, nil
}

func (b *boundedBuffer) Bytes() []byte { return b.data }

func (r ExecCommandRunner) Run(ctx context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	path := strings.TrimSpace(r.DockerPath)
	if path == "" {
		path = "docker"
	}
	limit := r.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = stdin
	command.Env = []string{"HOME=/tmp"}
	if pathValue := os.Getenv("PATH"); pathValue != "" {
		command.Env = append(command.Env, "PATH="+pathValue)
	}
	stdout := &boundedBuffer{limit: limit}
	stderr := &boundedBuffer{limit: 32 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated}
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return result, &CommandFailure{ExitCode: exitError.ExitCode(), Stderr: string(result.Stderr)}
	}
	return result, err
}

func (m *Moby) runAction(parent context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	return m.runActionWithTimeout(parent, m.ActionTimeout, args, stdin)
}

func (m *Moby) runStopAction(parent context.Context, args []string, grace int) (CommandResult, error) {
	timeout := m.ActionTimeout
	if grace > 0 && time.Duration(grace)*time.Second > timeout {
		timeout = time.Duration(grace) * time.Second
	}
	return m.runActionWithTimeout(parent, timeout, args, nil)
}

func (m *Moby) runActionWithTimeout(parent context.Context, timeout time.Duration, args []string, stdin io.Reader) (CommandResult, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return m.run(ctx, args, stdin)
}

func (m *Moby) run(ctx context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	result, err := m.Runner.Run(ctx, args, stdin)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if dockerNotFound(err) {
			return result, ErrDockerObjectNotFound
		}
		return result, errors.Join(ErrRuntimeUnavailable, err)
	}
	return result, nil
}

func dockerNotFound(err error) bool {
	var failure *CommandFailure
	if errors.As(err, &failure) {
		message := strings.ToLower(failure.Stderr)
		return strings.Contains(message, "no such container") || strings.Contains(message, "no such image") ||
			strings.Contains(message, "no such network") ||
			(strings.Contains(message, "network ") && strings.Contains(message, " not found")) ||
			strings.Contains(message, "no such object")
	}
	return false
}
