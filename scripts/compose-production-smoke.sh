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
filelog_smoke_pid=""
core_file="$(dirname -- "$compose_file")/traefik/dynamic/core.yaml"
core_backup=""
core_modified="false"
cleanup() {
	local exit_code=$?
	if [ -n "$filelog_smoke_pid" ]; then
		kill "$filelog_smoke_pid" 2>/dev/null || true
		wait "$filelog_smoke_pid" 2>/dev/null || true
		filelog_smoke_pid=""
	fi
	if [ "$exit_code" -ne 0 ]; then
		printf 'Compose smoke failed; collecting bounded diagnostics\n' >&2
		"${compose[@]}" ps >&2 || true
		"${compose[@]}" logs --tail=80 clickhouse otelcol-state-init telemetry-docker-logs-state-init otel-collector telemetry-host telemetry-docker-logs telemetry-docker-proxy telemetry-docker api worker migrate console proxy traefik >&2 || true
	fi
	if [ "${SMOKE_REMOVE_VOLUMES:-false}" = "true" ]; then
		"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
	else
		"${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
	fi
	if [ "$core_modified" = "true" ] && [ -n "$core_backup" ]; then
		cp -- "$core_backup" "$core_file" || true
	fi
	rm -f "$cookie_file" "$register_response"
	if [ -n "$core_backup" ]; then
		rm -f "$core_backup"
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

public_url="$(awk -F= '$1 == "PUBLIC_APP_URL" { value = substr($0, index($0, "=") + 1) } END { print value }' "$env_file")"
traefik_host="$(python3 - "$public_url" <<'PY'
import sys
from urllib.parse import urlparse

parsed = urlparse(sys.argv[1])
if not parsed.hostname:
    raise SystemExit("PUBLIC_APP_URL has no hostname")
print(parsed.hostname.lower())
PY
)"
if grep -Fq '__STEALTH_PUBLIC_HOST__' "$core_file"; then
	core_backup="$(mktemp "${TMPDIR:-/tmp}/stealth-compose-smoke-core.XXXXXX")"
	cp -- "$core_file" "$core_backup"
	python3 - "$core_file" "$traefik_host" <<'PY'
import os
import sys

path, host = sys.argv[1:]
with open(path, encoding="utf-8") as source:
    contents = source.read()
contents = contents.replace("__STEALTH_PUBLIC_HOST__", host)
temporary = path + ".smoke.tmp"
with open(temporary, "w", encoding="utf-8") as target:
    target.write(contents)
    target.flush()
    os.fsync(target.fileno())
os.replace(temporary, path)
PY
	core_modified="true"
fi

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
	# CLICKHOUSE_DB is set by the ClickHouse Compose service from the same
	# CLICKHOUSE_DATABASE value used by the Collector and API. clickhouse-client
	# otherwise defaults to the `default` database, where telemetry tables do
	# not exist.
	"${compose[@]}" exec -T clickhouse sh -ec 'clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --database "$CLICKHOUSE_DB" --query "$1"' sh "$query"
}

last_telemetry_query=""
last_telemetry_query_error=""
last_telemetry_query_result=""

print_bounded_diagnostic() {
	local label="$1"
	local output="$2"
	local maximum="${SMOKE_DIAGNOSTIC_BYTES:-4000}"
	printf '%s\n' "--- ${label} ---" >&2
	if [ -n "$output" ]; then
		printf '%s\n' "$output" | head -c "$maximum" >&2 || true
		printf '\n' >&2
	else
		printf '<none>\n' >&2
	fi
}

print_telemetry_diagnostic_query() {
	local label="$1"
	local query="$2"
	local output
	if output="$(clickhouse_query "$query" 2>&1)"; then
		print_bounded_diagnostic "$label" "$output"
	else
		print_bounded_diagnostic "$label (query failed)" "$output"
	fi
}

print_collector_export_diagnostics() {
	local output
	output="$(
		"${compose[@]}" logs --no-log-prefix --tail=160 otel-collector telemetry-host telemetry-docker-logs telemetry-docker 2>&1 |
			grep -Ei 'error|warn|fail|retry|queue|export|metric' |
			tail -80 || true
	)"
	print_bounded_diagnostic "Collector exporter warnings/errors (all Collector services, filtered, last 80 lines)" "$output"
}

