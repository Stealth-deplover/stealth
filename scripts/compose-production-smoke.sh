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
api_url=""
filelog_smoke_pid=""
platform_archive=""
app_archive=""
app_probe_binary=""
buildkit_wrong_identity_dir=""
buildkit_pki_smoke_created="false"
buildkit_apparmor_profile_file=""
buildkit_apparmor_profile_name=""
buildkit_apparmor_profile_loaded="false"
platform_response="$(mktemp "${TMPDIR:-/tmp}/stealth-compose-smoke-platform.XXXXXX")"
platform_project_id=""
platform_site_id=""
platform_app_id=""
platform_app_deployment_id=""
platform_host=""
platform_app_host=""
platform_base_domain=""
platform_previous_base_domain=""
platform_domain_changed="false"
core_file="$(dirname -- "$compose_file")/traefik/dynamic/core.yaml"
core_backup=""
core_modified="false"
static_file="$(dirname -- "$compose_file")/traefik/traefik.yaml"
cloudflare_state_source="$(dirname -- "$compose_file")/state/setup-state.enc"
cloudflare_import_dir="$(dirname -- "$compose_file")/state/.cloudflare-import"
cloudflare_import_artifact="$cloudflare_import_dir/cloudflare-import.enc"
cloudflare_setup_state_available="false"
if [ -s "$cloudflare_state_source" ]; then
	cloudflare_setup_state_available="true"
fi
static_backup=""
static_modified="false"
dynamic_state_dir="$(dirname -- "$compose_file")/traefik/dynamic"
generated_state_dir="$dynamic_state_dir/generated"
traefik_state_prepared="false"
generated_route_existed="false"
reload_existed="false"
forwarded_echo_container_id=""
forwarded_echo_dir=""
forwarded_echo_route_file=""
forwarded_echo_route_name=""
forwarded_echo_host="stealth-forwarded-header-echo.test"

prepare_traefik_state_for_smoke() {
	for directory in "$dynamic_state_dir" "$generated_state_dir"; do
		if [ -L "$directory" ] || [ ! -d "$directory" ]; then
			printf 'Traefik state is not a normal directory: %s\n' "$directory" >&2
			return 1
		fi
	done
	if [ "$(stat -c '%u' "$dynamic_state_dir")" != "$(id -u)" ]; then
		printf 'Traefik dynamic state must initially be owned by the invoking host user: %s\n' "$dynamic_state_dir" >&2
		return 1
	fi
	if [ -e "$generated_state_dir/platform-sites.yaml" ]; then
		generated_route_existed="true"
	fi
	if [ -L "$dynamic_state_dir/.reload.yaml" ] || { [ -e "$dynamic_state_dir/.reload.yaml" ] && [ ! -f "$dynamic_state_dir/.reload.yaml" ]; }; then
		printf 'Traefik reload state is not a normal file: %s\n' "$dynamic_state_dir/.reload.yaml" >&2
		return 1
	fi
	if [ -e "$dynamic_state_dir/.reload.yaml" ]; then
		reload_existed="true"
	fi
	traefik_state_prepared="true"
	printf 'Traefik state starts owned by host uid %s; the Compose init service will prepare worker access\n' "$(id -u)"
}

verify_traefik_state_init() {
	local host_uid dynamic_state generated_state reload_state
	host_uid="$(id -u)"
	dynamic_state="$(stat -c '%u:%g:%a' "$dynamic_state_dir")"
	generated_state="$(stat -c '%u:%g:%a' "$generated_state_dir")"
	reload_state="$(stat -c '%u:%g:%a' "$dynamic_state_dir/.reload.yaml")"
	if [ "$dynamic_state" != "${host_uid}:10001:775" ]; then
		printf 'init dynamic-state uid/gid/mode = %s, want %s:10001:775\n' "$dynamic_state" "$host_uid" >&2
		return 1
	fi
	if [ "$generated_state" != "${host_uid}:10001:775" ]; then
		printf 'init generated-state uid/gid/mode = %s, want %s:10001:775\n' "$generated_state" "$host_uid" >&2
		return 1
	fi
	if [ "$reload_state" != "${host_uid}:10001:664" ]; then
		printf 'init reload-state uid/gid/mode = %s, want %s:10001:664\n' "$reload_state" "$host_uid" >&2
		return 1
	fi
	printf 'Traefik state init prepared dynamic=%s generated=%s reload=%s\n' "$dynamic_state" "$generated_state" "$reload_state"
}

restore_traefik_state_after_smoke() {
	if [ "$traefik_state_prepared" != "true" ]; then
		return 0
	fi
	if [ "$generated_route_existed" != "true" ]; then
		rm -f -- "$generated_state_dir/platform-sites.yaml" || true
	fi
	if [ "$reload_existed" != "true" ]; then
		rm -f -- "$dynamic_state_dir/.reload.yaml" || true
	fi
}

cleanup() {
	local exit_code=$? worker_container
	if [ -n "$forwarded_echo_container_id" ]; then
		docker rm -f "$forwarded_echo_container_id" >/dev/null 2>&1 || true
		forwarded_echo_container_id=""
	fi
	if [ -n "$forwarded_echo_route_file" ]; then
		rm -f -- "$forwarded_echo_route_file" || true
		forwarded_echo_route_file=""
	fi
	if [ -n "$forwarded_echo_dir" ]; then
		rm -rf -- "$forwarded_echo_dir"
		forwarded_echo_dir=""
	fi
	if [ -n "$filelog_smoke_pid" ]; then
		kill "$filelog_smoke_pid" 2>/dev/null || true
		wait "$filelog_smoke_pid" 2>/dev/null || true
		filelog_smoke_pid=""
	fi
	# Best-effort cleanup keeps a persistent smoke database from retaining the
	# temporary Site, project, workload domain, or elevated role if a later
	# assertion fails. The database remains authoritative throughout the probe.
	if [ -n "$platform_site_id" ] && [ -n "$api_url" ] && [ -n "$auth_cookie_header" ]; then
		curl --silent --show-error --max-time 10 --header "Cookie: $auth_cookie_header" --request DELETE "${api_url%/}/v1/projects/${platform_project_id}/sites/${platform_site_id}" >/dev/null 2>&1 || true
	fi
	if [ -n "$platform_app_id" ] && [ -n "$api_url" ] && [ -n "$auth_cookie_header" ]; then
		curl --silent --show-error --max-time 10 --header "Cookie: $auth_cookie_header" --request DELETE "${api_url%/}/v1/projects/${platform_project_id}/apps/${platform_app_id}" >/dev/null 2>&1 || true
	fi
	if [ -n "$platform_project_id" ] && [ -n "$api_url" ] && [ -n "$auth_cookie_header" ]; then
		curl --silent --show-error --max-time 10 --header "Cookie: $auth_cookie_header" --header 'Content-Type: application/json' --request DELETE --data '{"confirm_name":"platform-route-smoke"}' "${api_url%/}/v1/projects/${platform_project_id}" >/dev/null 2>&1 || true
	fi
	if [ "$platform_domain_changed" = "true" ]; then
		if [ -n "$platform_previous_base_domain" ]; then
			"${compose[@]}" exec -T postgres sh -ec 'previous_domain="$1"; case "$previous_domain" in *[!A-Za-z0-9.-]*) exit 2;; esac; psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 --command "UPDATE instance_domain_settings SET workload_base_domain = '\''$previous_domain'\'', updated_at = now() WHERE id = TRUE"' sh "$platform_previous_base_domain" >/dev/null 2>&1 || true
		else
			"${compose[@]}" exec -T postgres sh -ec 'psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 --command "UPDATE instance_domain_settings SET workload_base_domain = NULL, updated_at = now() WHERE id = TRUE"' >/dev/null 2>&1 || true
		fi
	fi
	if [ "$exit_code" -ne 0 ]; then
		printf 'Compose smoke failed; collecting bounded diagnostics\n' >&2
		"${compose[@]}" ps >&2 || true
		"${compose[@]}" logs --tail=80 clickhouse buildkit otelcol-state-init telemetry-docker-logs-state-init traefik-state-init cloudflare-state-init otel-collector telemetry-host telemetry-docker-logs telemetry-docker-proxy telemetry-docker api worker migrate console proxy traefik >&2 || true
	fi
	if [ "${SMOKE_REMOVE_VOLUMES:-false}" = "true" ]; then
		"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
	else
		"${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
	fi
	if [ "$buildkit_apparmor_profile_loaded" = "true" ] && [ -n "$buildkit_apparmor_profile_file" ]; then
		if [ "$(id -u)" -eq 0 ]; then
			apparmor_parser -R "$buildkit_apparmor_profile_file" >/dev/null 2>&1 || true
		else
			sudo apparmor_parser -R "$buildkit_apparmor_profile_file" >/dev/null 2>&1 || true
		fi
	fi
	if [ "$core_modified" = "true" ] && [ -n "$core_backup" ]; then
		cp -- "$core_backup" "$core_file" || true
	fi
	if [ "$static_modified" = "true" ] && [ -n "$static_backup" ]; then
		cp -- "$static_backup" "$static_file" || true
	fi
	restore_traefik_state_after_smoke
	if [ -n "$buildkit_wrong_identity_dir" ]; then
		worker_container="$("${compose[@]}" ps -q worker 2>/dev/null || true)"
		if [ -n "$worker_container" ]; then
			docker exec --user 0 "$worker_container" rm -rf /tmp/stealth-buildkit-mtls-negative-test >/dev/null 2>&1 || true
		fi
		rm -rf -- "$buildkit_wrong_identity_dir"
	fi
	if [ "$buildkit_pki_smoke_created" = "true" ]; then
		pki_path="$(dirname -- "$compose_file")/state/buildkit-mtls"
		if [ -d "$pki_path" ] && [ ! -L "$pki_path" ]; then
			rm -rf -- "$pki_path"
		fi
		docker volume rm stealth_app_buildkit_worker_credentials stealth_app_buildkit_server_credentials >/dev/null 2>&1 || true
	fi
	rm -f "$cookie_file" "$register_response" "$platform_response" "$platform_archive" "$app_archive" "$app_probe_binary" "$buildkit_apparmor_profile_file"
	if [ -n "$core_backup" ]; then
		rm -f "$core_backup"
	fi
	exit "$exit_code"
}
trap cleanup EXIT

