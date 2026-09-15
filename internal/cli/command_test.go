package cli

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// TestExecCommandRunnerCombinedOutputCapturesBothStreams verifies the narrowly
// scoped log-reading primitive without changing the stdout-only semantics of
// the generic Output method.
func TestExecCommandRunnerCombinedOutputCapturesBothStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not available")
	}

	runner := execCommandRunner{}
	combined, err := runner.CombinedOutput(context.Background(), "", shell, "-c", "echo to-stdout; echo to-stderr 1>&2")
	if err != nil {
		t.Fatalf("CombinedOutput() error = %v", err)
	}
	for _, want := range []string{"to-stdout", "to-stderr"} {
		if !strings.Contains(string(combined), want) {
			t.Fatalf("CombinedOutput() = %q, want %q", combined, want)
		}
	}

	stdout, err := runner.Output(context.Background(), "", shell, "-c", "echo to-stdout; echo to-stderr 1>&2")
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if !strings.Contains(string(stdout), "to-stdout") {
		t.Fatalf("Output() = %q, want stdout", stdout)
	}
	if strings.Contains(string(stdout), "to-stderr") {
		t.Fatalf("Output() = %q unexpectedly captured stderr", stdout)
	}
}