print_collector_self_telemetry() {
	local output
	output="$(
		"${compose[@]}" exec -T api sh -ec 'wget -qO- -T 5 http://otel-collector:8888/metrics' 2>&1 |
			grep -E '^otelcol_(receiver_(accepted|refused|failed)_metric_points|exporter_((sent|enqueue_failed)_metric_points|queue_size|queue_capacity|in_flight_requests))' |
			head -120 || true
	)"
	print_bounded_diagnostic "Collector self-telemetry (pinned release metric counters)" "$output"
}

print_docker_filelog_diagnostics() {
	local output service_container project_name container_ids container_id log_path probe_uid
	service_container="$("${compose[@]}" ps -q api 2>/dev/null || true)"
	project_name=""
	if [ -n "$service_container" ]; then
		project_name="$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' "$service_container" 2>/dev/null || true)"
	fi
	output="$(
		{
			docker info --format 'Docker root={{.DockerRootDir}} logging_driver={{.LoggingDriver}} security={{json .SecurityOptions}}' 2>&1 || true
			printf 'Compose project: %s\n' "${project_name:-unknown}"
			for probe_uid in 0:0 10001:10001; do
				printf 'Container read probe uid=%s:\n' "$probe_uid"
				docker run --rm --log-driver=none --network none --user "$probe_uid" \
					--volume /var/lib/docker/containers:/hostfs:ro alpine:3.24 \
					sh -ec 'id; first="$(find /hostfs -maxdepth 2 -type f -name "*-json.log" -print -quit 2>/dev/null || true)"; if [ -n "$first" ]; then stat -c "%A %a %U:%G %s %n" "$first"; stat -c "parent=%A %a %U:%G %n" "$(dirname "$first")"; else printf "no readable Docker JSON log file\\n"; fi' || true
			done
			printf 'Container read probe uid=10001:10001 supplementary group=0:\n'
			docker run --rm --log-driver=none --network none --user 10001:10001 --group-add 0 \
				--volume /var/lib/docker/containers:/hostfs:ro alpine:3.24 \
				sh -ec 'id; first="$(find /hostfs -maxdepth 2 -type f -name "*-json.log" -print -quit 2>/dev/null || true)"; if [ -n "$first" ]; then stat -c "%A %a %U:%G %s %n" "$first"; stat -c "parent=%A %a %U:%G %n" "$(dirname "$first")"; else printf "no readable Docker JSON log file\\n"; fi' || true
			printf 'Container read probe uid=10001:10001 DAC_READ_SEARCH:\n'
			docker run --rm --log-driver=none --network none --user 10001:10001 \
				--cap-drop ALL --cap-add DAC_READ_SEARCH \
				--volume /var/lib/docker/containers:/hostfs:ro alpine:3.24 \
				sh -ec 'id; grep Cap /proc/self/status; first="$(find /hostfs -maxdepth 2 -type f -name "*-json.log" -print -quit 2>/dev/null || true)"; if [ -n "$first" ]; then stat -c "%A %a %U:%G %s %n" "$first"; stat -c "parent=%A %a %U:%G %n" "$(dirname "$first")"; else printf "no readable Docker JSON log file\\n"; fi' || true
			if [ -n "$project_name" ]; then
				container_ids="$(docker ps -aq --filter "label=com.docker.compose.project=$project_name" 2>/dev/null || true)"
			else
				container_ids="$service_container"
			fi
			for container_id in $container_ids; do
				docker inspect --format '{{.Name}} log_driver={{.HostConfig.LogConfig.Type}} log_path={{.LogPath}} user={{.Config.User}} groups={{json .HostConfig.GroupAdd}} caps={{json .HostConfig.CapAdd}} status={{.State.Status}}' "$container_id" 2>&1 || true
				log_path="$(docker inspect --format '{{.LogPath}}' "$container_id" 2>/dev/null || true)"
				if [ -n "$log_path" ] && [ -e "$log_path" ]; then
					stat --format='log_file=%n mode=%A owner=%U:%G bytes=%s' "$log_path" 2>&1 || true
				fi
			done
			printf 'Docker JSON log files (bounded):\n'
			find /var/lib/docker/containers -maxdepth 2 -type f -name '*-json.log' \
				-printf '%M %u:%g %s %p\n' 2>/dev/null | head -40 || true
		}
	)"
	print_bounded_diagnostic "Docker file-log runtime (driver/path/permissions)" "$output"
}