prepare_buildkit_pki_for_smoke() {
	local state_dir pki_path
	state_dir="$(dirname -- "$compose_file")/state"
	pki_path="$state_dir/buildkit-mtls"
	if [ ! -e "$pki_path" ] && [ ! -L "$pki_path" ] &&
		[ ! -e "$pki_path.pending" ] && [ ! -L "$pki_path.pending" ] &&
		[ ! -e "$pki_path.previous" ] && [ ! -L "$pki_path.previous" ]; then
		buildkit_pki_smoke_created="true"
	fi
	STEALTH_BUILDKIT_PKI_SMOKE_STATE_DIR="$state_dir" \
		go test ./internal/buildkitpki -run '^TestProductionComposeSmokePreparePKI$' -count=1
	for key in "$pki_path/ca-key.pem" "$pki_path/server/key.pem" "$pki_path/worker/key.pem" "$pki_path/health/key.pem"; do
		if [ -L "$key" ] || [ ! -f "$key" ]; then
			printf 'BuildKit mTLS smoke key was not created as a regular file: %s\n' "$key" >&2
			return 1
		fi
		if [ "$(stat -c '%a' "$key")" != '600' ]; then
			printf 'BuildKit mTLS host private key mode is not 0600: %s\n' "$key" >&2
			return 1
		fi
	done
}

expect_buildkit_auth_rejection() {
	local label="$1" output
	shift
	if output="$("${compose[@]}" exec -T worker "$@" 2>&1)"; then
		printf 'BuildKit accepted %s\n' "$label" >&2
		return 1
	fi
	# BuildKit v0.33.0 may surface a required-client-certificate handshake
	# rejection through gRPC as a generic EOF. Prove the endpoint remains
	# reachable with the valid identity immediately before and after this
	# request instead of depending on unstable error wording.
	printf 'BuildKit rejected %s while the authenticated control probe is available\n' "$label"
}

verify_buildkit_mtls_smoke() {
	local worker_container
	if ! command -v openssl >/dev/null 2>&1; then
		printf '%s\n' 'BuildKit mTLS smoke requires OpenSSL for an untrusted test identity' >&2
		return 1
	fi
	worker_container="$("${compose[@]}" ps -q worker)"
	if [ -z "$worker_container" ]; then
		printf '%s\n' 'worker container is unavailable for BuildKit mTLS smoke' >&2
		return 1
	fi
	buildkit_wrong_identity_dir="$(mktemp -d "${TMPDIR:-/tmp}/stealth-buildkit-untrusted.XXXXXX")"
	chmod 0700 "$buildkit_wrong_identity_dir"
	openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$buildkit_wrong_identity_dir/wrong-ca-key.pem" >/dev/null 2>&1
	openssl req -x509 -new -key "$buildkit_wrong_identity_dir/wrong-ca-key.pem" -sha256 -days 2 \
		-subj '/CN=Untrusted Stealth BuildKit smoke CA' \
		-addext 'basicConstraints=critical,CA:TRUE' \
		-addext 'keyUsage=critical,keyCertSign,cRLSign' \
		-out "$buildkit_wrong_identity_dir/wrong-ca.pem" >/dev/null 2>&1
	openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$buildkit_wrong_identity_dir/wrong-client-key.pem" >/dev/null 2>&1
	openssl req -new -key "$buildkit_wrong_identity_dir/wrong-client-key.pem" \
		-subj '/CN=Untrusted Stealth BuildKit smoke client' \
		-out "$buildkit_wrong_identity_dir/wrong-client.csr" >/dev/null 2>&1
	cat >"$buildkit_wrong_identity_dir/client-ext.cnf" <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
EOF
	openssl x509 -req -in "$buildkit_wrong_identity_dir/wrong-client.csr" \
		-CA "$buildkit_wrong_identity_dir/wrong-ca.pem" -CAkey "$buildkit_wrong_identity_dir/wrong-ca-key.pem" \
		-CAcreateserial -days 2 -sha256 -extfile "$buildkit_wrong_identity_dir/client-ext.cnf" \
		-out "$buildkit_wrong_identity_dir/wrong-client-cert.pem" >/dev/null 2>&1
	docker exec --user 0 "$worker_container" mkdir -p /tmp/stealth-buildkit-mtls-negative-test
	docker cp "$buildkit_wrong_identity_dir/wrong-ca.pem" "$worker_container:/tmp/stealth-buildkit-mtls-negative-test/wrong-ca.pem"
	docker cp "$buildkit_wrong_identity_dir/wrong-client-cert.pem" "$worker_container:/tmp/stealth-buildkit-mtls-negative-test/wrong-client-cert.pem"
	docker cp "$buildkit_wrong_identity_dir/wrong-client-key.pem" "$worker_container:/tmp/stealth-buildkit-mtls-negative-test/wrong-client-key.pem"
	docker exec --user 0 "$worker_container" sh -ec \
		'chown -R 10001:10001 /tmp/stealth-buildkit-mtls-negative-test && chmod 0700 /tmp/stealth-buildkit-mtls-negative-test && chmod 0444 /tmp/stealth-buildkit-mtls-negative-test/*.pem && chmod 0400 /tmp/stealth-buildkit-mtls-negative-test/wrong-client-key.pem'

	local -a base_args=(buildctl --addr tcp://buildkit:1234)
	local -a proper_ca=(--tlscacert /run/secrets/stealth-buildkit/ca.pem)
	local -a valid_client=(--tlscert /run/secrets/stealth-buildkit/client-cert.pem --tlskey /run/secrets/stealth-buildkit/client-key.pem)
	"${compose[@]}" exec -T worker buildctl "${base_args[@]:1}" "${proper_ca[@]}" "${valid_client[@]}" debug workers >/dev/null
	expect_buildkit_auth_rejection 'a client with no certificate' "${base_args[@]}" "${proper_ca[@]}" debug workers
	"${compose[@]}" exec -T worker buildctl "${base_args[@]:1}" "${proper_ca[@]}" "${valid_client[@]}" debug workers >/dev/null
	expect_buildkit_auth_rejection 'a client certificate signed by an untrusted CA' "${base_args[@]}" "${proper_ca[@]}" \
		--tlscert /tmp/stealth-buildkit-mtls-negative-test/wrong-client-cert.pem \
		--tlskey /tmp/stealth-buildkit-mtls-negative-test/wrong-client-key.pem debug workers
	"${compose[@]}" exec -T worker buildctl "${base_args[@]:1}" "${proper_ca[@]}" "${valid_client[@]}" debug workers >/dev/null
	expect_buildkit_auth_rejection 'an untrusted server CA' "${base_args[@]}" \
		--tlscacert /tmp/stealth-buildkit-mtls-negative-test/wrong-ca.pem "${valid_client[@]}" debug workers
	"${compose[@]}" exec -T worker buildctl "${base_args[@]:1}" "${proper_ca[@]}" "${valid_client[@]}" debug workers >/dev/null
	printf '%s\n' 'worker certificate authenticated BuildKit debug workers successfully'
}

prepare_buildkit_apparmor() {
	local restriction profile_dir
	if [ ! -r /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
		return 0
	fi
	restriction="$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns)"
	case "$restriction" in
		0) return 0 ;;
		1) ;;
		*)
			printf 'unsupported AppArmor unprivileged user namespace setting: %s\n' "$restriction" >&2
			return 1
			;;
	esac
	if ! command -v apparmor_parser >/dev/null 2>&1; then
		printf '%s\n' 'AppArmor restricts unprivileged user namespaces but apparmor_parser is unavailable' >&2
		return 1
	fi
	buildkit_apparmor_profile_name="stealth-buildkit-rootless-smoke-$$"
	buildkit_apparmor_profile_file="$(mktemp "${TMPDIR:-/tmp}/stealth-buildkit-apparmor.XXXXXX")"
	profile_dir="$(dirname -- "$compose_file")/buildkit"
	cat >"$buildkit_apparmor_profile_file" <<EOF
