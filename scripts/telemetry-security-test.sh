#!/bin/sh
set -eu

compose_file=${STEALTH_PRODUCTION_COMPOSE_FILE_FOR_TEST:-compose.production.yaml}

if [ ! -f "$compose_file" ]; then
	printf 'telemetry security check: missing %s\n' "$compose_file" >&2
	exit 1
fi

schema_version=$(tr -d '[:space:]' < telemetry/schema/VERSION 2>/dev/null || true)
collector_version=$(sed -n 's/^const CollectorVersion = "\([^"]*\)"$/\1/p' internal/telemetry/schema.go | head -n 1)
if [ -z "$schema_version" ] || [ -z "$collector_version" ] || [ "$schema_version" != "otel-clickhouse-exporter-$collector_version" ]; then
	printf 'telemetry security check: schema version registry is missing or inconsistent\n' >&2
	exit 1
fi
if ! grep -F "ARG OTEL_COLLECTOR_BASE_IMAGE=otel/opentelemetry-collector-contrib:$collector_version" Dockerfile >/dev/null 2>&1; then
	printf 'telemetry security check: Collector base image is not pinned to the schema version\n' >&2
	exit 1
fi

service_block() {
	awk -v target="$1" '
		$0 == "  " target ":" { found = 1; next }
		found && /^  [A-Za-z0-9_.-]+:/ { exit }
		found { print }
	' "$compose_file"
}

receiver_block() {
	awk '
		/^receivers:/ { found = 1; next }
		found && /^(processors|exporters|service):/ { exit }
		found { print }
	' "$1"
}

for service in api console proxy clickhouse; do
	block=$(service_block "$service")
	if printf '%s\n' "$block" | grep -E 'privileged:[[:space:]]*true|^[[:space:]]*-[[:space:]]*/var/run/docker\.sock:' >/dev/null 2>&1; then
		printf 'telemetry security check: %s has an unsafe privilege or Docker socket mount\n' "$service" >&2
		exit 1
	fi
done

for service in clickhouse otel-collector telemetry-host telemetry-docker-logs telemetry-docker telemetry-docker-proxy; do
	block=$(service_block "$service")
	if printf '%s\n' "$block" | grep -E '^[[:space:]]*ports:' >/dev/null 2>&1; then
		printf 'telemetry security check: %s is publicly published\n' "$service" >&2
		exit 1
	fi
done

collector_block=$(service_block otel-collector)
if printf '%s\n' "$collector_block" | grep -E '/:/hostfs|/var/lib/docker/containers|/var/run/docker\.sock|DAC_READ_SEARCH' >/dev/null 2>&1; then
	printf 'telemetry security check: main Collector has a host mount, Docker socket, or DAC capability\n' >&2
	exit 1
fi
if ! printf '%s\n' "$collector_block" | grep -E '^[[:space:]]*user:[[:space:]]*"?10001:10001"?[[:space:]]*$' >/dev/null 2>&1; then
	printf 'telemetry security check: main Collector must remain UID 10001\n' >&2
	exit 1
fi
if ! printf '%s\n' "$collector_block" | grep -F 'no-new-privileges:true' >/dev/null 2>&1; then
	printf 'telemetry security check: main Collector must retain no-new-privileges\n' >&2
	exit 1
fi

host_metrics_block=$(service_block telemetry-host)
if ! printf '%s\n' "$host_metrics_block" | grep -F '/:/hostfs:ro' >/dev/null 2>&1; then
	printf 'telemetry security check: host metrics Collector is missing its read-only host mount\n' >&2
	exit 1
fi
if printf '%s\n' "$host_metrics_block" | grep -E '/var/lib/docker/containers|/var/run/docker\.sock|DAC_READ_SEARCH' >/dev/null 2>&1; then
	printf 'telemetry security check: host metrics Collector has Docker log/socket authority\n' >&2
	exit 1
fi
if ! printf '%s\n' "$host_metrics_block" | grep -F 'no-new-privileges:true' >/dev/null 2>&1; then
	printf 'telemetry security check: host metrics Collector must retain no-new-privileges\n' >&2
	exit 1
fi

docker_logs_block=$(service_block telemetry-docker-logs)
if ! printf '%s\n' "$docker_logs_block" | grep -F '/var/lib/docker/containers:/hostfs/var/lib/docker/containers:ro' >/dev/null 2>&1; then
	printf 'telemetry security check: Docker log Collector lacks its narrow read-only log mount\n' >&2
	exit 1
fi
if printf '%s\n' "$docker_logs_block" | grep -E '/:/hostfs|/var/run/docker\.sock' >/dev/null 2>&1; then
	printf 'telemetry security check: Docker log Collector has a broad host or socket mount\n' >&2
	exit 1