print_telemetry_diagnostics() {
	local table
	printf 'Telemetry diagnostics for %s\n' "$1" >&2
	print_bounded_diagnostic "last ClickHouse query" "$last_telemetry_query"
	print_bounded_diagnostic "last ClickHouse query error" "${last_telemetry_query_error:-query succeeded; last result: ${last_telemetry_query_result:-unknown}}"
	print_telemetry_diagnostic_query "SHOW TABLES" 'SHOW TABLES'
	print_telemetry_diagnostic_query "DESCRIBE TABLE otel_logs" 'DESCRIBE TABLE otel_logs'
	print_telemetry_diagnostic_query "recent log count otel_logs" "SELECT count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE"
	print_telemetry_diagnostic_query "recent log severities otel_logs" "SELECT SeverityText, count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE GROUP BY SeverityText ORDER BY count() DESC LIMIT 20"
	print_telemetry_diagnostic_query "recent log samples otel_logs" "SELECT Timestamp, SeverityText, ServiceName, substring(Body, 1, 240) FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE ORDER BY Timestamp DESC LIMIT 20"
	for table in otel_metrics_gauge otel_metrics_sum otel_metrics_histogram otel_metrics_summary otel_metrics_exp_histogram; do
		print_telemetry_diagnostic_query "DESCRIBE TABLE ${table}" "DESCRIBE TABLE ${table}"
		print_telemetry_diagnostic_query "recent row count ${table}" "SELECT count() FROM ${table} WHERE TimeUnix >= now() - INTERVAL 10 MINUTE"
		print_telemetry_diagnostic_query "sample metric names ${table}" "SELECT MetricName, count() FROM ${table} WHERE TimeUnix >= now() - INTERVAL 10 MINUTE GROUP BY MetricName ORDER BY count() DESC LIMIT 20"
	done
	print_collector_export_diagnostics
	print_collector_self_telemetry
	print_docker_filelog_diagnostics
}

start_docker_filelog_smoke() {
	local marker="$1"
	# docker compose exec attaches to an exec session and does not write that
	# session's output to the container's Docker JSON log. Run a short-lived
	# process as the container's main process so file_log/docker observes the
	# exact log driver path used in production.
	"${compose[@]}" run --rm --no-deps --entrypoint sh api -ec '
		printf "compose filelog probe ready\n" >&2
		sleep "$2"
		printf "%s\n" "$1" >&2
		sleep "$3"
	' sh "$marker" "${SMOKE_FILELOG_DISCOVERY_DELAY_SECONDS:-5}" "${SMOKE_FILELOG_HOLD_SECONDS:-180}" >/dev/null 2>&1 &
	filelog_smoke_pid=$!
}

container_networks() {
	docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{printf "%s\n" $name}}{{end}}' "$1" | sort
}

network_count() {
	printf '%s\n' "$1" | awk 'NF { count++ } END { print count + 0 }'
}

network_contains() {
	local networks="$1"
	local wanted="$2"
	printf '%s\n' "$networks" | grep -Fqx -- "$wanted"
}

telemetry_ingest_network_name() {
	local configured
	configured="$(awk -F= '$1 == "STEALTH_TELEMETRY_INGEST_NETWORK_NAME" { print substr($0, index($0, "=") + 1); exit }' "$env_file")"
	configured="${configured%$'\r'}"
	if [ -n "$configured" ]; then
		printf '%s\n' "$configured"
		return
	fi
	printf '%s\n' 'stealth_telemetry_ingest'
}

traefik_ingress_network_name() {
	local configured
	configured="$(awk -F= '$1 == "STEALTH_INGRESS_NETWORK_NAME" { print substr($0, index($0, "=") + 1); exit }' "$env_file")"
	configured="${configured%$'\r'}"
	if [ -n "$configured" ]; then
		printf '%s\n' "$configured"
		return
	fi
	printf '%s\n' 'stealth_ingress'
}