abi <abi/4.0>,
include <tunables/global>

profile $buildkit_apparmor_profile_name flags=(unconfined) {
  userns,
}
EOF
	if [ "$(id -u)" -eq 0 ]; then
		apparmor_parser -r -W "$buildkit_apparmor_profile_file"
	else
		sudo apparmor_parser -r -W "$buildkit_apparmor_profile_file"
	fi
	buildkit_apparmor_profile_loaded="true"
	export APPS_BUILDKIT_APPARMOR_PROFILE="$buildkit_apparmor_profile_name"
	printf 'Loaded temporary BuildKit userns-only AppArmor profile for smoke: %s\n' "$buildkit_apparmor_profile_name"
	if [ ! -f "$profile_dir/stealth-buildkit-rootless.apparmor" ]; then
		printf 'managed BuildKit AppArmor profile asset is missing: %s\n' "$profile_dir/stealth-buildkit-rootless.apparmor" >&2
		return 1
	fi
}

prepare_buildkit_apparmor
prepare_buildkit_pki_for_smoke

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
cloudflared_ingress_ip="$(awk -F= '$1 == "STEALTH_CLOUDFLARED_INGRESS_IP" { value = substr($0, index($0, "=") + 1) } END { print value }' "$env_file")"
cloudflared_ingress_ip="${cloudflared_ingress_ip%$'\r'}"
if [ -z "$cloudflared_ingress_ip" ]; then
	printf 'STEALTH_CLOUDFLARED_INGRESS_IP is required for Traefik smoke rendering\n' >&2
	exit 2
fi
if grep -Fq '__STEALTH_CLOUDFLARED_TRUSTED_CIDR__' "$static_file"; then
	static_backup="$(mktemp "${TMPDIR:-/tmp}/stealth-compose-smoke-static.XXXXXX")"
	cp -- "$static_file" "$static_backup"
	python3 - "$static_file" "$cloudflared_ingress_ip" <<'PY'
import os
import sys

path, peer = sys.argv[1:]
with open(path, encoding="utf-8") as source:
    contents = source.read()
contents = contents.replace("__STEALTH_CLOUDFLARED_TRUSTED_CIDR__", peer + "/32")
temporary = path + ".smoke.tmp"
with open(temporary, "w", encoding="utf-8") as target:
    target.write(contents)
    target.flush()
    os.fsync(target.fileno())
os.replace(temporary, path)
PY
	static_modified="true"
fi

# Validate this after host-side placeholder rendering. The actual ownership
# handoff is performed by the same narrow root init service used by the
# production installer; the smoke never pre-chowns the checkout from the host.
prepare_traefik_state_for_smoke
"${compose[@]}" run --rm --no-deps \
	-e "STEALTH_TRAEFIK_HOST_UID=$(id -u)" \
	traefik-state-init
verify_traefik_state_init
"${compose[@]}" run --rm --no-deps cloudflare-state-init
if [ "$cloudflare_setup_state_available" = "false" ] && { [ -e "$cloudflare_import_artifact" ] || [ -L "$cloudflare_import_artifact" ]; }; then
	printf '%s\n' 'Cloudflare initializer fabricated an artifact without source state' >&2
	exit 1
fi
if [ -e "$cloudflare_import_artifact" ] || [ -L "$cloudflare_import_artifact" ]; then
	if [ ! -f "$cloudflare_import_artifact" ] || [ -L "$cloudflare_import_artifact" ] || [ ! -s "$cloudflare_import_artifact" ]; then
		printf '%s\n' 'Cloudflare initializer did not publish a regular encrypted narrow artifact' >&2
		exit 1
	fi
fi
for legacy_import in "$cloudflare_import_dir/setup-state.enc" "$cloudflare_import_dir/.setup-state.enc.tmp"; do
	if [ -e "$legacy_import" ] || [ -L "$legacy_import" ]; then
		printf 'Cloudflare initializer left a full setup snapshot in the worker import directory: %s\n' "$legacy_import" >&2
		exit 1
	fi
done
if [ -d "$cloudflare_import_dir" ]; then
	unexpected_import_entry="$(find "$cloudflare_import_dir" -mindepth 1 -maxdepth 1 ! -name cloudflare-import.enc -print -quit)"
	if [ -n "$unexpected_import_entry" ]; then
		printf 'Cloudflare worker import directory contains unexpected content: %s\n' "$unexpected_import_entry" >&2
		exit 1
	fi
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

