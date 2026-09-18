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
cookie_file="$(mktemp "${TMPDIR:-/tmp}/stealth-compose-smoke-cookie.XXXXXX")"
register_response="$(mktemp "${TMPDIR:-/tmp}/stealth-compose-smoke-registration.XXXXXX")"
auth_cookie_header=""
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
	rm -f "$cookie_file" "$register_response"
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

wait_for_admin_rows() {
	local name="$1"
	local path="$2"
	local expected="$3"
	local value=""
	for attempt in $(seq 1 "${SMOKE_ATTEMPTS:-60}"); do
		value="$(curl --fail --silent --show-error --header "Cookie: $auth_cookie_header" --max-time 5 "${api_url%/}${path}" 2>/dev/null || true)"
		if printf '%s' "$value" | grep -F "$expected" >/dev/null 2>&1; then
			printf '%s: Admin API returned the expected row\n' "$name"
			return 0
		fi
		sleep "${SMOKE_INTERVAL_SECONDS:-2}"
	done
	printf '%s Admin API query did not return %s\n' "$name" "$expected" >&2
	printf '%s\n' "$value" | head -c 600 >&2 || true
	return 1
}

verify_collector_storage() {
	"${compose[@]}" run --rm --no-deps --user 10001:10001 otelcol-state-init \
		sh -ec 'state=/var/lib/otelcol/file_storage; test -d "$state"; probe="$state/.compose-smoke-probe"; printf smoke >"$probe"; test "$(cat "$probe")" = smoke; rm -f "$probe"'
	printf 'Collector file_storage is writable as UID 10001\n'
}

write_collector_persistence_probe() {
	"${compose[@]}" run --rm --no-deps --user 10001:10001 otelcol-state-init \
		sh -ec 'state=/var/lib/otelcol; probe="$state/.compose-smoke-persistence-probe"; printf persistent >"$probe"; test "$(cat "$probe")" = persistent'
	printf 'Collector file_storage persistence probe written\n'
}

read_collector_persistence_probe() {
	"${compose[@]}" run --rm --no-deps --user 10001:10001 otelcol-state-init \
		sh -ec 'state=/var/lib/otelcol; probe="$state/.compose-smoke-persistence-probe"; test "$(cat "$probe")" = persistent; rm -f "$probe"'
	printf 'Collector file_storage persistence probe survived restart\n'
}

emit_otlp_signal() {
	local signal="$1"
	local payload="$2"
	"${compose[@]}" exec -T api sh -ec '
		payload="$1"
		signal="$2"
		wget -qO /dev/null \
			--header="Content-Type: application/json" \
			--post-data="$payload" \
			"http://otel-collector:4318/v1/$signal"
	' sh "$payload" "$signal"
}

seal_bootstrap_for_smoke() {
	# The release smoke database has no real GitHub owner. Sealing this
	# disposable database lets the normal account-registration route create a
	# short-lived test admin without bypassing the production HTTP path.
	"${compose[@]}" exec -T postgres sh -ec \
		'psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --command "UPDATE instance_bootstrap SET sealed_at=COALESCE(sealed_at, now()) WHERE id=TRUE"'
}

"${compose[@]}" up -d postgres redis clickhouse
wait_for_healthy postgres
wait_for_healthy redis
wait_for_healthy clickhouse
"${compose[@]}" up migrate
seal_bootstrap_for_smoke
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

api_url="http://${api_endpoint}"

API_URL="http://${api_endpoint}" \
CONSOLE_URL="http://${console_endpoint}" \
PROXY_URL="http://${proxy_endpoint}" \
"$repo_root/scripts/production-smoke.sh"

smoke_marker="compose-smoke-$(date -u +%Y%m%d%H%M%S)-$$"
smoke_email="${smoke_marker}@example.test"
smoke_password='correct-horse-battery-staple'
metric_name='stealth.compose.smoke'
timestamp_seconds="$(date -u +%s)"
timestamp_ns="$((timestamp_seconds * 1000000000))"
start_timestamp_ns="$((timestamp_ns - 1000000000))"
trace_id="$(printf '%s' "$smoke_marker" | sha256sum | cut -c1-32)"
span_id="$(printf '%s-span' "$smoke_marker" | sha256sum | cut -c1-16)"

register_status="$(curl --silent --show-error --max-time 10 \
	--cookie-jar "$cookie_file" \
	--header 'Content-Type: application/json' \
	--data-raw "{\"email\":\"$smoke_email\",\"password\":\"$smoke_password\",\"organization_name\":\"Compose smoke\"}" \
	--output "$register_response" \
	--write-out '%{http_code}' \
	"${api_url%/}/v1/account/registrations")"
if [ "$register_status" != '201' ]; then
	printf 'smoke admin account registration returned HTTP %s\n' "$register_status" >&2
	sed -n '1,80p' "$register_response" >&2
	exit 1