verify_traefik_runtime_boundaries() {
	local container networks mounts caps security_opt user ingress_network service service_container
	ingress_network="$(traefik_ingress_network_name)"
	container="$("${compose[@]}" ps -q traefik)"
	if [ -z "$container" ]; then
		printf 'missing Traefik container\n' >&2
		return 1
	fi
	networks="$(container_networks "$container")"
	if [ "$(network_count "$networks")" -ne 1 ] || ! network_contains "$networks" "$ingress_network"; then
		printf 'Traefik must join only the dedicated ingress network: %s\n' "$networks" >&2
		return 1
	fi
	if [ "$(docker network inspect --format '{{.Internal}}' "$ingress_network")" != "true" ]; then
		printf 'Traefik ingress network must be internal: %s\n' "$ingress_network" >&2
		return 1
	fi
	mounts="$(docker inspect --format '{{range .Mounts}}{{printf "%s=%t " .Destination .RW}}{{end}}' "$container")"
	case "$mounts" in
		*'/etc/traefik/traefik.yaml=false '*|*'/etc/traefik/traefik.yaml=false') ;;
		*) printf 'Traefik static config is not mounted read-only: %s\n' "$mounts" >&2; return 1 ;;
	esac
	case "$mounts" in
		*'/etc/traefik/dynamic=false '*|*'/etc/traefik/dynamic=false') ;;
		*) printf 'Traefik dynamic config is not mounted read-only: %s\n' "$mounts" >&2; return 1 ;;
	esac
	caps="$(docker inspect --format '{{json .HostConfig.CapAdd}} {{json .HostConfig.CapDrop}}' "$container")"
	case "$caps" in
		*'"ALL"'*) ;;
		*) printf 'Traefik does not drop all Linux capabilities: %s\n' "$caps" >&2; return 1 ;;
	esac
	security_opt="$(docker inspect --format '{{json .HostConfig.SecurityOpt}}' "$container")"
	case "$security_opt" in
		*'no-new-privileges:true'*) ;;
		*) printf 'Traefik is missing no-new-privileges: %s\n' "$security_opt" >&2; return 1 ;;
	esac
	user="$(docker inspect --format '{{.Config.User}}' "$container")"
	if [ "$user" != "65532:65532" ]; then
		printf 'Traefik user = %q, want non-root 65532:65532\n' "$user" >&2
		return 1
	fi
	for service in api console; do
		service_container="$("${compose[@]}" ps -q "$service")"
		if ! network_contains "$(container_networks "$service_container")" "$ingress_network"; then
			printf '%s is not attached to the Traefik ingress network\n' "$service" >&2
			return 1
		fi
	done
	for service in proxy worker otel-collector telemetry-host telemetry-docker-logs telemetry-docker telemetry-docker-proxy clickhouse postgres redis; do
		service_container="$("${compose[@]}" ps -q "$service" 2>/dev/null || true)"
		if [ -n "$service_container" ] && network_contains "$(container_networks "$service_container")" "$ingress_network"; then
			printf 'prohibited service %s is attached to the Traefik ingress network\n' "$service" >&2
			return 1
		fi
	done
	printf 'Traefik security, health, and network boundaries passed\n'
}

traefik_http_status() {
	local path="$1" host="$2" cookie_header="${3:-}" output status
	output="$("${compose[@]}" exec -T api sh -ec '
		path="$1"; host="$2"; cookie="$3"
		set -- --header "Host: $host"
		if [ -n "$cookie" ]; then set -- "$@" --header "Cookie: $cookie"; fi
		wget -S -O /dev/null --timeout=8 "$@" "http://traefik:8080$path" 2>&1 || true
	' sh "$path" "$host" "$cookie_header")"
	status="$(printf '%s\n' "$output" | awk '/HTTP\/[0-9.]+/ { code=$2 } END { gsub(/\r/, "", code); print code }')"
	printf '%s\n' "$status"
}