verify_worker_platform_state_boundary() {
	local worker worker_user mounts dynamic_state generated_state reload_state core_state host_uid
	host_uid="$(id -u)"
	worker="$("${compose[@]}" ps -q worker)"
	if [ -z "$worker" ]; then
		printf '%s\n' 'missing worker container' >&2
		return 1
	fi
	worker_user="$(docker exec "$worker" sh -ec 'printf "%s:%s\n" "$(id -u)" "$(id -g)"')"
	if [ "$worker_user" != '10001:10001' ]; then
		printf 'worker effective uid/gid = %s, want 10001:10001\n' "$worker_user" >&2
		return 1
	fi
	mounts="$(docker inspect --format '{{range .Mounts}}{{printf "%s=%t " .Destination .RW}}{{end}}' "$worker")"
	case "$mounts" in
		*'/var/lib/stealth/traefik=true '*|*'/var/lib/stealth/traefik=true') ;;
		*) printf 'worker Traefik state mount is not writable: %s\n' "$mounts" >&2; return 1 ;;
	esac
	case "$mounts" in
		*'/var/lib/stealth/traefik/core.yaml=false '*|*'/var/lib/stealth/traefik/core.yaml=false') ;;
		*) printf 'worker core.yaml is not overlaid read-only: %s\n' "$mounts" >&2; return 1 ;;
	esac
	case "$mounts" in
		*'/var/lib/stealth/cloudflare-import=false '*|*'/var/lib/stealth/cloudflare-import=false') ;;
		*) printf 'worker Cloudflare import directory is not mounted read-only: %s\n' "$mounts" >&2; return 1 ;;
	esac
	case "$mounts" in
		*'/etc/traefik='*|*'/var/lib/stealth/traefik/traefik.yaml='*|*'/var/lib/stealth/traefik/static='*|*'/run/secrets/cloudflare-tunnel-token='*|*'=/state='*|*'/var/lib/stealth/setup-state='*)
			printf 'worker has an unexpected release-managed Traefik mount: %s\n' "$mounts" >&2
			return 1
			;;
	esac
	dynamic_state="$(docker exec "$worker" sh -ec 'stat -c "%u:%g:%a" /var/lib/stealth/traefik')"
	if [ "$dynamic_state" != "${host_uid}:10001:775" ]; then
		printf 'worker dynamic-state uid/gid/mode = %s, want %s:10001:775\n' "$dynamic_state" "$host_uid" >&2
		return 1
	fi
	generated_state="$(docker exec "$worker" sh -ec 'stat -c "%u:%g:%a" /var/lib/stealth/traefik/generated')"
	if [ "$generated_state" != "${host_uid}:10001:775" ]; then
		printf 'worker generated-state uid/gid/mode = %s, want %s:10001:775\n' "$generated_state" "$host_uid" >&2
		return 1
	fi
	reload_state="$(docker exec "$worker" sh -ec 'stat -c "%u:%g:%a" /var/lib/stealth/traefik/.reload.yaml')"
	core_state="$(docker exec "$worker" sh -ec 'stat -c "%u:%g:%a" /var/lib/stealth/traefik/core.yaml')"
	if [ "$reload_state" != '10001:10001:644' ]; then
		printf 'worker reload-state uid/gid/mode = %s, want 10001:10001:644 after atomic replacement\n' "$reload_state" >&2
		return 1
	fi
	printf 'worker runtime identity=%s dynamic=%s generated=%s reload=%s core=%s\n' "$worker_user" "$dynamic_state" "$generated_state" "$reload_state" "$core_state"
	"${compose[@]}" exec -T worker sh -ec '
set -eu
state=/var/lib/stealth/traefik
temporary="$state/.permission-regression.$$"
printf "%s\n" route >"$temporary"
mv "$temporary" "$state/.permission-regression-route"
rm -f "$state/.permission-regression-route"
printf "%s\n" "# worker reload permission regression" >"$temporary"
reload_backup="$state/.permission-regression-reload-backup"
cp "$state/.reload.yaml" "$reload_backup"
mv "$temporary" "$state/.reload.yaml"
cp "$reload_backup" "$temporary"
mv "$temporary" "$state/.reload.yaml"
rm -f "$reload_backup"
core_backup="$state/.permission-regression-core-backup"
cp "$state/core.yaml" "$core_backup"
if printf "%s\n" attempted >"$state/core.yaml" 2>/dev/null; then
	cp "$core_backup" "$state/core.yaml" || true
	rm -f "$core_backup"
	echo "worker modified core.yaml" >&2
	exit 1
fi
printf "%s\n" attempted >"$temporary"
if mv "$temporary" "$state/core.yaml" 2>/dev/null; then
	cp "$core_backup" "$state/core.yaml" || true
	rm -f "$core_backup"
	echo "worker replaced core.yaml" >&2
	exit 1
fi
rm -f "$temporary" "$core_backup"
test ! -e /var/lib/stealth/traefik/traefik.yaml
'
	printf 'worker generated-state and reload writes, core.yaml protection, and static-file isolation passed\n'
}

ingress_env_value() {
	local key="$1" value
	value="$(awk -F= -v key="$key" '$1 == key { value = substr($0, index($0, "=") + 1) } END { print value }' "$env_file")"
	printf '%s\n' "${value%$'\r'}"
}

