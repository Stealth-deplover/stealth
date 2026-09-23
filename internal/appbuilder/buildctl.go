package appbuilder

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
)

var ErrBuildKitUnavailable = errors.New("BuildKit is unavailable")

type CommandRunner interface {
	Run(context.Context, string, []string, []string, io.Writer, io.Writer) error
}

type ExecCommandRunner struct{}

func (ExecCommandRunner) Run(ctx context.Context, program string, args, env []string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, program, args...)
	command.Env = append([]string(nil), env...)
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

type BuildRequest struct {
	Definition     appbuildspec.Spec
	ContextPath    string
	DockerfileRoot string
	OutputPath     string
	MetadataPath   string
}

type BuildKitClient struct {
	Address      string
	BuildctlPath string
	Runner       CommandRunner
}

func (c *BuildKitClient) runner() CommandRunner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecCommandRunner{}
}

func (c *BuildKitClient) buildctlPath() string {
	if strings.TrimSpace(c.BuildctlPath) != "" {
		return c.BuildctlPath
	}
	return "/usr/local/bin/buildctl"
}

func (c *BuildKitClient) environment() []string {
	// User build commands run in BuildKit. The client process receives only
	// runtime basics and never inherits worker database/provider credentials.
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "LANG=C.UTF-8"}
}

func (c *BuildKitClient) Ready(ctx context.Context) error {
	if strings.TrimSpace(c.Address) == "" {
		return ErrBuildKitUnavailable
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"--addr", c.Address, "debug", "workers"}
	if err := c.runner().Run(probeCtx, c.buildctlPath(), args, c.environment(), io.Discard, io.Discard); err != nil {
		return ErrBuildKitUnavailable
	}
	return nil
}

func (c *BuildKitClient) Build(ctx context.Context, request BuildRequest, progress io.Writer) error {
	definition, err := appbuildspec.Normalize(request.Definition)
	if err != nil || strings.TrimSpace(c.Address) == "" || request.ContextPath == "" || request.DockerfileRoot == "" || request.OutputPath == "" || request.MetadataPath == "" {
		return appbuildspec.ErrInvalidBuildSpec
	}
	args, err := buildctlArgs(c.Address, definition, request.ContextPath, request.DockerfileRoot, request.OutputPath, request.MetadataPath)
	if err != nil {
		return err
	}
	if progress == nil {
		progress = io.Discard
	}
	return c.runner().Run(ctx, c.buildctlPath(), args, c.environment(), progress, progress)
}

func buildctlArgs(address string, definition appbuildspec.Spec, contextPath, dockerfileRoot, outputPath, metadataPath string) ([]string, error) {
	definition, err := appbuildspec.Normalize(definition)
	if err != nil || strings.ContainsAny(address+contextPath+dockerfileRoot+outputPath+metadataPath, "\x00\r\n") {
		return nil, appbuildspec.ErrInvalidBuildSpec
	}
	args := []string{
		"--addr", address,
		"build",
		"--frontend", "dockerfile.v0",
		"--local", "context=" + contextPath,
		"--local", "dockerfile=" + dockerfileRoot,
		"--opt", "filename=" + definition.DockerfilePath,
		"--opt", "platform=" + definition.Platform,
		"--progress=plain",
		"--output", "type=oci,dest=" + outputPath,
		"--metadata-file", metadataPath,
	}
	if definition.Target != nil {
		args = append(args, "--opt", "target="+*definition.Target)
	}
	return args, nil
}
