#!/usr/bin/env bash
set -Eeuo pipefail

# This is intentionally separate from the fresh-install Compose smoke. It
# materializes a v0.2.5-era telemetry layout, runs the current target release's
# install-engine configuration transaction against it, then runs the existing
# full HTTP/OTLP/Docker-log/Docker-metric/host-metric smoke using that migrated
# installation root.

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
source_env="${ENV_FILE:-$repo_root/.env.production}"
target_version="${STEALTH_UPGRADE_SMOKE_VERSION:-v0.2.6}"

if ! command -v docker >/dev/null 2>&1; then
	printf '%s\n' 'managed asset upgrade smoke requires Docker' >&2
	exit 2
fi
if [ ! -f "$source_env" ]; then
	printf 'environment file not found: %s\n' "$source_env" >&2
	exit 2
fi

smoke_root="$(mktemp -d "${TMPDIR:-/tmp}/stealth-managed-upgrade.XXXXXX")"
asset_root="$(mktemp -d "${TMPDIR:-/tmp}/stealth-managed-assets.XXXXXX")"
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

mkdir -p "$smoke_root/telemetry" "$smoke_root/console/deploy" "$asset_root/$target_version/telemetry" "$asset_root/$target_version/console/deploy" "$asset_root/$target_version/traefik/dynamic/generated"
cp "$repo_root/compose.production.yaml" "$asset_root/$target_version/compose.production.yaml"
cp "$repo_root/telemetry/otel-collector.yaml" "$asset_root/$target_version/telemetry/otel-collector.yaml"
cp "$repo_root/telemetry/host-metrics.yaml" "$asset_root/$target_version/telemetry/host-metrics.yaml"
cp "$repo_root/telemetry/docker-logs.yaml" "$asset_root/$target_version/telemetry/docker-logs.yaml"
cp "$repo_root/telemetry/docker-stats.yaml" "$asset_root/$target_version/telemetry/docker-stats.yaml"
cp "$repo_root/console/deploy/nginx.conf" "$asset_root/$target_version/console/deploy/nginx.conf"
cp "$repo_root/traefik/traefik.yaml" "$asset_root/$target_version/traefik/traefik.yaml"
cp "$repo_root/traefik/dynamic/core.yaml" "$asset_root/$target_version/traefik/dynamic/core.yaml"
cp "$repo_root/traefik/dynamic/generated/.gitkeep" "$asset_root/$target_version/traefik/dynamic/generated/.gitkeep"
cp "$source_env" "$smoke_root/config.env"
chmod 600 "$smoke_root/config.env"

config_value() {
	local key="$1"
	sed -n "s/^${key}=//p" "$source_env" | tail -n 1
}

# The fixture removes post-#83 image keys to prove config migration restores
# them. Tag the locally built workflow images with the canonical target names
# so the subsequent Compose smoke remains hermetic and never pulls a release
# image from a registry.
collector_image="$(config_value OTEL_COLLECTOR_IMAGE)"
docker_logs_image="$(config_value OTEL_DOCKER_LOGS_COLLECTOR_IMAGE)"
docker_proxy_image="$(config_value STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE)"
for required_image in "$collector_image" "$docker_logs_image" "$docker_proxy_image"; do
	if [ -z "$required_image" ] || ! docker image inspect "$required_image" >/dev/null 2>&1; then
		printf 'required locally-built smoke image is unavailable: %s\n' "$required_image" >&2
		exit 1
	fi
done
docker tag "$collector_image" "ghcr.io/stealth-deplover/stealth-otel-collector:$target_version"
docker tag "$docker_logs_image" "ghcr.io/stealth-deplover/stealth-otel-docker-logs:$target_version"
docker tag "$docker_proxy_image" "ghcr.io/stealth-deplover/stealth-telemetry-docker-proxy:$target_version"

# Model an installation created before the PR #83 collector split and before
# managed-asset migration. The configuration remains operator state; only
# release-owned topology files are deliberately old.
sed -i \
	-e '/^OTEL_HOST_COLLECTOR_IMAGE=/d' \
	-e '/^OTEL_DOCKER_LOGS_COLLECTOR_IMAGE=/d' \
	-e '/^STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE=/d' \
	-e '/^STEALTH_TELEMETRY_INGEST_NETWORK_NAME=/d' \
	-e '/^OTEL_DOCKER_LOGS_VOLUME_NAME=/d' \
	"$smoke_root/config.env"
printf '%s\n' 'UPGRADE_SECRET_MARKER=upgrade-secret-must-survive' >>"$smoke_root/config.env"
printf '%s\n' 'v0.2.5' >"$smoke_root/VERSION"
printf '%s\n' \
	'services:' \
	'  otel-collector:' \
	'    volumes:' \
	'      - /:/hostfs:ro' \
	'    cap_add:' \
	'      - DAC_READ_SEARCH' \
	'networks:' \
	'  stealth:' >"$smoke_root/compose.production.yaml"
printf '%s\n' \
	'receivers:' \
	'  file_log/docker:' \
	'    include:' \
	'      - /hostfs/var/lib/docker/containers/*/*-json.log' >"$smoke_root/telemetry/otel-collector.yaml"
printf '%s\n' 'receivers:' '  docker_stats:' >"$smoke_root/telemetry/docker-stats.yaml"
printf '%s\n' 'server {' '  # v0.2.5 managed proxy asset' '}' >"$smoke_root/console/deploy/nginx.conf"

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
for _ in $(seq 1 30); do
	asset_port="$(head -n 1 "$asset_root/server.log" 2>/dev/null || true)"
	if [ -n "${asset_port:-}" ]; then
		break
	fi
	sleep 0.1
done
if [ -z "${asset_port:-}" ]; then
	printf '%s\n' 'local managed-asset server did not start' >&2
	exit 1
fi

export STEALTH_UPGRADE_SMOKE_ROOT="$smoke_root"
export STEALTH_UPGRADE_SMOKE_ASSET_BASE="http://127.0.0.1:$asset_port"
export STEALTH_UPGRADE_SMOKE_VERSION="$target_version"
go test ./internal/installengine -run '^TestManagedAssetUpgradeSmoke$' -count=1

docker compose --env-file "$smoke_root/config.env" -f "$smoke_root/compose.production.yaml" config --quiet
ENV_FILE="$smoke_root/config.env" \
COMPOSE_FILE="$smoke_root/compose.production.yaml" \
SMOKE_REMOVE_VOLUMES=true \
"$repo_root/scripts/compose-production-smoke.sh"