verify_traefik_network_address_model() {
	local ingress_network subnet ip_range traefik_ip cloudflared_ip container actual_ip api_container api_image temp_id temp_ip ipam_json compose_config
	ingress_network="$(traefik_ingress_network_name)"
	subnet="$(ingress_env_value STEALTH_INGRESS_NETWORK_SUBNET)"
	ip_range="$(ingress_env_value STEALTH_INGRESS_IP_RANGE)"
	traefik_ip="$(ingress_env_value STEALTH_TRAEFIK_INGRESS_IP)"
	cloudflared_ip="$(ingress_env_value STEALTH_CLOUDFLARED_INGRESS_IP)"
	container="$("${compose[@]}" ps -q traefik)"
	actual_ip="$(docker inspect --format "{{(index .NetworkSettings.Networks \"$ingress_network\").IPAddress}}" "$container")"
	if [ "$actual_ip" != "$traefik_ip" ]; then
		printf 'Traefik runtime IP = %s, want persisted %s\n' "$actual_ip" "$traefik_ip" >&2
		return 1
	fi
	compose_config="$("${compose[@]}" --profile cloudflare config)"
	if ! grep -Fq -- "ipv4_address: $cloudflared_ip" <<<"$compose_config"; then
		printf 'rendered Cloudflared profile does not reserve persisted IP %s\n' "$cloudflared_ip" >&2
		return 1
	fi
	ipam_json="$(docker network inspect --format '{{json .IPAM.Config}}' "$ingress_network")"
	if ! python3 -c 'import ipaddress, json, sys; subnet = ipaddress.ip_network(sys.argv[1]); pool = ipaddress.ip_network(sys.argv[2]); configs = json.loads(sys.argv[3]); raise SystemExit(0 if any(item.get("Subnet") == str(subnet) and item.get("IPRange") == str(pool) for item in configs) else 1)' "$subnet" "$ip_range" "$ipam_json"; then
		printf 'Docker ingress IPAM does not match persisted subnet=%s pool=%s\n' "$subnet" "$ip_range" >&2
		return 1
	fi
	api_container="$("${compose[@]}" ps -q api)"
	api_image="$(docker inspect --format '{{.Config.Image}}' "$api_container")"
	for _ in 1 2 3; do
		temp_id="$(docker run -d --rm --network "$ingress_network" --entrypoint sh "$api_image" -ec 'sleep 20')"
		temp_ip="$(docker inspect --format "{{(index .NetworkSettings.Networks \"$ingress_network\").IPAddress}}" "$temp_id")"
		if [ "$temp_ip" = "$traefik_ip" ] || [ "$temp_ip" = "$cloudflared_ip" ]; then
			docker rm -f "$temp_id" >/dev/null 2>&1 || true
			printf 'dynamic ingress allocation consumed reserved peer %s\n' "$temp_ip" >&2
			return 1
		fi
		if ! python3 -c 'import ipaddress, sys; raise SystemExit(0 if ipaddress.ip_address(sys.argv[2]) in ipaddress.ip_network(sys.argv[1]) else 1)' "$ip_range" "$temp_ip"; then
			docker rm -f "$temp_id" >/dev/null 2>&1 || true
			printf 'dynamic ingress allocation escaped pool %s: %s\n' "$ip_range" "$temp_ip" >&2
			return 1
		fi
		docker rm -f "$temp_id" >/dev/null 2>&1 || true
	done
	printf 'Traefik persisted IP and reserved dynamic ingress pool passed: subnet=%s pool=%s traefik=%s cloudflared=%s\n' "$subnet" "$ip_range" "$traefik_ip" "$cloudflared_ip"
}

start_forwarded_header_echo() {
	local ingress_network api_container api_image container_name dynamic_dir
	ingress_network="$(traefik_ingress_network_name)"
	dynamic_dir="$(dirname -- "$core_file")"
	forwarded_echo_route_file="$dynamic_dir/smoke-forwarded-headers-$$.yaml"
	forwarded_echo_route_name="$(basename -- "$forwarded_echo_route_file")"
	if [ -e "$forwarded_echo_route_file" ]; then
		printf 'refusing to overwrite existing smoke route file: %s\n' "$forwarded_echo_route_file" >&2
		return 1
	fi
	# The worker owns the generated-state write path. Create this temporary
	# smoke route through the same non-root boundary as the reconciler instead
	# of giving the host script a second ownership mechanism.
	"${compose[@]}" exec -T worker sh -ec '
set -eu
name="$1"
host="$2"
state=/var/lib/stealth/traefik
path="$state/$name"
temporary="$path.tmp"
cat >"$temporary" <<EOF
http:
  routers:
    smoke-forwarded-header-echo:
      entryPoints:
        - web
      rule: "Host(\`$host\`) && Path(\`/cgi-bin/headers\`)"
      priority: 300
      middlewares:
        - stealth-security-headers
      service: smoke-forwarded-header-echo
  services:
    smoke-forwarded-header-echo:
      loadBalancer:
        passHostHeader: true
        servers:
          - url: http://forwarded-header-echo:8080
EOF
sync "$temporary" 2>/dev/null || true
mv "$temporary" "$path"
' sh "$forwarded_echo_route_name" "$forwarded_echo_host"
	forwarded_echo_dir="$(mktemp -d "${TMPDIR:-/tmp}/stealth-forwarded-header-echo.XXXXXX")"
	chmod 0755 "$forwarded_echo_dir"
	python3 - "$forwarded_echo_dir/nginx.conf" <<'PY'
import sys

path = sys.argv[1]
contents = '''events {}
http {
  server {
    listen 8080;
    location = /cgi-bin/headers {
      default_type text/plain;
      return 200 "remote_addr=$remote_addr\\nx_forwarded_for=$http_x_forwarded_for\\nx_forwarded_proto=$http_x_forwarded_proto\\nx_real_ip=$http_x_real_ip\\n";
    }
  }
}
'''
with open(path, 'w', encoding='utf-8') as target:
    target.write(contents)
PY
	nginx_image="$(docker inspect --format '{{.Config.Image}}' "$("${compose[@]}" ps -q proxy)")"
	container_name="${COMPOSE_PROJECT_NAME:-stealth}-forwarded-header-echo-$$"
	forwarded_echo_container_id="$(docker run -d --rm --name "$container_name" --network "$ingress_network" --network-alias forwarded-header-echo --volume "$forwarded_echo_dir/nginx.conf:/etc/nginx/nginx.conf:ro" "$nginx_image")"
	for _ in $(seq 1 20); do
		if forwarded_header_probe_from_api 1.2.3.4 https >/dev/null 2>&1; then
			printf 'forwarded-header echo backend is reachable through Traefik\n'
			return 0
		fi
		sleep 1
	done
	docker logs "$forwarded_echo_container_id" >&2 2>/dev/null || true
	printf '%s\n' 'forwarded-header echo backend did not become reachable through Traefik' >&2
	return 1
}

forwarded_header_probe_from_api() {
	local spoofed_for="$1" spoofed_proto="$2"
	"${compose[@]}" exec -T api sh -ec '
		wget -qO- --timeout=5 \
			--header "Host: $1" \
			--header "X-Forwarded-For: $2" \
			--header "X-Forwarded-Proto: $3" \
			http://traefik:8080/cgi-bin/headers
	' sh "$forwarded_echo_host" "$spoofed_for" "$spoofed_proto"
}

forwarded_header_probe_from_trusted_peer() {
	local spoofed_for="$1" spoofed_proto="$2" ingress_network api_container api_image cloudflared_ip
	ingress_network="$(traefik_ingress_network_name)"
	cloudflared_ip="$(ingress_env_value STEALTH_CLOUDFLARED_INGRESS_IP)"
	api_container="$("${compose[@]}" ps -q api)"
	api_image="$(docker inspect --format '{{.Config.Image}}' "$api_container")"
	docker run --rm --network "$ingress_network" --ip "$cloudflared_ip" --entrypoint sh "$api_image" -ec '
		wget -qO- --timeout=5 \
			--header "Host: $1" \
			--header "X-Forwarded-For: $2" \
			--header "X-Forwarded-Proto: $3" \
			http://traefik:8080/cgi-bin/headers
	' sh "$forwarded_echo_host" "$spoofed_for" "$spoofed_proto"
}

verify_traefik_forwarded_header_boundary() {
	local untrusted trusted
	untrusted="$(forwarded_header_probe_from_api 1.2.3.4 https)"
	if printf '%s\n' "$untrusted" | grep -Fq 'x_forwarded_for=1.2.3.4' || printf '%s\n' "$untrusted" | grep -Fq 'x_forwarded_proto=https'; then
		printf 'untrusted client spoof reached the echo backend as trusted metadata:\n%s\n' "$untrusted" >&2
		return 1
	fi
	trusted="$(forwarded_header_probe_from_trusted_peer 1.2.3.4 https)"
	if ! printf '%s\n' "$trusted" | grep -Fq 'x_forwarded_for=1.2.3.4' || ! printf '%s\n' "$trusted" | grep -Fq 'x_forwarded_proto=https'; then
		printf 'configured Cloudflared peer did not preserve the trusted forwarded chain:\n%s\n' "$trusted" >&2
		return 1
	fi
	printf 'Traefik forwarded-header trust boundary passed: untrusted spoof stripped, configured Cloudflared peer preserved\n'
}

http_probe_output() {
	local service="$1" path="$2" host="$3" cookie_header="${4:-}" forwarded_proto="${5:-}" target
	case "$service" in
		proxy) target="http://proxy${path}" ;;
		traefik) target="http://traefik:8080${path}" ;;
		*) printf 'unknown probe service %s\n' "$service" >&2; return 2 ;;
	esac
	"${compose[@]}" exec -T api sh -ec '
		path="$1"; host="$2"; cookie="$3"; forwarded_proto="$4"; target="$5"
		set -- --header "Host: $host"
		if [ -n "$cookie" ]; then set -- "$@" --header "Cookie: $cookie"; fi
		if [ -n "$forwarded_proto" ]; then set -- "$@" --header "X-Forwarded-Proto: $forwarded_proto"; fi
		wget -S -O /dev/null --timeout=8 "$@" "$target" 2>&1 || true
	' sh "$path" "$host" "$cookie_header" "$forwarded_proto" "$target"
}

http_probe_status() {
	local output="$1"
	printf '%s\n' "$output" | awk '/HTTP\/[0-9.]+/ { code=$2 } END { gsub(/\r/, "", code); print code }'
}

http_probe_first_status() {
	local output="$1"
	printf '%s\n' "$output" | awk '/HTTP\/[0-9.]+/ { code=$2; gsub(/\r/, "", code); print code; exit }'
}

http_probe_header() {
	local output="$1" wanted="$2"
	printf '%s\n' "$output" | awk -v wanted="$wanted" '
	BEGIN { wanted = tolower(wanted) }
	{
		line = $0
		sub(/\r$/, "", line)
		sub(/^[[:space:]]+/, "", line)
		colon = index(line, ":")
		if (colon > 0 && tolower(substr(line, 1, colon - 1)) == wanted) {
			value = substr(line, colon + 1)
			sub(/^[[:space:]]+/, "", value)
			print value
			exit
		}
	}'
}

http_probe_body_digest() {
	local service="$1" path="$2" host="$3" cookie_header="${4:-}" target
	case "$service" in
		proxy) target="http://proxy${path}" ;;
		traefik) target="http://traefik:8080${path}" ;;
		*) return 2 ;;
	esac
	"${compose[@]}" exec -T api sh -ec '
		path="$1"; host="$2"; cookie="$3"; target="$4"
		set -- --header "Host: $host"
		if [ -n "$cookie" ]; then set -- "$@" --header "Cookie: $cookie"; fi
	wget -q -O - --timeout=8 "$@" "$target" 2>/dev/null | sha256sum | awk "{print \$1}"
	' sh "$path" "$host" "$cookie_header" "$target"
}

traefik_http_status() {
	http_probe_status "$(http_probe_output traefik "$1" "$2" "${3:-}")"
}

