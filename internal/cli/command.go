package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

func (execCommandRunner) Run(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func (execCommandRunner) Output(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	return command.Output()
}

func (a *App) runCommand(ctx context.Context, dir, name string, args ...string) error {
	if err := a.runner.Run(ctx, dir, a.out, a.errOut, name, args...); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}

func (a *App) runCommandCaptured(ctx context.Context, dir, name string, args ...string) error {
	var output bytes.Buffer
	writer := io.Writer(&output)
	if a.verbose {
		writer = io.MultiWriter(&output, a.errOut)
	}
	if err := a.runner.Run(ctx, dir, writer, writer, name, args...); err != nil {
		return fmt.Errorf("%s %v failed: %w", name, args, err)
	}
	return nil
}
