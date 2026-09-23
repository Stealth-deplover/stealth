package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/installengine"
)

type ingressCommandCall struct {
	name string
	args []string
}

type ingressCommandRunner struct {
	statuses []byte
	runErr   error
	calls    []ingressCommandCall
}

func (r *ingressCommandRunner) Run(_ context.Context, _ string, stdout, _ io.Writer, name string, args ...string) error {
	r.calls = append(r.calls, ingressCommandCall{name: name, args: append([]string(nil), args...)})
	if r.runErr == nil && len(args) > 0 {
		_, _ = io.WriteString(stdout, "ingress-control ran\n")
	}
	return r.runErr
}
func (r *ingressCommandRunner) Output(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, ingressCommandCall{name: name, args: append([]string(nil), args...)})
	if len(args) >= 4 && args[0] == "compose" && args[len(args)-3] == "ps" {
		return append([]byte(nil), r.statuses...), nil
	}
	return nil, errors.New("unexpected Output command")
}
func (r *ingressCommandRunner) CombinedOutput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, errors.New("unexpected CombinedOutput command")
}

func ingressFixture(t *testing.T, databaseURL, statuses string) (*App, *ingressCommandRunner) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "installation")
	layout, err := installengine.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := installengine.WritePrivateFile(layout.EnvFile, strings.Join([]string{
		"SETUP_MODE=false",
		"STEALTH_INGRESS_CONTROL_IMAGE=ghcr.io/stealth-deplover/stealth-ingress-control:v1.2.3",
		"PUBLIC_APP_URL=https://cloud.example.com",
		"DATABASE_URL=" + databaseURL,
	}, "\n")+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := installengine.WriteAtomic(layout.ComposeFile, []byte("services:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEALTH_INSTALL_DIR", root)
	runner := &ingressCommandRunner{statuses: []byte(statuses)}
	return &App{out: io.Discard, errOut: io.Discard, runner: runner, homeDir: t.TempDir()}, runner
}

const healthyIngressServices = `[
{"Service":"api","State":"running","Health":"healthy"},
{"Service":"console","State":"running","Health":"healthy"},
{"Service":"traefik","State":"running","Health":"healthy"},
{"Service":"proxy","State":"running","Health":"healthy"},
{"Service":"cloudflared","State":"running"},
{"Service":"postgres","State":"running","Health":"healthy"}
]`

func TestIngressCutoverRunsRestrictedComposeServiceWithNoDeps(t *testing.T) {
	app, runner := ingressFixture(t, "postgres://user:pass@postgres:5432/stealth", healthyIngressServices)
	if code := app.runIngress([]string{"cutover"}); code != 0 {
		t.Fatalf("runIngress(cutover) = %d", code)
	}
	if len(runner.calls) != 2 || runner.calls[1].name != "docker" {
		t.Fatalf("commands = %#v", runner.calls)
	}
	args := runner.calls[1].args
	if !containsCLIArg(args, "run") || !containsCLIArg(args, "--rm") || !containsCLIArg(args, "--no-deps") || !containsCLIArg(args, "ingress-control") || !containsCLIArg(args, "cutover") {
		t.Fatalf("maintenance invocation = %#v", args)
	}
}

func TestIngressCutoverFailsPreflightBeforeStartingMaintenanceService(t *testing.T) {
	statuses := strings.Replace(healthyIngressServices, `"Service":"traefik","State":"running","Health":"healthy"`, `"Service":"traefik","State":"running","Health":"unhealthy"`, 1)
	app, runner := ingressFixture(t, "postgres://user:pass@postgres:5432/stealth", statuses)
	if code := app.runIngress([]string{"cutover"}); code == 0 {
		t.Fatal("unhealthy Traefik must fail cutover preflight")
	}
	for _, call := range runner.calls {
		if containsCLIArg(call.args, "ingress-control") {
			t.Fatalf("maintenance provider action ran after failed preflight: %#v", runner.calls)
		}
	}
}

func TestIngressExternalPostgresAndRollbackDoNotUsePublicAPI(t *testing.T) {
	statuses := `[
{"Service":"proxy","State":"running","Health":"healthy"},
{"Service":"cloudflared","State":"running"},
{"Service":"traefik","State":"running","Health":"unhealthy"}
]`
	app, runner := ingressFixture(t, "postgres://user:pass@db.example.net:5432/stealth", statuses)
	if code := app.runIngress([]string{"rollback"}); code != 0 {
		t.Fatalf("host rollback failed without an API service: %d", code)
	}
	if len(runner.calls) != 2 || !containsCLIArg(runner.calls[1].args, "rollback") || !containsCLIArg(runner.calls[1].args, "--no-deps") {
		t.Fatalf("rollback command calls = %#v", runner.calls)
	}
}

func TestIngressRollbackRequiresOnlyLocalRollbackDependencies(t *testing.T) {
	tests := []struct {
		name        string
		databaseURL string
		statuses    string
	}{
		{name: "proxy unhealthy", databaseURL: "postgres://user:pass@db.example.net:5432/stealth", statuses: `[{"Service":"proxy","State":"running","Health":"unhealthy"},{"Service":"cloudflared","State":"running"}]`},
		{name: "cloudflared stopped", databaseURL: "postgres://user:pass@db.example.net:5432/stealth", statuses: `[{"Service":"proxy","State":"running","Health":"healthy"},{"Service":"cloudflared","State":"exited"}]`},
		{name: "bundled postgres unhealthy", databaseURL: "postgres://user:pass@postgres:5432/stealth", statuses: `[{"Service":"proxy","State":"running","Health":"healthy"},{"Service":"cloudflared","State":"running"},{"Service":"postgres","State":"running","Health":"unhealthy"}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, runner := ingressFixture(t, test.databaseURL, test.statuses)
			if code := app.runIngress([]string{"rollback"}); code == 0 {
				t.Fatal("rollback should refuse before provider mutation")
			}
			for _, call := range runner.calls {
				if containsCLIArg(call.args, "ingress-control") {
					t.Fatalf("provider rollback ran after local preflight failure: %#v", runner.calls)
				}
			}
		})
	}
}

func TestIngressVerifyPassesPlatformSiteArguments(t *testing.T) {
	app, runner := ingressFixture(t, "postgres://user:pass@postgres:5432/stealth", healthyIngressServices)
	digest := strings.Repeat("a", 64)
	if code := app.runIngress([]string{"verify", "--site-hostname", "portfolio.apps.example.com", "--site-sha256", digest}); code != 0 {
		t.Fatalf("runIngress(verify) = %d", code)
	}
	args := runner.calls[1].args
	for _, want := range []string{"verify", "--site-hostname", "portfolio.apps.example.com", "--site-sha256", digest} {
		if !containsCLIArg(args, want) {
			t.Fatalf("verify command %#v missing %q", args, want)
		}
	}
}

func TestIngressStatusReportsNginxRollbackHealth(t *testing.T) {
	app, _ := ingressFixture(t, "postgres://user:pass@postgres:5432/stealth", healthyIngressServices)
	var output strings.Builder
	app.out = &output
	if code := app.runIngress([]string{"status"}); code != 0 {
		t.Fatalf("runIngress(status) = %d", code)
	}
	if !strings.Contains(output.String(), "Rollback origin health: proxy/Nginx healthy") {
		t.Fatalf("status output = %q", output.String())
	}
}

func containsCLIArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}