verify_nginx_traefik_parity() {
	local path nginx_response traefik_response nginx_status traefik_status nginx_digest traefik_digest header nginx_header traefik_header
	for path in / /v1/account /healthz /readyz /version /not-a-real-route; do
		nginx_response="$(http_probe_output proxy "$path" "$traefik_host" "$auth_cookie_header")"
		traefik_response="$(http_probe_output traefik "$path" "$traefik_host" "$auth_cookie_header")"
		nginx_status="$(http_probe_status "$nginx_response")"
		traefik_status="$(http_probe_status "$traefik_response")"
		if [ "$nginx_status" != "$traefik_status" ]; then
			printf 'Nginx/Traefik status mismatch path=%s nginx=%s traefik=%s\n' "$path" "$nginx_status" "$traefik_status" >&2
			return 1
		fi
		if [ "$path" = "/" ]; then
			local nginx_first_status traefik_first_status nginx_location traefik_location
			nginx_first_status="$(http_probe_first_status "$nginx_response")"
			traefik_first_status="$(http_probe_first_status "$traefik_response")"
			nginx_location="$(http_probe_header "$nginx_response" Location)"
			traefik_location="$(http_probe_header "$traefik_response" Location)"
			if [ "$nginx_first_status" != "307" ] || [ "$traefik_first_status" != "307" ] || [ "$nginx_location" != "/organizations" ] || [ "$traefik_location" != "/organizations" ] || [ "$nginx_status" != "200" ] || [ "$traefik_status" != "200" ]; then
				printf 'Console root redirect mismatch: Nginx initial=%s location=%q final=%s; Traefik initial=%s location=%q final=%s\n' "$nginx_first_status" "$nginx_location" "$nginx_status" "$traefik_first_status" "$traefik_location" "$traefik_status" >&2
				return 1
			fi
		fi
		nginx_digest="$(http_probe_body_digest proxy "$path" "$traefik_host" "$auth_cookie_header")"
		traefik_digest="$(http_probe_body_digest traefik "$path" "$traefik_host" "$auth_cookie_header")"
		if [ "$nginx_digest" != "$traefik_digest" ]; then
			printf 'Nginx/Traefik body mismatch path=%s nginx=%s traefik=%s\n' "$path" "$nginx_digest" "$traefik_digest" >&2
			return 1
		fi
		if [ "$path" = "/" ] || [ "$path" = "/v1/account" ]; then
			for header in X-Content-Type-Options Referrer-Policy Permissions-Policy X-Frame-Options Content-Security-Policy; do
				nginx_header="$(http_probe_header "$nginx_response" "$header")"
				traefik_header="$(http_probe_header "$traefik_response" "$header")"
				if [ "$nginx_header" != "$traefik_header" ]; then
					printf 'Nginx/Traefik header mismatch path=%s header=%s nginx=%q traefik=%q\n' "$path" "$header" "$nginx_header" "$traefik_header" >&2
					return 1
				fi
			done
		fi
	done
	local nginx_http_response nginx_https_response nginx_hsts traefik_https_response traefik_hsts
	# Use a non-redirecting API response for the HSTS assertion. The root
	# Console route may redirect before rendering its document, while Nginx's
	# server-level header contract applies to both API and Console responses.
	nginx_http_response="$(http_probe_output proxy /v1/account "$traefik_host" "$auth_cookie_header")"
	if [ -n "$(http_probe_header "$nginx_http_response" Strict-Transport-Security)" ]; then
		printf 'Nginx plain HTTP unexpectedly emitted HSTS\n' >&2
		return 1
	fi
	nginx_https_response="$(http_probe_output proxy /v1/account "$traefik_host" "$auth_cookie_header" https)"
	nginx_hsts="$(http_probe_header "$nginx_https_response" Strict-Transport-Security)"
	if [ "$nginx_hsts" != 'max-age=31536000; includeSubDomains' ]; then
		printf 'Nginx HTTPS HSTS policy = %q, want the current edge policy\n' "$nginx_hsts" >&2
		print_bounded_diagnostic 'Nginx HTTPS response headers' "$nginx_https_response"
		return 1
	fi
	traefik_https_response="$(http_probe_output traefik / "$traefik_host" "$auth_cookie_header" https)"
	traefik_hsts="$(http_probe_header "$traefik_https_response" Strict-Transport-Security)"
	if [ -n "$traefik_hsts" ]; then
		printf 'Traefik trusted-header boundary accepted HSTS-triggering X-Forwarded-Proto from an untrusted smoke peer: %q\n' "$traefik_hsts" >&2
		return 1
	fi
	local unknown_nginx unknown_traefik
	unknown_nginx="$(http_probe_status "$(http_probe_output proxy / unknown.example.invalid)")"
	unknown_traefik="$(http_probe_status "$(http_probe_output traefik / unknown.example.invalid)")"
	if [ "$unknown_traefik" != "404" ]; then
		printf 'Traefik unknown host status = %s, want 404\n' "$unknown_traefik" >&2
		return 1
	fi
	# The legacy Nginx file has one default server and therefore accepts an
	# unmatched Host; Traefik deliberately closes that broader legacy surface.
	printf 'Nginx/Traefik parity passed for core paths and security headers; unknown-host legacy Nginx=%s, Traefik=%s (documented hardening)\n' "$unknown_nginx" "$unknown_traefik"
}

verify_traefik_routing() {
	local status sse_headers
	status="$(traefik_http_status / "$traefik_host")"
	if [ "$status" != "200" ]; then
		printf 'Traefik Console route status = %q, want 200\n' "$status" >&2
		return 1
	fi
	for path in /healthz /readyz /version /not-a-real-route; do
		status="$(traefik_http_status "$path" "$traefik_host" "$auth_cookie_header")"
		if [ "$status" != "404" ]; then
			printf 'Traefik Console fallback path=%s status=%q, want 404\n' "$path" "$status" >&2
			return 1
		fi
	done
	status="$(traefik_http_status /v1/account "$traefik_host" "$auth_cookie_header")"
	if [ "$status" = "404" ] || [ -z "$status" ]; then
		printf 'Traefik API path-preservation request status = %q\n' "$status" >&2
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
	verify_traefik_forwarded_header_boundary
	verify_nginx_traefik_parity
	printf 'Traefik core API, Console fallback, fail-closed, SSE, and Nginx parity checks passed; upload limits remain API-owned\n'
}

platform_request() {
	local method="$1" path="$2" body="$3" output="$4"
	if [ -n "$body" ]; then
		curl --silent --show-error --max-time 10 \
			--header "Cookie: $auth_cookie_header" \
			--header 'Content-Type: application/json' \
			--request "$method" --data-raw "$body" \
			--output "$output" --write-out '%{http_code}' \
			"${api_url%/}${path}"
		return
	fi
	curl --silent --show-error --max-time 10 \
		--header "Cookie: $auth_cookie_header" \
		--request "$method" \
		--output "$output" --write-out '%{http_code}' \
		"${api_url%/}${path}"
}

platform_json_field() {
	local file="$1" path="$2"
	python3 - "$file" "$path" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    value = json.load(source)
for part in sys.argv[2].split('.'):
    if not isinstance(value, dict):
        value = None
        break
    value = value.get(part)
if value is None:
    print("")
else:
    print(value)
PY
}

