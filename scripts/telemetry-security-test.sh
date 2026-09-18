#!/bin/sh
set -eu

compose_file=${STEALTH_PRODUCTION_COMPOSE_FILE_FOR_TEST:-compose.production.yaml}

if [ ! -f "$compose_file" ]; then
	printf 'telemetry security check: missing %s\n' "$compose_file" >&2
	exit 1
fi

schema_version=$(tr -d '[:space:]' < telemetry/schema/VERSION 2>/dev/null || true)
if [ -z "$schema_version" ] || ! grep -F "const CollectorSchemaVersion = \"$schema_version\"" internal/telemetry/schema.go >/dev/null 2>&1; then
	printf 'telemetry security check: schema version registry is missing or inconsistent\n' >&2
	exit 1
fi
if ! grep -F "otel/opentelemetry-collector-contrib:${schema_version##*-}" "$compose_file" >/dev/null 2>&1; then
	printf 'telemetry security check: Collector image is not pinned to the schema version\n' >&2
	exit 1
fi

service_block() {
	awk -v target="$1" '
		$0 == "  " target ":" { found = 1; next }
		found && /^  [A-Za-z0-9_.-]+:/ { exit }
		found { print }
	' "$compose_file"
}

for service in api console proxy clickhouse; do
	block=$(service_block "$service")
	if printf '%s\n' "$block" | grep -E 'privileged:[[:space:]]*true|^[[:space:]]*-[[:space:]]*/var/run/docker\.sock:' >/dev/null 2>&1; then
		printf 'telemetry security check: %s has an unsafe privilege or Docker socket mount\n' "$service" >&2
		exit 1
	fi
done

collector_block=$(service_block otel-collector)
if printf '%s\n' "$collector_block" | grep -E 'privileged:[[:space:]]*true|^[[:space:]]*-[[:space:]]*/var/run/docker\.sock:' >/dev/null 2>&1; then
	printf 'telemetry security check: main collector has Docker authority\n' >&2
	exit 1
fi
if ! printf '%s\n' "$collector_block" | grep -F '/dev/null:/hostfs/var/run/docker.sock:ro' >/dev/null 2>&1; then
	printf 'telemetry security check: hostmetrics Docker socket mask is missing\n' >&2
	exit 1
fi

docker_metrics_block=$(service_block telemetry-docker)
if printf '%s\n' "$docker_metrics_block" | grep -E '/var/run/docker\.sock|privileged:[[:space:]]*true|group_add:' >/dev/null 2>&1; then
	printf 'telemetry security check: Docker metrics collector has direct or elevated Docker authority\n' >&2
	exit 1
fi
if printf '%s\n' "$docker_metrics_block" | grep -E '^[[:space:]]*ports:|^[[:space:]]*-[[:space:]]*"?[0-9.]+:' >/dev/null 2>&1; then
	printf 'telemetry security check: Docker metrics collector exposes a network listener\n' >&2
	exit 1
fi
docker_proxy_block=$(service_block telemetry-docker-proxy)
if ! printf '%s\n' "$docker_proxy_block" | grep -F '/var/run/docker.sock:/var/run/docker.sock:ro' >/dev/null 2>&1; then
	printf 'telemetry security check: restricted Docker proxy is missing its read-only socket boundary\n' >&2
	exit 1
fi
if printf '%s\n' "$docker_proxy_block" | grep -E 'privileged:[[:space:]]*true|^[[:space:]]*ports:' >/dev/null 2>&1; then
	printf 'telemetry security check: restricted Docker proxy is privileged or publicly published\n' >&2
	exit 1
fi
if ! printf '%s\n' "$docker_proxy_block" | grep -F 'expose: ["2375"]' >/dev/null 2>&1; then
	printf 'telemetry security check: restricted Docker proxy is not on the internal metrics network\n' >&2
	exit 1
fi

for setup_file in compose.setup.yaml Dockerfile; do
	if [ ! -f "$setup_file" ]; then
		printf 'telemetry security check: missing %s\n' "$setup_file" >&2
		exit 1
	fi
done

if ! grep -F 'FROM runtime-base AS telemetry-docker-proxy' Dockerfile >/dev/null 2>&1 || ! grep -F 'telemetry-docker-proxy' Dockerfile >/dev/null 2>&1; then
	printf 'telemetry security check: restricted Docker proxy image target is missing\n' >&2
	exit 1
fi

setup_block=$(awk '
	/^  setup:/ { found = 1; next }
	found && /^  [A-Za-z0-9_.-]+:/ { exit }
	found { print }
' compose.setup.yaml)
if printf '%s\n' "$setup_block" | grep -E '/var/run/docker\.sock|privileged:[[:space:]]*true' >/dev/null 2>&1; then
	printf 'telemetry security check: setup service has Docker authority\n' >&2
	exit 1
fi

if ! awk '/^FROM runtime-base AS setup$/ { found = 1; next } found && /^FROM / { exit } found' Dockerfile | grep -E 'docker-cli|docker[[:space:]]+compose|/usr/bin/docker' >/dev/null 2>&1; then
	:
else
	printf 'telemetry security check: setup image contains Docker tooling\n' >&2
	exit 1
fi

if awk '
	/^receivers:/ { found = 1; next }
	found && /^processors:/ { exit }
	found { print }
' telemetry/docker-stats.yaml | grep -E '^  otlp:' >/dev/null 2>&1 || ! grep -E '^  docker_stats:' telemetry/docker-stats.yaml >/dev/null 2>&1; then
	printf 'telemetry security check: isolated collector configuration is not metrics-only\n' >&2
	exit 1
fi

if grep -F 'endpoint: unix:///var/run/docker.sock' telemetry/docker-stats.yaml >/dev/null 2>&1; then
	printf 'telemetry security check: Docker stats config still points at the raw socket\n' >&2
	exit 1
fi
if ! grep -F 'telemetry-docker-proxy' telemetry/docker-stats.yaml >/dev/null 2>&1; then
	printf 'telemetry security check: Docker stats proxy endpoint is missing\n' >&2
	exit 1
fi

printf 'telemetry security check: Docker authority is limited to worker and restricted metrics proxy\n'
