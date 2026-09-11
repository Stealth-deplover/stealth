// Package cli contains the small, operator-facing Stealth command line
// interface. It orchestrates the existing production Compose and migration
// primitives; it does not duplicate backend business logic.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/buildinfo"
)

const (
	defaultRawBaseURL = "https://raw.githubusercontent.com/Stealth-deplover/stealth"
	defaultHomeName   = ".stealth"
)

// CommandRunner is the small boundary around external commands used by the
// CLI. Keeping it injectable makes status/doctor/install orchestration
// testable without requiring Docker in unit tests.
type CommandRunner interface {
	Run(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error
	Output(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

type execCommandRunner struct{}

// App owns process dependencies and CLI configuration. A single App is used
// for one invocation, so it is safe for command implementations to keep small
// amounts of invocation state here.
type App struct {
	in         io.Reader
	out        io.Writer
	errOut     io.Writer
	runner     CommandRunner
	httpClient *http.Client
	homeDir    string
	assetBase  string
	verbose    bool

	// These are intentionally configurable for deterministic tests. Production
	// defaults remain bounded and conservative.
	pollAttempts int
	pollInterval time.Duration
}

// NewApp creates a CLI with production defaults. Tests can replace the
// command runner, HTTP client, or installation directory after construction.
func NewApp(in io.Reader, out, errOut io.Writer) *App {
	homeDir, _ := os.UserHomeDir()
	return &App{
		in:           in,
		out:          out,
		errOut:       errOut,
		runner:       execCommandRunner{},
		httpClient:   &http.Client{Timeout: 20 * time.Second},
		homeDir:      homeDir,
		assetBase:    defaultRawBaseURL,
		pollAttempts: 60,
		pollInterval: 2 * time.Second,
	}
}

// Run dispatches one CLI invocation and returns a shell-friendly exit code.
func Run(args []string, in io.Reader, out, errOut io.Writer) int {
	return NewApp(in, out, errOut).run(args)
}

func (a *App) run(args []string) int {
	if len(args) == 0 {
		a.printUsage(a.out)
		return 2
	}

	switch args[0] {
	case "help", "--help", "-h":
		a.printUsage(a.out)
		return 0
	case "version":
		return a.runVersion(args[1:])
	case "install":
		return a.runInstall(args[1:])
	case "setup":
		return a.runSetup(args[1:])
	case "status":
		return a.runStatus(args[1:])
	case "doctor":
		return a.runDoctor(args[1:])
	case "logs":
		return a.runLogs(args[1:])
	default:
		fmt.Fprintf(a.errOut, "unknown command %q\n\n", args[0])
		a.printUsage(a.errOut)
		return 2
	}
}

func (a *App) printUsage(w io.Writer) {
	fmt.Fprintln(w, "Stealth — Developer Cloud Control Plane")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  stealth install [--version vX.Y.Z] [--repair] [--verbose]")
	fmt.Fprintln(w, "  stealth setup [--adopt-owner]")
	fmt.Fprintln(w, "  stealth status")
	fmt.Fprintln(w, "  stealth doctor")
	fmt.Fprintln(w, "  stealth logs [api|worker|console|proxy|postgres|redis]")
	fmt.Fprintln(w, "  stealth version [--json]")
}

func (a *App) runVersion(args []string) int {
	fs := flag.NewFlagSet("stealth version", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	jsonOutput := fs.Bool("json", false, "print build metadata as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.errOut, "version does not accept positional arguments")
		return 2
	}
	info := buildinfo.Current()
	if *jsonOutput {
		if err := json.NewEncoder(a.out).Encode(info); err != nil {
			fmt.Fprintf(a.errOut, "could not write version: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(a.out, "Stealth %s\nCommit: %s\nBuilt: %s\n", info.Version, info.Commit, info.BuildTime)
	return 0
}

func (a *App) runInstall(args []string) int {
	fs := flag.NewFlagSet("stealth install", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	verbose := fs.Bool("verbose", false, "show Docker command output")
	repair := fs.Bool("repair", false, "reuse an existing installation without replacing its configuration")
	versionOverride := fs.String("version", "", "install a specific release version")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.errOut, "install does not accept positional arguments")
		return 2
	}
	if !a.hasInteractiveTerminal() {
		fmt.Fprintln(a.errOut, "Interactive setup requires a TTY.")
		fmt.Fprintln(a.errOut, "Run `stealth install` from a terminal.")
		return 1
	}

	a.verbose = *verbose
	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	existing := installationExists(layout)
	if existing && !*repair {
		fmt.Fprintf(a.errOut, "Existing Stealth installation detected at %s.\n", layout.Root)
		fmt.Fprintln(a.errOut, "Configuration and data were left unchanged.")
		fmt.Fprintln(a.errOut, "Run `stealth doctor` to inspect it, or `stealth install --repair` to verify it safely.")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	checks := a.systemChecks(ctx, layout.Root)
	if existing {
		plan, loadErr := a.loadExistingPlan(layout)
		if loadErr != nil {
			fmt.Fprintf(a.errOut, "existing installation cannot be repaired: %v\n", loadErr)
			return 1
		}
		return a.runInstallerTUI(ctx, checks, plan, true)
	}
	version, err := a.resolveReleaseVersion(*versionOverride)
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine release version: %v\n", err)
		return 1
	}
	if result := a.runInstallerTUI(ctx, checks, &InstallPlan{Layout: layout, Version: version}, false); result != 0 {
		return result
	}
	return a.runSetupWithContext(ctx)
}

func (a *App) runStatus(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.errOut, "status does not accept arguments")
		return 2
	}
	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	if !installationExists(layout) {
		fmt.Fprintf(a.errOut, "Stealth is not installed at %s\n", layout.Root)
		return 1
	}
	config, err := readEnvFile(layout.EnvFile)
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read configuration: %v\n", err)
		return 1
	}
	statuses, err := a.composeStatuses(context.Background(), layout)
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read Docker service status: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.out, "Stealth %s\n\n", valueOr(config["VERSION"], readVersion(layout)))
	fmt.Fprintln(a.out, "SERVICE          STATUS")
	for _, service := range []string{"api", "worker", "console", "postgres", "redis", "proxy"} {
		status := statuses[service]
		if status.Service == "" {
			status = ServiceStatus{Service: service, State: "not found"}
		}
		fmt.Fprintf(a.out, "%-16s %s\n", displayServiceName(service), status.Display())
	}
	if publicURL := config["PUBLIC_APP_URL"]; publicURL != "" {
		fmt.Fprintf(a.out, "\nConsole: %s\n", publicURL)
	}
	if anyServiceUnhealthy(statuses) {
		return 1
	}
	return 0
}

func (a *App) runDoctor(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.errOut, "doctor does not accept arguments")
		return 2
	}
	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.out, "Stealth Doctor")
	fmt.Fprintln(a.out)

	ctx := context.Background()
	failed := false
	check := func(name string, ok bool, detail string) {
		if !ok {
			failed = true
		}
		fmt.Fprintln(a.out, renderCheck(SystemCheck{Name: name, OK: ok, Detail: detail, Required: true}))
	}

	if output, commandErr := a.runner.Output(ctx, "", "docker", "version", "--format", "{{.Server.Version}}"); commandErr != nil {
		check("Docker", false, "Docker is unavailable")
	} else {
		check("Docker", true, strings.TrimSpace(string(output)))
	}
	if output, commandErr := a.runner.Output(ctx, "", "docker", "compose", "version", "--short"); commandErr != nil {
		check("Docker Compose", false, "Docker Compose is unavailable")
	} else {
		check("Docker Compose", true, strings.TrimSpace(string(output)))
	}

	if !installationExists(layout) {
		check("Installation", false, layout.Root+" is not initialized")
		return boolExit(failed)
	}
	check("Configuration", fileIsPrivate(layout.EnvFile), configCheckDetail(layout.EnvFile, fileIsPrivate(layout.EnvFile)))
	check("Compose file", regularFile(layout.ComposeFile), layout.ComposeFile)
	config, configErr := readEnvFile(layout.EnvFile)
	if configErr != nil {
		check("Configuration syntax", false, "could not parse config.env")
		return boolExit(failed)
	}
	check("Configuration syntax", hasRequiredConfig(config), "required values are present")

	statuses, statusErr := a.composeStatuses(ctx, layout)
	if statusErr != nil {
		check("Docker services", false, "could not query Compose")
	} else {
		for _, service := range []string{"postgres", "redis", "api", "worker", "console", "proxy"} {
			status := statuses[service]
			check(displayServiceName(service), status.Healthy(), status.Display())
		}
	}

	ports := portsFromConfig(config)
	for _, endpoint := range []struct {
		name string
		url  string
	}{
		{"API health", "http://127.0.0.1:" + ports.API + "/healthz"},
		{"API readiness", "http://127.0.0.1:" + ports.API + "/readyz"},
		{"API version", "http://127.0.0.1:" + ports.API + "/version"},
		{"Console", "http://127.0.0.1:" + ports.Console + "/"},
		{"Proxy", "http://127.0.0.1:" + ports.Proxy + "/"},
	} {
		status, requestErr := a.httpStatus(ctx, endpoint.url)
		check(endpoint.name, requestErr == nil && status >= 200 && status < 300, httpStatusDetail(status, requestErr))
	}
	if free, statErr := freeBytes(filepath.Dir(layout.Root)); statErr != nil {
		check("Disk space", false, "could not inspect filesystem")
	} else {
		check("Disk space", free > 0, formatBytes(free)+" free")
	}
	return boolExit(failed)
}