prepare_platform_route_smoke() {
	local account_id organization_id project_status site_status site_status_after_upload app_status app_architecture
	local upload_body
	account_id="$(platform_json_field "$register_response" account.id)"
	organization_id="$(platform_json_field "$register_response" organization.id)"
	if [ -z "$account_id" ] || [ -z "$organization_id" ]; then
		printf '%s\n' 'registration response did not contain account and organization IDs for platform smoke' >&2
		return 1
	fi
	project_status="$(platform_request POST "/v1/organizations/${organization_id}/projects" '{"name":"platform-route-smoke"}' "$platform_response")"
	if [ "$project_status" != '201' ]; then
		printf 'platform smoke project creation returned HTTP %s\n' "$project_status" >&2
		sed -n '1,80p' "$platform_response" >&2
		return 1
	fi
	platform_project_id="$(platform_json_field "$platform_response" project.id)"
	if [ -z "$platform_project_id" ]; then
		printf '%s\n' 'platform smoke project response did not contain an ID' >&2
		return 1
	fi

	site_status="$(platform_request POST "/v1/projects/${platform_project_id}/sites" '{"name":"platform-route-smoke"}' "$platform_response")"
	if [ "$site_status" != '201' ]; then
		printf 'platform smoke Site creation returned HTTP %s\n' "$site_status" >&2
		sed -n '1,80p' "$platform_response" >&2
		return 1
	fi
	platform_site_id="$(platform_json_field "$platform_response" site.id)"
	if [ -z "$platform_site_id" ]; then
		printf '%s\n' 'platform smoke Site response did not contain an ID' >&2
		return 1
	fi

	platform_base_domain="apps-${smoke_marker}.example.com"
	# Seed this disposable desired state directly so the smoke does not alter the
	# installation owner role. The API authorization and audit path are covered
	# by the required PostgreSQL integration test.
	platform_previous_base_domain="$("${compose[@]}" exec -T postgres sh -ec \
		'psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --tuples-only --no-align --command "SELECT workload_base_domain FROM instance_domain_settings WHERE id = TRUE"' | tr -d '\r\n')"
	"${compose[@]}" exec -T postgres sh -ec \
		'domain="$1"; case "$domain" in *[!A-Za-z0-9.-]*) exit 2;; esac; psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 --command "UPDATE instance_domain_settings SET workload_base_domain = '\''$domain'\'', updated_at = now() WHERE id = TRUE"' \
		sh "$platform_base_domain"
	platform_domain_changed="true"

	platform_archive="$(mktemp "${TMPDIR:-/tmp}/stealth-platform-route-smoke.XXXXXX.zip")"
	python3 - "$platform_archive" "$smoke_marker" <<'PY'
import sys
import zipfile

archive, marker = sys.argv[1:]
with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
    output.writestr("index.html", "<!doctype html><title>platform smoke</title>" + marker)
PY
	upload_body="$platform_response"
	site_status_after_upload="$(curl --silent --show-error --max-time 20 \
		--header "Cookie: $auth_cookie_header" \
		--form "source=@${platform_archive};type=application/zip" \
		--form 'source_name=platform-route-smoke.zip' \
		--form 'activate=true' \
		--output "$upload_body" --write-out '%{http_code}' \
		"${api_url%/}/v1/projects/${platform_project_id}/sites/${platform_site_id}/deployments")"
	if [ "$site_status_after_upload" != '201' ]; then
		printf 'platform smoke deployment upload returned HTTP %s\n' "$site_status_after_upload" >&2
		sed -n '1,80p' "$upload_body" >&2
		return 1
	fi

	site_status="$(platform_request GET "/v1/projects/${platform_project_id}/sites/${platform_site_id}" '' "$platform_response")"
	if [ "$site_status" != '200' ]; then
		printf 'platform smoke Site read returned HTTP %s\n' "$site_status" >&2
		return 1
	fi
	platform_host="$(platform_json_field "$platform_response" site.platform_hostname)"
	if [ -z "$platform_host" ]; then
		printf '%s\n' 'platform smoke Site response did not expose platform_hostname' >&2
		return 1
	fi
	app_status="$(platform_request POST "/v1/projects/${platform_project_id}/apps" '{"name":"buildkit-smoke-app","enabled":true}' "$platform_response")"
	if [ "$app_status" != '201' ]; then
		printf 'platform smoke App creation returned HTTP %s\n' "$app_status" >&2
		sed -n '1,80p' "$platform_response" >&2
		return 1
	fi
	platform_app_id="$(platform_json_field "$platform_response" app.id)"
	platform_app_host="$(platform_json_field "$platform_response" app.platform_hostname)"
	if [ -z "$platform_app_id" ] || [ -z "$platform_app_host" ]; then
		printf '%s\n' 'platform smoke App response did not contain its id and reserved hostname' >&2
		return 1
	fi

	app_archive="$(mktemp "${TMPDIR:-/tmp}/stealth-app-build-smoke.XXXXXX.zip")"
	case "$(uname -m)" in
		x86_64|amd64) app_architecture='amd64' ;;
		aarch64|arm64) app_architecture='arm64' ;;
		*) printf 'unsupported BuildKit smoke host architecture: %s\n' "$(uname -m)" >&2; return 1 ;;
	esac
	app_probe_binary="$(mktemp "${TMPDIR:-/tmp}/stealth-app-build-secret-probe.XXXXXX")"
	(cd -- "$repo_root" && GOOS=linux GOARCH="$app_architecture" CGO_ENABLED=0 go build -trimpath -o "$app_probe_binary" ./scripts/fixtures/app-buildkit-secret-probe)
	chmod 0755 "$app_probe_binary"
	python3 - "$app_archive" "$smoke_marker" "$app_probe_binary" <<'PY'
import sys
import zipfile

archive, marker, probe = sys.argv[1:]
with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
    output.writestr("Dockerfile", "FROM scratch\nCOPY payload.txt /payload.txt\nCOPY --chmod=0755 buildkit-secret-probe /buildkit-secret-probe\nRUN [\"/buildkit-secret-probe\"]\n")
    output.writestr("payload.txt", marker + "\n")
    output.write(probe, "buildkit-secret-probe")
PY
	local deployment_response="$platform_response"
	local deployment_status
	deployment_status="$(curl --silent --show-error --max-time 30 \
		--header "Cookie: $auth_cookie_header" \
		--form "source=@${app_archive};type=application/zip" \
		--form 'select=true' \
		--output "$deployment_response" --write-out '%{http_code}' \
		"${api_url%/}/v1/projects/${platform_project_id}/apps/${platform_app_id}/deployments")"
	if [ "$deployment_status" != '202' ]; then
		printf 'platform smoke App source upload returned HTTP %s\n' "$deployment_status" >&2
		sed -n '1,80p' "$deployment_response" >&2
		return 1
	fi
	platform_app_deployment_id="$(platform_json_field "$deployment_response" deployment.id)"
	if [ -z "$platform_app_deployment_id" ]; then
		printf '%s\n' 'platform smoke App deployment response did not contain an id' >&2
		return 1
	fi
	printf 'platform smoke state prepared: host=%s\n' "$platform_host"
}

verify_platform_route_smoke() {
	local status body
	for attempt in $(seq 1 "${SMOKE_ATTEMPTS:-60}"); do
		status="$(traefik_http_status / "$platform_host")"
		if [ "$status" = '200' ]; then
			body="$("${compose[@]}" exec -T api sh -ec \
				'wget -qO- --timeout=8 --header "Host: $1" http://traefik:8080/' sh "$platform_host" || true)"
			if printf '%s' "$body" | grep -Fq -- "$smoke_marker"; then
				break
			fi
		fi
		if [ "$attempt" = "${SMOKE_ATTEMPTS:-60}" ]; then
			printf 'platform hostname route did not serve the expected Site after reconciliation: host=%s status=%s\n' "$platform_host" "$status" >&2
			return 1
		fi
		sleep "${SMOKE_INTERVAL_SECONDS:-2}"
	done
	for path in /v1/account /healthz /readyz /version /metrics; do
		status="$(traefik_http_status "$path" "$platform_host")"
		if [ "$status" != '404' ]; then
			printf 'platform hostname exposed control-plane path=%s status=%s\n' "$path" "$status" >&2
			return 1
		fi
	done
	status="$(traefik_http_status / "unknown.${platform_base_domain}")"
	if [ "$status" != '404' ]; then
		printf 'unknown platform hostname status=%s, want 404\n' "$status" >&2
		return 1
	fi
	printf 'platform hostname Traefik route served the Site and isolated control-plane paths\n'
}

