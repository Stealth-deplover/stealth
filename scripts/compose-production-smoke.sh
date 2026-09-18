#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${ENV_FILE:-$repo_root/.env.production}"
compose_file="${COMPOSE_FILE:-$repo_root/compose.production.yaml}"

if [ ! -f "$env_file" ]; then
	printf 'environment file not found: %s\n' "$env_file" >&2
	exit 2
fi
if [ ! -f "$compose_file" ]; then
	printf 'compose file not found: %s\n' "$compose_file" >&2
	exit 2
fi

compose=(docker compose --env-file "$env_file" -f "$compose_file")
cleanup() {
	local exit_code=$?
	if [ "$exit_code" -ne 0 ]; then
		printf 'Compose smoke failed; collecting bounded diagnostics\n' >&2
		"${compose[@]}" ps >&2 || true
		"${compose[@]}" logs --tail=80 clickhouse otelcol-state-init otel-collector telemetry-docker-proxy telemetry-docker api worker migrate console proxy >&2 || true
	fi
	if [ "${SMOKE_REMOVE_VOLUMES:-false}" = "true" ]; then
		"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
	else
		"${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
	fi
	exit "$exit_code"
}
trap cleanup EXIT

case "${SMOKE_REMOVE_VOLUMES:-false}" in
	true|false) ;;
	*)
		printf 'SMOKE_REMOVE_VOLUMES must be true or false\n' >&2
		exit 2
		;;
esac

wait_for_healthy() {
	local service="$1"
	local container status
	for attempt in $(seq 1 "${SMOKE_ATTEMPTS:-60}"); do
		container="$("${compose[@]}" ps -q "$service" 2>/dev/null || true)"
		if [ -n "$container" ]; then
			status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container" 2>/dev/null || true)"
			if [ "$status" = "healthy" ]; then
				printf '%s: healthy\n' "$service"
				return 0
			fi
			if [ "$status" = "exited" ] || [ "$status" = "dead" ]; then
				printf '%s exited before becoming healthy\n' "$service" >&2
				return 1
			fi
		fi
		sleep "${SMOKE_INTERVAL_SECONDS:-2}"
	done
	printf '%s did not become healthy\n' "$service" >&2
	return 1
}

clickhouse_query() {
	local query="$1"
	"${compose[@]}" exec -T clickhouse sh -ec 'clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --query "$1"' sh "$query"
}

wait_for_telemetry_rows() {
	local name="$1"
	local query="$2"
	local value
	for attempt in $(seq 1 "${SMOKE_ATTEMPTS:-60}"); do
		value="$(clickhouse_query "$query" 2>/dev/null | tr -d '[:space:]' || true)"
		if [[ "$value" =~ ^[1-9][0-9]*$ ]]; then
			printf '%s: %s rows\n' "$name" "$value"
			return 0
		fi
		sleep "${SMOKE_INTERVAL_SECONDS:-2}"
	done
	printf '%s telemetry query did not return rows\n' "$name" >&2
	return 1
}

"${compose[@]}" up -d postgres redis clickhouse
wait_for_healthy postgres
wait_for_healthy redis
wait_for_healthy clickhouse
"${compose[@]}" up migrate
"${compose[@]}" up -d otel-collector telemetry-docker-proxy telemetry-docker
wait_for_healthy otel-collector
wait_for_healthy telemetry-docker-proxy
wait_for_healthy telemetry-docker
"${compose[@]}" up -d api worker console proxy
wait_for_healthy api
wait_for_healthy worker
wait_for_healthy console
wait_for_healthy proxy

api_endpoint="$("${compose[@]}" port api 8080 | head -n 1)"
console_endpoint="$("${compose[@]}" port console 3000 | head -n 1)"
proxy_endpoint="$("${compose[@]}" port proxy 80 | head -n 1)"

if [ -z "$api_endpoint" ] || [ -z "$console_endpoint" ] || [ -z "$proxy_endpoint" ]; then
	printf 'could not resolve published Compose ports for smoke checks\n' >&2
	exit 1
fi

API_URL="http://${api_endpoint}" \
CONSOLE_URL="http://${console_endpoint}" \
PROXY_URL="http://${proxy_endpoint}" \
"$repo_root/scripts/production-smoke.sh"

# Exercise the actual exporter path, not just Collector startup. API and
# worker startup logs/traces plus host/container metrics must reach ClickHouse.
wait_for_telemetry_rows "metrics" "SELECT count() FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE"
wait_for_telemetry_rows "container metrics" "SELECT count() FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE AND mapContains(ResourceAttributes, 'container.id')"
wait_for_telemetry_rows "logs" "SELECT count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE"
wait_for_telemetry_rows "traces" "SELECT count() FROM otel_traces WHERE Timestamp >= now() - INTERVAL 10 MINUTE"

# The schema registry is created by the API and is a small persistence
# sentinel. Restart ClickHouse, wait for the real healthcheck, and verify the
# sentinel remains available on the named volume.
wait_for_telemetry_rows "schema registry" "SELECT count() FROM telemetry_schema_migrations WHERE version = 'otel-clickhouse-exporter-0.161.0'"
"${compose[@]}" restart clickhouse
wait_for_healthy clickhouse
wait_for_telemetry_rows "schema registry after restart" "SELECT count() FROM telemetry_schema_migrations WHERE version = 'otel-clickhouse-exporter-0.161.0'"

printf 'Compose telemetry ingestion and ClickHouse persistence smoke checks passed\n'