fi
if ! printf '%s\n' "$docker_logs_block" | grep -E '^[[:space:]]*user:[[:space:]]*"?10001:10001"?[[:space:]]*$' >/dev/null 2>&1; then
	printf 'telemetry security check: Docker log Collector must remain UID 10001\n' >&2
	exit 1
fi
if ! printf '%s\n' "$docker_logs_block" | grep -E '^[[:space:]]*-[[:space:]]*DAC_READ_SEARCH[[:space:]]*$' >/dev/null 2>&1; then
	printf 'telemetry security check: Docker log Collector lacks its dedicated DAC capability\n' >&2
	exit 1
fi
if printf '%s\n' "$docker_logs_block" | grep -F 'no-new-privileges:true' >/dev/null 2>&1; then
	printf 'telemetry security check: Docker log Collector cannot use no-new-privileges with its file capability\n' >&2
	exit 1
fi

docker_metrics_block=$(service_block telemetry-docker)
if printf '%s\n' "$docker_metrics_block" | grep -E '/var/run/docker\.sock|privileged:[[:space:]]*true|group_add:|DAC_READ_SEARCH' >/dev/null 2>&1; then
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

socket_owners=0
for service in otel-collector telemetry-host telemetry-docker-logs telemetry-docker telemetry-docker-proxy; do
	if printf '%s\n' "$(service_block "$service")" | grep -F '/var/run/docker.sock' >/dev/null 2>&1; then
		socket_owners=$((socket_owners + 1))
		if [ "$service" != telemetry-docker-proxy ]; then
			printf 'telemetry security check: %s owns the Docker socket instead of the restricted proxy\n' "$service" >&2
			exit 1
		fi
	fi
done
if [ "$socket_owners" -ne 1 ]; then
	printf 'telemetry security check: expected only telemetry-docker-proxy to own the Docker socket\n' >&2
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
if ! grep -F 'FROM scratch AS telemetry-collector' Dockerfile >/dev/null 2>&1 || ! grep -F 'telemetry-collector-healthcheck' Dockerfile >/dev/null 2>&1; then
	printf 'telemetry security check: capability-free Collector image target or health probe is missing\n' >&2
	exit 1
fi
capability_free_image=$(awk '
	/^FROM scratch AS telemetry-collector$/ { found = 1; next }
	found && /^FROM / { exit }
	found { print }
' Dockerfile)
if printf '%s\n' "$capability_free_image" | grep -E 'setcap|cap_dac_read_search|telemetry-docker-logs-capability' >/dev/null 2>&1; then
	printf 'telemetry security check: main Collector image carries a filesystem bypass capability\n' >&2
	exit 1
fi
if ! grep -F 'FROM scratch AS telemetry-docker-logs' Dockerfile >/dev/null 2>&1 || ! grep -F 'setcap cap_dac_read_search+ep' Dockerfile >/dev/null 2>&1; then
	printf 'telemetry security check: dedicated Docker log capability image target is missing\n' >&2
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

if receiver_block telemetry/docker-stats.yaml | grep -E '^  otlp:' >/dev/null 2>&1 || ! receiver_block telemetry/docker-stats.yaml | grep -E '^  docker_stats:' >/dev/null 2>&1; then
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

if grep -E '(^  hostmetrics:|^  file_log/docker:)' telemetry/otel-collector.yaml >/dev/null 2>&1 || grep -F '/hostfs' telemetry/otel-collector.yaml >/dev/null 2>&1; then
	printf 'telemetry security check: main Collector config still owns host metrics or Docker logs\n' >&2
	exit 1
fi
if ! grep -E '^  hostmetrics:' telemetry/host-metrics.yaml >/dev/null 2>&1 || ! grep -F 'root_path: /hostfs' telemetry/host-metrics.yaml >/dev/null 2>&1; then
	printf 'telemetry security check: host metrics config is missing hostmetrics root_path\n' >&2
	exit 1
fi
if receiver_block telemetry/host-metrics.yaml | grep -E '^  otlp:' >/dev/null 2>&1; then
	printf 'telemetry security check: host metrics collector has an OTLP receiver\n' >&2
	exit 1
fi
if ! grep -E '^  file_log/docker:' telemetry/docker-logs.yaml >/dev/null 2>&1 || ! grep -F '/hostfs/var/lib/docker/containers' telemetry/docker-logs.yaml >/dev/null 2>&1; then
	printf 'telemetry security check: Docker log config is missing its file receiver\n' >&2
	exit 1
fi
if receiver_block telemetry/docker-logs.yaml | grep -E '^  otlp:' >/dev/null 2>&1; then
	printf 'telemetry security check: Docker log collector has an OTLP receiver\n' >&2
	exit 1
fi

printf 'telemetry security check: host, Docker-log, and Docker-metrics Collector boundaries passed\n'