fi
auth_cookie_header="$(awk '
BEGIN { separator = "" }
{
    domain = $1
    if (domain ~ /^#HttpOnly_/) {
        sub(/^#HttpOnly_/, "", domain)
    } else if (domain ~ /^#/) {
        next
    }
    if (NF >= 7) {
        printf "%s%s=%s", separator, $6, $7
        separator = "; "
    }
}' "$cookie_file")"
if [ -z "$auth_cookie_header" ]; then
	printf 'smoke admin account did not return a session cookie\n' >&2
	exit 1
fi

"${compose[@]}" exec -T postgres sh -ec \
	'email="$1"; case "$email" in *[!A-Za-z0-9.@_-]*) printf "invalid smoke email\n" >&2; exit 2;; esac; psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 --command "INSERT INTO instance_roles (account_id, role) SELECT id, '\''instance_admin'\'' FROM accounts WHERE email = '\''$email'\'' ON CONFLICT (account_id) DO UPDATE SET role = EXCLUDED.role"' \
	sh "$smoke_email"
role_count="$("${compose[@]}" exec -T postgres sh -ec \
	'email="$1"; case "$email" in *[!A-Za-z0-9.@_-]*) printf "invalid smoke email\n" >&2; exit 2;; esac; psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 --tuples-only --no-align --command "SELECT count(*) FROM instance_roles WHERE account_id = (SELECT id FROM accounts WHERE email = '\''$email'\'') AND role = '\''instance_admin'\''"' \
	sh "$smoke_email" | tr -d '[:space:]')"
if [ "$role_count" != '1' ]; then
	printf 'smoke admin role was not created\n' >&2
	exit 1
fi

filelog_marker="${smoke_marker}-docker-log"
"${compose[@]}" exec -T api sh -ec 'printf "%s\n" "$1" >&2' sh "compose filelog smoke ${filelog_marker}"

log_payload="$(cat <<EOF
{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"compose-smoke"}},{"key":"smoke.marker","value":{"stringValue":"$smoke_marker"}}]},"scopeLogs":[{"scope":{"name":"compose-smoke"},"logRecords":[{"timeUnixNano":"$timestamp_ns","observedTimeUnixNano":"$timestamp_ns","severityNumber":17,"severityText":"ERROR","body":{"stringValue":"compose smoke log $smoke_marker"},"attributes":[{"key":"smoke.marker","value":{"stringValue":"$smoke_marker"}}],"traceId":"$trace_id","spanId":"$span_id"}]}]}]}
EOF
)"
trace_payload="$(cat <<EOF
{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"compose-smoke"}},{"key":"smoke.marker","value":{"stringValue":"$smoke_marker"}}]},"scopeSpans":[{"scope":{"name":"compose-smoke"},"spans":[{"traceId":"$trace_id","spanId":"$span_id","name":"compose smoke","kind":"SPAN_KIND_SERVER","startTimeUnixNano":"$start_timestamp_ns","endTimeUnixNano":"$timestamp_ns","attributes":[{"key":"smoke.marker","value":{"stringValue":"$smoke_marker"}}],"status":{"code":"STATUS_CODE_OK"}}]}]}]}
EOF
)"
metric_payload="$(cat <<EOF
{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"compose-smoke"}},{"key":"smoke.marker","value":{"stringValue":"$smoke_marker"}}]},"scopeMetrics":[{"scope":{"name":"compose-smoke"},"metrics":[{"name":"$metric_name","description":"Compose smoke metric","unit":"1","gauge":{"dataPoints":[{"timeUnixNano":"$timestamp_ns","asDouble":1,"attributes":[{"key":"smoke.marker","value":{"stringValue":"$smoke_marker"}}]}]}}]}]}]}
EOF
)"

# Exercise the actual OTLP exporter path with one authenticated, correlated
# log, trace, and metric. The Admin API checks below prove that the same rows
# are visible through the production authorization/query boundary.
emit_otlp_signal logs "$log_payload"
emit_otlp_signal traces "$trace_payload"
emit_otlp_signal metrics "$metric_payload"

from="$(date -u -d '5 minutes ago' +%Y-%m-%dT%H:%M:%SZ)"
to="$(date -u -d '1 minute' +%Y-%m-%dT%H:%M:%SZ)"
wait_for_telemetry_rows "smoke metric" "SELECT count() FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE AND MetricName = '${metric_name}' AND ResourceAttributes['smoke.marker'] = '${smoke_marker}'"
wait_for_telemetry_rows "smoke log" "SELECT count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE AND positionCaseInsensitiveUTF8(Body, '${smoke_marker}') > 0"
wait_for_telemetry_rows "smoke trace" "SELECT count() FROM otel_traces WHERE Timestamp >= now() - INTERVAL 10 MINUTE AND TraceId = '${trace_id}'"
wait_for_telemetry_rows "Docker file log" "SELECT count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE AND SeverityText = 'WARN' AND positionCaseInsensitiveUTF8(Body, '${filelog_marker}') > 0"