verify_app_build_smoke() {
	local deployment_url status image_row image_path image_archive_sha256 image_digest image_size actual_sha
	local app_runtime app_desired_generation app_observed_generation app_desired_deployment build_status selected
	deployment_url="${api_url%/}/v1/projects/${platform_project_id}/apps/${platform_app_id}/deployments/${platform_app_deployment_id}"
	for attempt in $(seq 1 "${SMOKE_ATTEMPTS:-60}"); do
		status="$(curl --silent --show-error --max-time 10 --header "Cookie: $auth_cookie_header" --output "$platform_response" --write-out '%{http_code}' "$deployment_url")"
		if [ "$status" != '200' ]; then
			printf 'App deployment smoke read returned HTTP %s\n' "$status" >&2
			return 1
		fi
		build_status="$(platform_json_field "$platform_response" deployment.build_status)"
		case "$build_status" in
			succeeded) break ;;
			failed)
				printf 'real BuildKit smoke deployment failed: %s\n' "$(platform_json_field "$platform_response" deployment.error_message)" >&2
				local logs_status
				logs_status="$(curl --silent --show-error --max-time 10 \
					--header "Cookie: $auth_cookie_header" \
					--output "$platform_response" --write-out '%{http_code}' \
					"${deployment_url}/logs?limit=100" || true)"
				if [ "$logs_status" = '200' ]; then
					python3 - "$platform_response" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    response = json.load(source)
for entry in response.get("logs", [])[-30:]:
    message = str(entry.get("message", "")).replace("\r", " ").replace("\x00", " ")
    if len(message) > 1200:
        message = message[:1200] + "…"
    print(f"App build log sequence={entry.get('sequence', '?')}: {message}")
PY
				fi
				return 1
			;;
			queued|running|deferred) ;;
			*) printf 'unexpected App build status: %s\n' "$build_status" >&2; return 1 ;;
		esac
		if [ "$attempt" = "${SMOKE_ATTEMPTS:-60}" ]; then
			printf 'real BuildKit App deployment did not finish: %s\n' "$build_status" >&2
			return 1
		fi
		sleep "${SMOKE_INTERVAL_SECONDS:-2}"
	done
	selected="$(platform_json_field "$platform_response" deployment.selected)"
	if [ "$(platform_json_field "$platform_response" deployment.status)" != 'ready' ] || [ "$selected" != 'True' ]; then
		printf '%s\n' 'successful App build was not ready and selected as requested' >&2
		return 1
	fi
	image_digest="$(platform_json_field "$platform_response" deployment.image_digest)"
	image_archive_sha256="$(platform_json_field "$platform_response" deployment.image_archive_sha256)"
	if ! [[ "$image_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || ! [[ "$image_archive_sha256" =~ ^[0-9a-f]{64}$ ]]; then
		printf 'BuildKit returned malformed durable image identities: digest=%s archive_sha256=%s\n' "$image_digest" "$image_archive_sha256" >&2
		return 1
	fi
	image_row="$("${compose[@]}" exec -T postgres sh -ec \
		'id="$1"; case "$id" in *[!0-9a-f-]*) exit 2;; esac; psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 --tuples-only --no-align --field-separator="|" --command "SELECT image_path,image_archive_sha256,image_digest,image_size_bytes FROM app_deployments WHERE id = '\''$id'\''"' \
		sh "$platform_app_deployment_id" | tr -d '\r')"
	IFS='|' read -r image_path stored_archive_sha256 stored_digest image_size <<<"$image_row"
	if [ -z "$image_path" ] || [ "$stored_archive_sha256" != "$image_archive_sha256" ] || [ "$stored_digest" != "$image_digest" ] || ! [[ "$image_size" =~ ^[1-9][0-9]*$ ]]; then
		printf 'persisted App OCI metadata is incomplete or disagrees with the API: %s\n' "$image_row" >&2
		return 1
	fi
	if ! [[ "$image_path" =~ ^[0-9a-f-]+/[0-9a-f-]+/[0-9a-f-]+$ ]]; then
		printf 'persisted App image locator is not UUID-derived: %s\n' "$image_path" >&2
		return 1
	fi
	actual_sha="$("${compose[@]}" exec -T worker sh -ec \
		'path="$1"; expected="$2"; file="/var/lib/stealth/storage/app-images/$path"; test -s "$file"; actual="$(sha256sum "$file" | cut -d " " -f 1)"; test "$actual" = "$expected"; printf "%s" "$actual"' \
		sh "$image_path" "$image_archive_sha256")"
	if [ "$actual_sha" != "$image_archive_sha256" ]; then
		printf '%s\n' 'persisted OCI archive checksum does not match its bytes' >&2
		return 1
	fi
	status="$(curl --silent --show-error --max-time 10 --header "Cookie: $auth_cookie_header" --output "$platform_response" --write-out '%{http_code}' "${api_url%/}/v1/projects/${platform_project_id}/apps/${platform_app_id}")"
	if [ "$status" != '200' ]; then
		printf 'App runtime truth smoke read returned HTTP %s\n' "$status" >&2
		return 1
	fi
	app_runtime="$(platform_json_field "$platform_response" app.runtime_status)"
	app_desired_generation="$(platform_json_field "$platform_response" app.desired_generation)"
	app_observed_generation="$(platform_json_field "$platform_response" app.observed_generation)"
	app_desired_deployment="$(platform_json_field "$platform_response" app.desired_deployment_id)"
	if [ "$app_runtime" != 'not_deployed' ] || [ "$app_desired_generation" != '2' ] || [ "$app_observed_generation" != '0' ] || [ "$app_desired_deployment" != "$platform_app_deployment_id" ]; then
		printf 'App runtime truth changed after build: status=%s desired=%s observed=%s deployment=%s\n' "$app_runtime" "$app_desired_generation" "$app_observed_generation" "$app_desired_deployment" >&2
		return 1
	fi
	if [ ! -f "$generated_state_dir/platform-sites.yaml" ] || ! grep -Fq -- "$platform_host" "$generated_state_dir/platform-sites.yaml" || grep -Fq -- "$platform_app_host" "$generated_state_dir/platform-sites.yaml"; then
		printf '%s\n' 'App hostname appeared in the current Site-only Traefik route snapshot' >&2
		return 1
	fi
	status="$(traefik_http_status / "$platform_app_host")"
	if [ "$status" != '404' ]; then
		printf 'built App hostname returned HTTP %s, want fail-closed 404\n' "$status" >&2
		return 1
	fi
	printf 'real BuildKit App build passed: digest=%s archive_sha256=%s runtime=%s route=absent\n' "$image_digest" "$image_archive_sha256" "$app_runtime"
}

clear_platform_route_smoke() {
	local route_status
	"${compose[@]}" exec -T postgres sh -ec \
		'psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 --command "UPDATE instance_domain_settings SET workload_base_domain = NULL, updated_at = now() WHERE id = TRUE"'
	for attempt in $(seq 1 "${SMOKE_ATTEMPTS:-60}"); do
		route_status="$(traefik_http_status / "$platform_host")"
		if [ "$route_status" = '404' ]; then
			printf 'platform route removal after workload-domain clear passed\n'
			return 0
		fi
		sleep "${SMOKE_INTERVAL_SECONDS:-2}"
	done
	printf 'platform route remained after workload-domain clear: status=%s\n' "$route_status" >&2
	return 1
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
"${compose[@]}" up -d buildkit
wait_for_healthy buildkit
"${compose[@]}" up -d api worker console proxy traefik
wait_for_healthy api
wait_for_healthy worker
verify_buildkit_mtls_smoke
wait_for_healthy console
wait_for_healthy proxy
wait_for_healthy traefik
ingress_control_status="$("${compose[@]}" run --rm --no-deps ingress-control status)"
if ! printf '%s\n' "$ingress_control_status" | grep -Fq 'Cloudflare Tunnel: not configured' || ! printf '%s\n' "$ingress_control_status" | grep -Fq 'Console origin: not applicable'; then
	printf 'unconfigured Cloudflare ingress-control status is incorrect: %s\n' "$ingress_control_status" >&2
	exit 1
fi
printf '%s\n' 'ingress-control one-shot status passed with an unconfigured Cloudflare provider'
verify_telemetry_runtime_boundaries
verify_traefik_runtime_boundaries
verify_worker_platform_state_boundary
verify_traefik_network_address_model
start_forwarded_header_echo

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
prepare_platform_route_smoke
verify_platform_route_smoke
verify_app_build_smoke
clear_platform_route_smoke

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
