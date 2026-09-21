package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/google/uuid"
)

// TestPurgeCurrentManagedVolumesDockerIntegration exercises the real Docker
// ownership boundary. The unit tests use a fake runner for failure branches;
// this test proves the rendered Compose volume names and Docker labels work
// together without allowing an unrelated sentinel volume to be removed.
func TestPurgeCurrentManagedVolumesDockerIntegration(t *testing.T) {
	if os.Getenv("TEST_DOCKER_INTEGRATION") != "1" {
		t.Skip("set TEST_DOCKER_INTEGRATION=1 to run Docker integration tests")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if output, err := dockerIntegrationCommand(ctx, "info"); err != nil {
		t.Fatalf("Docker daemon is unavailable: %v\n%s", err, output)
	}

	root := filepath.Join(t.TempDir(), "install")
	layout, err := installengine.NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	project := "stealth-aud18-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	managed := map[string]string{
		"POSTGRES_VOLUME_NAME":            project + "_postgres",
		"STORAGE_VOLUME_NAME":             project + "_storage",
		"FUNCTIONS_RUNNER_STAGING_VOLUME": project + "_staging",
		"CLICKHOUSE_VOLUME_NAME":          project + "_clickhouse",
		"OTELCOL_VOLUME_NAME":             project + "_otelcol",
		"OTEL_DOCKER_LOGS_VOLUME_NAME":    project + "_dockerlogs",
	}
	composeNames := map[string]string{
		managed["POSTGRES_VOLUME_NAME"]:            "postgres_data",
		managed["STORAGE_VOLUME_NAME"]:             "stealth_storage",
		managed["FUNCTIONS_RUNNER_STAGING_VOLUME"]: "function_runner_staging",
		managed["CLICKHOUSE_VOLUME_NAME"]:          "clickhouse_data",
		managed["OTELCOL_VOLUME_NAME"]:             "otelcol_state",
		managed["OTEL_DOCKER_LOGS_VOLUME_NAME"]:    "otel_docker_logs_state",
	}
	sentinel := project + "_unrelated"
	allVolumes := append([]string(nil), managed["POSTGRES_VOLUME_NAME"], managed["STORAGE_VOLUME_NAME"], managed["FUNCTIONS_RUNNER_STAGING_VOLUME"], managed["CLICKHOUSE_VOLUME_NAME"], managed["OTELCOL_VOLUME_NAME"], managed["OTEL_DOCKER_LOGS_VOLUME_NAME"], sentinel)
	for _, name := range allVolumes {
		t.Cleanup(func() { _, _ = dockerIntegrationCommand(context.Background(), "volume", "rm", "-f", name) })
	}

	if err := os.MkdirAll(filepath.Dir(layout.ProxyFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.TelemetryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(layout.EnvFile, formatEnvFile(map[string]string{
		"COMPOSE_PROJECT_NAME":            project,
		"STORAGE_DRIVER":                  "local",
		"POSTGRES_VOLUME_NAME":            managed["POSTGRES_VOLUME_NAME"],
		"STORAGE_VOLUME_NAME":             managed["STORAGE_VOLUME_NAME"],
		"FUNCTIONS_RUNNER_STAGING_VOLUME": managed["FUNCTIONS_RUNNER_STAGING_VOLUME"],
		"CLICKHOUSE_VOLUME_NAME":          managed["CLICKHOUSE_VOLUME_NAME"],
		"OTELCOL_VOLUME_NAME":             managed["OTELCOL_VOLUME_NAME"],
		"OTEL_DOCKER_LOGS_VOLUME_NAME":    managed["OTEL_DOCKER_LOGS_VOLUME_NAME"],
	})); err != nil {
		t.Fatal(err)
	}
	if err := installengine.WriteAtomic(layout.ComposeFile, []byte(dockerPurgeCompose(project, managed)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installengine.WriteAtomic(layout.ProxyFile, []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installengine.WriteAtomic(layout.VersionFile, []byte("v0.2.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for volumeName, composeName := range composeNames {
		if output, err := dockerIntegrationCommand(ctx, "volume", "create",
			"--label", "com.docker.compose.project="+project,
			"--label", "com.docker.compose.volume="+composeName,
			volumeName,
		); err != nil {
			t.Fatalf("create managed volume %q: %v\n%s", volumeName, err, output)
		}
	}
	if output, err := dockerIntegrationCommand(ctx, "volume", "create", sentinel); err != nil {
		t.Fatalf("create sentinel volume: %v\n%s", err, output)
	}

	t.Setenv("STEALTH_INSTALL_DIR", root)
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	if exitCode := app.run([]string{"uninstall", "--purge", "--yes"}); exitCode != 0 {
		t.Fatalf("real Docker purge exit code = %d", exitCode)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("purge did not remove installation root: err=%v", err)
	}
	if output, err := dockerIntegrationCommand(ctx, "volume", "inspect", sentinel); err != nil {
		t.Fatalf("unrelated sentinel volume was removed: %v\n%s", err, output)
	}
	for _, volumeName := range managed {
		if output, err := dockerIntegrationCommand(ctx, "volume", "inspect", volumeName); err == nil {
			t.Fatalf("managed volume %q remains after purge: %s", volumeName, output)
		}
	}
}

func dockerIntegrationCommand(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	return command.CombinedOutput()
}

func dockerPurgeCompose(project string, volumes map[string]string) string {
	return fmt.Sprintf(`services:
  volume-holder:
    image: alpine:3.24
    command: ["sleep", "infinity"]
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - stealth_storage:/var/lib/stealth/storage
      - function_runner_staging:/var/lib/stealth/staging
      - clickhouse_data:/var/lib/clickhouse
      - otelcol_state:/var/lib/otelcol
      - otel_docker_logs_state:/var/lib/otelcol-logs
volumes:
  postgres_data:
    name: %s
  stealth_storage:
    name: %s
  function_runner_staging:
    name: %s
  clickhouse_data:
    name: %s
  otelcol_state:
    name: %s
  otel_docker_logs_state:
    name: %s
`, volumes["POSTGRES_VOLUME_NAME"], volumes["STORAGE_VOLUME_NAME"], volumes["FUNCTIONS_RUNNER_STAGING_VOLUME"], volumes["CLICKHOUSE_VOLUME_NAME"], volumes["OTELCOL_VOLUME_NAME"], volumes["OTEL_DOCKER_LOGS_VOLUME_NAME"])
}