wait_for_admin_rows "logs" "/v1/admin/telemetry/logs?from=${from}&to=${to}&service=compose-smoke&level=ERROR&query=${smoke_marker}&limit=10" "$smoke_marker"
wait_for_admin_rows "traces" "/v1/admin/telemetry/traces?from=${from}&to=${to}&service=compose-smoke&trace_id=${trace_id}&limit=10" "$trace_id"
wait_for_admin_rows "metrics" "/v1/admin/telemetry/metrics?from=${from}&to=${to}&service=compose-smoke&name=${metric_name}&limit=10" "$smoke_marker"

# Exercise the container receiver and verify each signal family it is expected
# to emit. The union keeps this independent of whether a receiver release
# classifies a particular instrument as gauge or sum.
wait_for_telemetry_rows "container metrics" "SELECT count() FROM (SELECT MetricName, ResourceAttributes FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE UNION ALL SELECT MetricName, ResourceAttributes FROM otel_metrics_sum WHERE TimeUnix >= now() - INTERVAL 10 MINUTE) WHERE mapContains(ResourceAttributes, 'container.id')"
for metric in \
	container.cpu.usage.total \
	container.memory.usage.total \
	container.network.io.usage.rx_bytes \
	container.blockio.io_service_bytes_recursive; do
	wait_for_telemetry_rows "${metric}" "SELECT count() FROM (SELECT MetricName, ResourceAttributes FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE UNION ALL SELECT MetricName, ResourceAttributes FROM otel_metrics_sum WHERE TimeUnix >= now() - INTERVAL 10 MINUTE) WHERE MetricName = '${metric}' AND mapContains(ResourceAttributes, 'container.id')"
done
# The receiver intentionally emits the aggregate state metric without a
# per-container resource. Health status is emitted per container that has a
# Docker healthcheck, which the Compose stack provides for its core services.
wait_for_telemetry_rows "container.state.status" "SELECT count() FROM (SELECT MetricName, Attributes FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE UNION ALL SELECT MetricName, Attributes FROM otel_metrics_sum WHERE TimeUnix >= now() - INTERVAL 10 MINUTE) WHERE MetricName = 'container.state.status' AND Attributes['container.state.status'] != ''"
wait_for_telemetry_rows "container.state.health.status" "SELECT count() FROM (SELECT MetricName, Attributes, ResourceAttributes FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE UNION ALL SELECT MetricName, Attributes, ResourceAttributes FROM otel_metrics_sum WHERE TimeUnix >= now() - INTERVAL 10 MINUTE) WHERE MetricName = 'container.state.health.status' AND mapContains(ResourceAttributes, 'container.id') AND Attributes['container.state.health.state'] != ''"

verify_collector_storage

# The schema registry is created by the API and is a small persistence
# sentinel. Restart ClickHouse, wait for the real healthcheck, and verify the
# sentinel plus the exact smoke rows remain available on the named volume.
wait_for_telemetry_rows "schema registry" "SELECT count() FROM telemetry_schema_migrations WHERE version = 'otel-clickhouse-exporter-0.161.0'"
"${compose[@]}" restart clickhouse
wait_for_healthy clickhouse
wait_for_telemetry_rows "schema registry after restart" "SELECT count() FROM telemetry_schema_migrations WHERE version = 'otel-clickhouse-exporter-0.161.0'"
wait_for_telemetry_rows "smoke metric after restart" "SELECT count() FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE AND MetricName = '${metric_name}' AND ResourceAttributes['smoke.marker'] = '${smoke_marker}'"
wait_for_telemetry_rows "smoke log after restart" "SELECT count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE AND positionCaseInsensitiveUTF8(Body, '${smoke_marker}') > 0"
wait_for_telemetry_rows "smoke trace after restart" "SELECT count() FROM otel_traces WHERE Timestamp >= now() - INTERVAL 10 MINUTE AND TraceId = '${trace_id}'"
wait_for_telemetry_rows "Docker file log after restart" "SELECT count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE AND SeverityText = 'WARN' AND positionCaseInsensitiveUTF8(Body, '${filelog_marker}') > 0"
wait_for_admin_rows "traces after ClickHouse restart" "/v1/admin/telemetry/traces?from=${from}&to=${to}&service=compose-smoke&trace_id=${trace_id}&limit=10" "$trace_id"

write_collector_persistence_probe
"${compose[@]}" restart otel-collector
wait_for_healthy otel-collector
verify_collector_storage
read_collector_persistence_probe

printf 'Compose telemetry ingestion and ClickHouse persistence smoke checks passed\n'
