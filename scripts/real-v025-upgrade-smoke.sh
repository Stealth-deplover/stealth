#!/usr/bin/env bash
set -Eeuo pipefail

# End-to-end upgrade proof for the actual released v0.2.5 installation shape.
# The Go test performs the two CLI phases and target-binary handoff. The same
# temporary root is then used by the production Compose smoke, so this is not
# only an installengine unit/integration check.

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
source_env="${ENV_FILE:-$repo_root/.env.production}"
bridge_version="${STEALTH_REAL_V025_BRIDGE_VERSION:-v0.2.6}"
target_version="${STEALTH_REAL_V025_TARGET_VERSION:-v0.2.7}"
fixture_root="$repo_root/internal/cli/testdata/v0.2.5"

if ! command -v docker >/dev/null 2>&1; then
	printf '%s\n' 'real v0.2.5 upgrade smoke requires Docker' >&2
	exit 2
fi
if ! command -v go >/dev/null 2>&1; then
	printf '%s\n' 'real v0.2.5 upgrade smoke requires Go' >&2
	exit 2
fi
if [ ! -f "$source_env" ]; then
	printf 'environment file not found: %s\n' "$source_env" >&2
	exit 2
fi
if [ ! -d "$fixture_root" ]; then
	printf 'v0.2.5 fixture not found: %s\n' "$fixture_root" >&2
	exit 2
fi

smoke_root="$(mktemp -d "${TMPDIR:-/tmp}/stealth-real-v025-upgrade.XXXXXX")"
asset_root="$(mktemp -d "${TMPDIR:-/tmp}/stealth-real-v025-assets.XXXXXX")"
asset_pid=""
cleanup() {
	local status=$?
	if [ -n "$asset_pid" ]; then
		kill "$asset_pid" >/dev/null 2>&1 || true
		wait "$asset_pid" >/dev/null 2>&1 || true
	fi
	rm -rf "$smoke_root" "$asset_root"
	exit "$status"
}
trap cleanup EXIT

cp -a "$fixture_root"/. "$smoke_root"/
for asset_version in "$bridge_version" "$target_version"; do
	mkdir -p "$asset_root/$asset_version/telemetry" "$asset_root/$asset_version/console/deploy"
	cp "$repo_root/compose.production.yaml" "$asset_root/$asset_version/compose.production.yaml"
	cp "$repo_root/compose.setup.yaml" "$asset_root/$asset_version/compose.setup.yaml"
	cp "$repo_root/telemetry/otel-collector.yaml" "$asset_root/$asset_version/telemetry/otel-collector.yaml"
	cp "$repo_root/telemetry/host-metrics.yaml" "$asset_root/$asset_version/telemetry/host-metrics.yaml"
	cp "$repo_root/telemetry/docker-logs.yaml" "$asset_root/$asset_version/telemetry/docker-logs.yaml"
	cp "$repo_root/telemetry/docker-stats.yaml" "$asset_root/$asset_version/telemetry/docker-stats.yaml"
	cp "$repo_root/console/deploy/nginx.conf" "$asset_root/$asset_version/console/deploy/nginx.conf"
done

config_value() {
	local key="$1"
	awk -F= -v wanted="$key" '$1 == wanted { value = substr($0, index($0, "=") + 1) } END { print value }' "$source_env"
}

set_config_value() {
	local key="$1" value="$2"
	if grep -q "^${key}=" "$smoke_root/config.env"; then
		sed -i "s|^${key}=.*|${key}=${value}|" "$smoke_root/config.env"
	else
		printf '%s=%s\n' "$key" "$value" >>"$smoke_root/config.env"
	fi
}

run_id="${GITHUB_RUN_ID:-local}"
set_config_value COMPOSE_PROJECT_NAME "stealth-v025-upgrade-${run_id}"
set_config_value STEALTH_NETWORK_NAME "stealth_v025_upgrade_network_${run_id}"
set_config_value DOCKER_GID "$(config_value DOCKER_GID)"
set_config_value PROXY_HTTP_BIND "127.0.0.1"
set_config_value PROXY_HTTP_PORT "$(config_value PROXY_HTTP_PORT)"
set_config_value API_HOST_PORT "$(config_value API_HOST_PORT)"
set_config_value CONSOLE_HOST_PORT "$(config_value CONSOLE_HOST_PORT)"
chmod 600 "$smoke_root/config.env"