verify_traefik_routing() {
	local status sse_headers
	if ! "${compose[@]}" exec -T api sh -ec 'wget -qO- --timeout=8 --header="Host: $1" "http://traefik:8080/healthz" >/dev/null' sh "$traefik_host"; then
		printf 'Traefik API health route did not return successfully\n' >&2
		return 1
	fi
	if ! "${compose[@]}" exec -T api sh -ec 'wget -qO- --timeout=8 --header="Host: $1" "http://traefik:8080/" >/dev/null' sh "$traefik_host"; then
		printf 'Traefik Console route did not return successfully\n' >&2
		return 1
	fi
	if ! "${compose[@]}" exec -T api sh -ec 'wget -qO- --timeout=8 --header="Host: $1" --header="Cookie: $2" "http://traefik:8080/v1/account" >/dev/null' sh "$traefik_host" "$auth_cookie_header"; then
		printf 'Traefik API path-preservation request failed\n' >&2
		return 1
	fi
	status="$(traefik_http_status /not-a-real-route "$traefik_host")"
	if [ "$status" != "404" ]; then
		printf 'Traefik unknown path status = %q, want 404\n' "$status" >&2
		return 1
	fi
	# A router scoped to PUBLIC_APP_URL must reject an unrelated Host header.
	status="$(traefik_http_status / unknown.example.invalid "")"
	if [ "$status" != "404" ]; then
		printf 'Traefik unknown host status = %q, want 404\n' "$status" >&2
		return 1
	fi
	status="$(traefik_http_status /dashboard/ "$traefik_host")"
	if [ "$status" != "404" ]; then
		printf 'Traefik dashboard probe status = %q, want 404\n' "$status" >&2
		return 1
	fi
	sse_headers="$("${compose[@]}" exec -T api sh -ec '
		wget -S -O /dev/null --timeout=5 \
			--header="Host: $1" \
			--header="Accept: text/event-stream" \
			--header="Cookie: $2" \
			http://traefik:8080/v1/admin/realtime 2>&1 || true
	' sh "$traefik_host" "$auth_cookie_header")"
	if ! printf '%s\n' "$sse_headers" | grep -Eq 'HTTP/[0-9.]+ 200'; then
		printf 'Traefik Admin realtime did not establish HTTP 200:\n%s\n' "$sse_headers" >&2
		return 1
	fi
	if ! printf '%s\n' "$sse_headers" | grep -Eiq 'content-type:.*text/event-stream'; then
		printf 'Traefik Admin realtime did not preserve SSE content type:\n%s\n' "$sse_headers" >&2
		return 1
	fi
	printf 'Traefik core API, Console, fail-closed, and SSE routing passed\n'
}

