package appbuilder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appbuildspec"
)

var (
	ErrBuildKitUnavailable          = errors.New("BuildKit is unavailable")
	ErrBuildKitAuthenticationFailed = errors.New("BuildKit authentication failed")
	ErrBuildKitTLSConfig            = errors.New("BuildKit mutual TLS configuration is incomplete or invalid")
)

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
	Address        string
	CACertPath     string
	ClientCertPath string
	ClientKeyPath  string
	BuildctlPath   string
	Runner         CommandRunner
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
	tlsArgs, err := c.tlsArgs()
	if err != nil || strings.TrimSpace(c.Address) == "" {
		return errors.Join(ErrBuildKitUnavailable, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := append([]string{"--addr", c.Address}, tlsArgs...)
	args = append(args, "debug", "workers")
	stderr := &boundedOutput{limit: 4096}
	if err := c.runner().Run(probeCtx, c.buildctlPath(), args, c.environment(), io.Discard, stderr); err != nil {
		return classifyBuildKitFailure(err, stderr.String())
	}
	return nil
}

func (c *BuildKitClient) Build(ctx context.Context, request BuildRequest, progress io.Writer) error {
	tlsArgs, tlsErr := c.tlsArgs()
	if tlsErr != nil {
		return errors.Join(ErrBuildKitUnavailable, tlsErr)
	}
	definition, err := appbuildspec.Normalize(request.Definition)
	if err != nil || strings.TrimSpace(c.Address) == "" || request.ContextPath == "" || request.DockerfileRoot == "" || request.OutputPath == "" || request.MetadataPath == "" {
		return appbuildspec.ErrInvalidBuildSpec
	}
	args, err := buildctlArgs(c.Address, tlsArgs, definition, request.ContextPath, request.DockerfileRoot, request.OutputPath, request.MetadataPath)
	if err != nil {
		return err
	}
	if progress == nil {
		progress = io.Discard
	}
	stderr := &boundedOutput{limit: 8192}
	if err := c.runner().Run(ctx, c.buildctlPath(), args, c.environment(), progress, io.MultiWriter(progress, stderr)); err != nil {
		if isBuildKitAuthenticationFailure(stderr.String()) {
			return errors.Join(ErrBuildKitUnavailable, ErrBuildKitAuthenticationFailed)
		}
		return err
	}
	return nil
}

func (c *BuildKitClient) tlsArgs() ([]string, error) {
	paths := []string{c.CACertPath, c.ClientCertPath, c.ClientKeyPath}
	for _, path := range paths {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) || strings.ContainsAny(path, "\x00\r\n") {
			return nil, ErrBuildKitTLSConfig
		}
	}
	return []string{"--tlscacert", c.CACertPath, "--tlscert", c.ClientCertPath, "--tlskey", c.ClientKeyPath}, nil
}

func buildctlArgs(address string, tlsArgs []string, definition appbuildspec.Spec, contextPath, dockerfileRoot, outputPath, metadataPath string) ([]string, error) {
	definition, err := appbuildspec.Normalize(definition)
	if err != nil || strings.ContainsAny(address+contextPath+dockerfileRoot+outputPath+metadataPath, "\x00\r\n") {
		return nil, appbuildspec.ErrInvalidBuildSpec
	}
	args := []string{
		"--addr", address,
	}
	args = append(args, tlsArgs...)
	args = append(args,
		"build",
		"--frontend", "dockerfile.v0",
		"--local", "context="+contextPath,
		"--local", "dockerfile="+dockerfileRoot,
		"--opt", "filename="+definition.DockerfilePath,
		"--opt", "platform="+definition.Platform,
		"--progress=plain",
		"--output", "type=oci,dest="+outputPath,
		"--metadata-file", metadataPath,
	)
	if definition.Target != nil {
		args = append(args, "--opt", "target="+*definition.Target)
	}
	return args, nil
}

func classifyBuildKitFailure(err error, diagnostic string) error {
	if isBuildKitAuthenticationFailure(diagnostic) {
		return errors.Join(ErrBuildKitUnavailable, ErrBuildKitAuthenticationFailed)
	}
	return fmt.Errorf("%w: %v", ErrBuildKitUnavailable, err)
}

func isBuildKitAuthenticationFailure(diagnostic string) bool {
	diagnostic = strings.ToLower(diagnostic)
	for _, marker := range []string{"tls:", "certificate required", "bad certificate", "unknown authority", "certificate signed by unknown", "handshake failure", "remote error: tls", "failed to verify certificate", "cannot load client certificate"} {
		if strings.Contains(diagnostic, marker) {
			return true
		}
	}
	return false
}

type boundedOutput struct {
	limit int
	value strings.Builder
}

func (b *boundedOutput) Write(contents []byte) (int, error) {
	original := len(contents)
	remaining := b.limit - b.value.Len()
	if remaining > 0 {
		if len(contents) > remaining {
			contents = contents[:remaining]
		}
		_, _ = b.value.Write(contents)
	}
	return original, nil
}

func (b *boundedOutput) String() string { return b.value.String() }
