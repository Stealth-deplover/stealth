package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

type systemCheckRunner struct {
	outputs []commandOutput
	calls   []recordedCommand
}

func (r *systemCheckRunner) Run(context.Context, string, io.Writer, io.Writer, string, ...string) error {
	return nil
}

func (r *systemCheckRunner) Output(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(r.outputs) == 0 {
		return nil, errors.New("unexpected host preflight command")
	}
	output := r.outputs[0]
	r.outputs = r.outputs[1:]
	return output.value, output.err
}

func (r *systemCheckRunner) CombinedOutput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, errors.New("combined output is not used by host preflight")
}

func TestSystemChecksUseHostDockerAndComposeProbes(t *testing.T) {
	runner := &systemCheckRunner{outputs: []commandOutput{
		{err: errors.New("Docker daemon is inaccessible")},
		{err: errors.New("Docker Compose is missing")},
	}}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner

	checks := app.systemChecks(context.Background(), filepath.Join(t.TempDir(), "stealth"))
	byName := make(map[string]SystemCheck, len(checks))
	for _, check := range checks {
		byName[check.Name] = check
	}
	for _, name := range []string{"Docker", "Docker Compose"} {
		check, ok := byName[name]
		if !ok || check.OK || !check.Required {
			t.Fatalf("host %s check = %#v", name, check)
		}
	}
	if len(runner.calls) != 2 || runner.calls[0].name != "docker" || runner.calls[1].name != "docker" {
		t.Fatalf("host Docker probe calls = %#v", runner.calls)
	}
	if !equalStrings(runner.calls[0].args, []string{"version", "--format", "{{.Server.Version}}"}) {
		t.Fatalf("Docker version probe = %#v", runner.calls[0].args)
	}
	if !equalStrings(runner.calls[1].args, []string{"compose", "version", "--short"}) {
		t.Fatalf("Compose version probe = %#v", runner.calls[1].args)
	}
}