verify_telemetry_runtime_boundaries() {
	local service container mounts caps networks
	local collector_networks="" host_networks="" docker_logs_networks="" docker_metrics_networks="" docker_proxy_networks=""
	local clickhouse_networks api_networks worker_networks ingest_network clickhouse_network proxy_network
	for service in otel-collector telemetry-host telemetry-docker-logs telemetry-docker telemetry-docker-proxy; do
		container="$("${compose[@]}" ps -q "$service")"
		if [ -z "$container" ]; then
			printf 'missing telemetry container: %s\n' "$service" >&2
			return 1
		fi
		mounts="$(docker inspect --format '{{range .Mounts}}{{printf "%s->%s " .Source .Destination}}{{end}}' "$container")"
		caps="$(docker inspect --format '{{json .HostConfig.CapAdd}}' "$container")"
		networks="$(container_networks "$container")"
		case "$service" in
			otel-collector)
				collector_networks="$networks"
				case "$mounts" in
					*'->/hostfs'*|*'/var/lib/docker/containers->/hostfs/var/lib/docker/containers'*|*'/var/run/docker.sock->'*)
						printf 'main Collector has an unexpected host mount: %s\n' "$mounts" >&2
						return 1
					;;
				esac
				case "$caps" in
					*DAC_READ_SEARCH*)
						printf 'main Collector has DAC_READ_SEARCH: %s\n' "$caps" >&2
						return 1
					;;
				 esac
				;;
			telemetry-host)
				host_networks="$networks"
				case "$mounts" in
					*'/->/hostfs '*) ;;
					*)
						printf 'host metrics Collector has an unexpected mount: %s\n' "$mounts" >&2
						return 1
					;;
				esac
				case "$caps" in
					*DAC_READ_SEARCH*)
						printf 'host metrics Collector has DAC_READ_SEARCH: %s\n' "$caps" >&2
						return 1
					;;
					esac
				;;
			telemetry-docker-logs)
				docker_logs_networks="$networks"
				case "$mounts" in
					*'/var/lib/docker/containers->/hostfs/var/lib/docker/containers'*) ;;
					*)
						printf 'Docker log Collector is missing its narrow host mount: %s\n' "$mounts" >&2
						return 1
					;;
				 esac
				case "$mounts" in
					*'/->/hostfs '*)
						printf 'Docker log Collector has an overly broad host-root mount: %s\n' "$mounts" >&2
						return 1
					;;
					*'/var/run/docker.sock->'*)
						printf 'Docker log Collector has an overly broad mount: %s\n' "$mounts" >&2
						return 1
					;;
				esac
				case "$caps" in
					*DAC_READ_SEARCH*) ;;
					*)
						printf 'Docker log Collector lacks DAC_READ_SEARCH: %s\n' "$caps" >&2
						return 1
					;;
				 esac
				;;
			telemetry-docker)
				docker_metrics_networks="$networks"
				case "$mounts" in
					*'/var/run/docker.sock->'*)
						printf 'Docker metrics Collector has a Docker socket: %s\n' "$mounts" >&2
						return 1
					;;
				 esac
				;;
			telemetry-docker-proxy)
				docker_proxy_networks="$networks"
				case "$mounts" in
					*'/var/run/docker.sock->/var/run/docker.sock'*) ;;
					*)
						printf 'Docker proxy is missing its socket mount: %s\n' "$mounts" >&2
						return 1
					;;
				 esac
				;;
		esac
	done

	ingest_network="$(telemetry_ingest_network_name)"
	if [ "$(network_count "$host_networks")" -ne 1 ] || ! network_contains "$host_networks" "$ingest_network"; then
		printf 'host metrics Collector must join only the configured ingest network: expected=%q actual=%q\n' "$ingest_network" "$host_networks" >&2
		return 1
	fi
	if [ "$(network_count "$docker_logs_networks")" -ne 1 ] || ! network_contains "$docker_logs_networks" "$ingest_network"; then
		printf 'Docker log Collector must join only the configured ingest network: expected=%q actual=%q\n' "$ingest_network" "$docker_logs_networks" >&2
		return 1
	fi
	if [ "$(docker network inspect --format '{{.Internal}}' "$ingest_network")" != "true" ]; then
		printf 'telemetry ingest network must be internal: %s\n' "$ingest_network" >&2
		return 1
	fi
	if [ "$(network_count "$collector_networks")" -ne 3 ] || ! network_contains "$collector_networks" "$ingest_network"; then
		printf 'main Collector network boundary is incorrect: %s\n' "$collector_networks" >&2
		return 1
	fi

	container="$("${compose[@]}" ps -q clickhouse)"
	clickhouse_networks="$(container_networks "$container")"
	container="$("${compose[@]}" ps -q api)"
	api_networks="$(container_networks "$container")"
	container="$("${compose[@]}" ps -q worker)"
	worker_networks="$(container_networks "$container")"
	if [ "$(network_count "$clickhouse_networks")" -ne 1 ]; then
		printf 'ClickHouse network boundary is incorrect: %s\n' "$clickhouse_networks" >&2
		return 1
	fi
	clickhouse_network="$(printf '%s\n' "$clickhouse_networks" | awk 'NF { print; exit }')"
	if ! network_contains "$collector_networks" "$clickhouse_network" || network_contains "$host_networks" "$clickhouse_network" || network_contains "$docker_logs_networks" "$clickhouse_network"; then
		printf 'isolated Collectors share the ClickHouse network\n' >&2
		return 1
	fi
	if network_contains "$api_networks" "$ingest_network" || network_contains "$worker_networks" "$ingest_network"; then
		printf 'application services share the isolated telemetry ingest network\n' >&2
		return 1
	fi

	if [ "$(network_count "$docker_proxy_networks")" -ne 1 ] || [ "$(network_count "$docker_metrics_networks")" -ne 2 ]; then
		printf 'Docker metrics network count is incorrect: collector=%s proxy=%s\n' "$docker_metrics_networks" "$docker_proxy_networks" >&2
		return 1
	fi
	proxy_network="$(printf '%s\n' "$docker_proxy_networks" | awk 'NF { print; exit }')"
	if [ "$proxy_network" = "$ingest_network" ] || ! network_contains "$docker_metrics_networks" "$ingest_network" || ! network_contains "$docker_metrics_networks" "$proxy_network"; then
		printf 'Docker metrics Collector does not isolate its proxy and ingest networks\n' >&2
		return 1
	fi
	printf 'Telemetry runtime privilege and network boundaries passed\n'
}

stop_docker_filelog_smoke() {
	if [ -n "$filelog_smoke_pid" ]; then
		kill "$filelog_smoke_pid" 2>/dev/null || true
		wait "$filelog_smoke_pid" 2>/dev/null || true
		filelog_smoke_pid=""
	fi
}