func (a *App) runLogs(args []string) int {
	fs := flag.NewFlagSet("stealth logs", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	follow := fs.Bool("follow", false, "follow log output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(a.errOut, "logs accepts at most one service")
		return 2
	}
	service := ""
	if fs.NArg() == 1 {
		service = fs.Arg(0)
		if !validLogService(service) {
			fmt.Fprintf(a.errOut, "unknown log service %q\n", service)
			return 2
		}
	}
	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	if !installationExists(layout) {
		fmt.Fprintf(a.errOut, "Stealth is not installed at %s\n", layout.Root)
		return 1
	}
	composeArgs := a.composeArgs(layout, "logs", "--tail=100")
	if *follow {
		composeArgs = append(composeArgs, "--follow")
	}
	if service != "" {
		composeArgs = append(composeArgs, service)
	}
	if err := a.runner.Run(context.Background(), layout.Root, a.out, a.errOut, "docker", composeArgs...); err != nil {
		if exitCode := commandExitCode(err); exitCode >= 0 {
			return exitCode
		}
		fmt.Fprintf(a.errOut, "could not read logs: %v\n", err)
		return 1
	}
	return 0
}

func boolExit(failed bool) int {
	if failed {
		return 1
	}
	return 0
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func commandExitCode(err error) int {
	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
