package cli

import (
	"os"
	"strings"
	"testing"
)

func TestInstallerPlatformChecks(t *testing.T) {
	tests := []struct {
		name  string
		goos  string
		arch  string
		valid bool
	}{
		{name: "linux amd64", goos: "linux", arch: "amd64", valid: true},
		{name: "linux arm64", goos: "linux", arch: "arm64", valid: true},
		{name: "darwin arm64", goos: "darwin", arch: "arm64"},
		{name: "darwin amd64", goos: "darwin", arch: "amd64"},
		{name: "freebsd amd64", goos: "freebsd", arch: "amd64"},
		{name: "windows amd64", goos: "windows", arch: "amd64"},
		{name: "linux unsupported architecture", goos: "linux", arch: "386"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checks := installerPlatformChecks(test.goos, test.arch)
			if got := checksPass(checks); got != test.valid {
				t.Fatalf("installerPlatformChecks(%s/%s) pass = %v, want %v: %#v", test.goos, test.arch, got, test.valid, checks)
			}
			if !test.valid && test.goos != "linux" && checks[0].OK {
				t.Fatalf("unsupported OS %s was accepted: %#v", test.goos, checks[0])
			}
		})
	}
}

func TestHasInteractiveTerminalRequiresBothStreamsAndRejectsDumbTerminals(t *testing.T) {
	input, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	app := NewApp(input, output, &strings.Builder{})
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	if !app.hasInteractiveTerminal() {
		t.Fatal("character-device stdin/stdout should be considered interactive")
	}

	t.Setenv("TERM", " dumb ")
	if app.hasInteractiveTerminal() {
		t.Fatal("TERM=dumb must disable interactive TUI")
	}

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	if !app.hasInteractiveTerminal() {
		t.Fatal("NO_COLOR should not disable TTY interactivity")
	}

	app.out = &strings.Builder{}
	if app.hasInteractiveTerminal() {
		t.Fatal("non-character stdout must disable interactive TUI")
	}
}