wait_for_telemetry_rows() {
	local name="$1"
	local query="$2"
	local value output
	last_telemetry_query="$query"
	last_telemetry_query_error=""
	last_telemetry_query_result=""
	for attempt in $(seq 1 "${SMOKE_ATTEMPTS:-60}"); do
		if output="$(clickhouse_query "$query" 2>&1)"; then
			value="$(printf '%s' "$output" | tr -d '[:space:]')"
			last_telemetry_query_error=""
		else
			value=""
			last_telemetry_query_error="$output"
		fi
		last_telemetry_query_result="$value"
		if [[ "$value" =~ ^[1-9][0-9]*$ ]]; then
			printf '%s: %s rows\n' "$name" "$value"
			return 0
		fi
		sleep "${SMOKE_INTERVAL_SECONDS:-2}"
	done
	printf '%s telemetry query did not return rows\n' "$name" >&2
	print_telemetry_diagnostics "$name"
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
"${compose[@]}" up -d telemetry-host telemetry-docker-logs
wait_for_healthy telemetry-host
wait_for_healthy telemetry-docker-logs
wait_for_healthy telemetry-docker-proxy
wait_for_healthy telemetry-docker
"${compose[@]}" up -d api worker console proxy traefik
wait_for_healthy api
wait_for_healthy worker
wait_for_healthy console
wait_for_healthy proxy
wait_for_healthy traefik
verify_telemetry_runtime_boundaries
verify_traefik_runtime_boundaries

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

verify_traefik_routing

filelog_marker="${smoke_marker}-docker-log"
start_docker_filelog_smoke "compose filelog smoke ${filelog_marker}"

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
wait_for_telemetry_rows "Docker file log" "SELECT count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE AND SeverityText = 'WARN' AND positionCaseInsensitiveUTF8(Body, '${filelog_marker}') > 0 AND ResourceAttributes['container.id'] != ''"
stop_docker_filelog_smoke

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
wait_for_admin_rows "Docker file log attribution" "/v1/admin/telemetry/logs?from=${from}&to=${to}&query=${filelog_marker}&limit=10" 'container.name'
# The receiver intentionally emits the aggregate state metric without a
# per-container resource. Health status is emitted per container that has a
# Docker healthcheck, which the Compose stack provides for its core services.
wait_for_telemetry_rows "container.state.status" "SELECT count() FROM (SELECT MetricName, Attributes FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE UNION ALL SELECT MetricName, Attributes FROM otel_metrics_sum WHERE TimeUnix >= now() - INTERVAL 10 MINUTE) WHERE MetricName = 'container.state.status' AND Attributes['container.state.status'] != ''"
wait_for_telemetry_rows "container.state.health.status" "SELECT count() FROM (SELECT MetricName, Attributes, ResourceAttributes FROM otel_metrics_gauge WHERE TimeUnix >= now() - INTERVAL 10 MINUTE UNION ALL SELECT MetricName, Attributes, ResourceAttributes FROM otel_metrics_sum WHERE TimeUnix >= now() - INTERVAL 10 MINUTE) WHERE MetricName = 'container.state.health.status' AND mapContains(ResourceAttributes, 'container.id') AND Attributes['container.state.health.state'] != ''"

# hostmetrics' CPU scraper emits the pinned system.cpu.time sum metric. This
# proves the isolated host collector reaches ClickHouse through main OTLP.
wait_for_telemetry_rows "host metrics" "SELECT count() FROM otel_metrics_sum WHERE TimeUnix >= now() - INTERVAL 10 MINUTE AND MetricName = 'system.cpu.time'"

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
wait_for_telemetry_rows "Docker file log after restart" "SELECT count() FROM otel_logs WHERE Timestamp >= now() - INTERVAL 10 MINUTE AND SeverityText = 'WARN' AND positionCaseInsensitiveUTF8(Body, '${filelog_marker}') > 0 AND ResourceAttributes['container.id'] != ''"
wait_for_admin_rows "traces after ClickHouse restart" "/v1/admin/telemetry/traces?from=${from}&to=${to}&service=compose-smoke&trace_id=${trace_id}&limit=10" "$trace_id"

write_collector_persistence_probe
"${compose[@]}" restart otel-collector
wait_for_healthy otel-collector
verify_collector_storage
read_collector_persistence_probe

printf 'Compose telemetry ingestion and ClickHouse persistence smoke checks passed\n'