api_image="$(config_value STEALTH_API_IMAGE)"
worker_image="$(config_value STEALTH_WORKER_IMAGE)"
migrate_image="$(config_value STEALTH_MIGRATE_IMAGE)"
console_image="$(config_value STEALTH_CONSOLE_IMAGE)"
collector_image="$(config_value OTEL_COLLECTOR_IMAGE)"
logs_image="$(config_value OTEL_DOCKER_LOGS_COLLECTOR_IMAGE)"
proxy_image="$(config_value STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE)"
for required_image in "$api_image" "$worker_image" "$migrate_image" "$console_image" "$collector_image" "$logs_image" "$proxy_image"; do
	if [ -z "$required_image" ] || ! docker image inspect "$required_image" >/dev/null 2>&1; then
		printf 'required locally-built smoke image is unavailable: %s\n' "$required_image" >&2
		exit 1
	fi
done

# The historical fixture deliberately has one operator image override. Keep it
# unchanged in config.env and make the locally built API image satisfy it.
docker tag "$api_image" "stealth-api:v025-operator-override"

# Release-owned v0.2.5 image tags are advanced by MigrateReleaseConfig. Make
# the target release references resolve locally without changing the target
# Compose or the migration policy.
docker tag "$worker_image" "ghcr.io/stealth-deplover/stealth-worker:${target_version}"
docker tag "$migrate_image" "ghcr.io/stealth-deplover/stealth-migrate:${target_version}"
docker tag "$console_image" "ghcr.io/stealth-deplover/stealth-console:${target_version}"
docker tag "$collector_image" "ghcr.io/stealth-deplover/stealth-otel-collector:${target_version}"
docker tag "$logs_image" "ghcr.io/stealth-deplover/stealth-otel-docker-logs:${target_version}"
docker tag "$proxy_image" "ghcr.io/stealth-deplover/stealth-telemetry-docker-proxy:${target_version}"

python3 -c '
import functools
import http.server
import sys

directory = sys.argv[1]
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=directory)
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
print(server.server_port, flush=True)
server.serve_forever()
' "$asset_root" >"$asset_root/server.log" 2>&1 &
asset_pid=$!
asset_port=""
for _ in $(seq 1 30); do
	asset_port="$(head -n 1 "$asset_root/server.log" 2>/dev/null || true)"
	if [ -n "$asset_port" ]; then
		break
	fi
	sleep 0.1
done
if [ -z "$asset_port" ]; then
	printf '%s\n' 'real v0.2.5 asset server did not start' >&2
	exit 1
fi

export STEALTH_INSTALL_DIR="$smoke_root"
export STEALTH_REAL_V025_UPGRADE_ROOT="$smoke_root"
export STEALTH_REAL_V025_ASSET_BASE="http://127.0.0.1:$asset_port"
export STEALTH_REAL_V025_BRIDGE_VERSION="$bridge_version"
export STEALTH_REAL_V025_TARGET_VERSION="$target_version"
go test ./internal/cli -run '^TestRealV025UpgradeSmoke$' -count=1

# The Go handoff uses a temporary loopback listener for the target binary's
# StepVerify checks. Restore the actual workflow ports before the migrated root
# is handed to the production smoke; these are operator/runtime settings, not
# release-managed asset content.
source_proxy_port="$(awk -F= '$1 == "PROXY_HTTP_PORT" { value = substr($0, index($0, "=") + 1) } END { print value }' "$source_env")"
source_api_port="$(awk -F= '$1 == "API_HOST_PORT" { value = substr($0, index($0, "=") + 1) } END { print value }' "$source_env")"
source_console_port="$(awk -F= '$1 == "CONSOLE_HOST_PORT" { value = substr($0, index($0, "=") + 1) } END { print value }' "$source_env")"
set_config_value PROXY_HTTP_PORT "$source_proxy_port"
set_config_value API_HOST_PORT "$source_api_port"
set_config_value CONSOLE_HOST_PORT "$source_console_port"
chmod 600 "$smoke_root/config.env"

docker compose --env-file "$smoke_root/config.env" -f "$smoke_root/compose.production.yaml" config --quiet
ENV_FILE="$smoke_root/config.env" \
COMPOSE_FILE="$smoke_root/compose.production.yaml" \
SMOKE_REMOVE_VOLUMES=true \
"$repo_root/scripts/compose-production-smoke.sh"
