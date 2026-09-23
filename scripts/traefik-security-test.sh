#!/usr/bin/env bash
set -Eeuo pipefail

# Validate the production Traefik and App BuildKit boundaries against the
# rendered Compose model and release-managed files. Topology violations fail
# before containers start; the running smoke exercises the read-only boundary.
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="${COMPOSE_FILE:-$repo_root/compose.production.yaml}"
env_file="${ENV_FILE:-$repo_root/.env.production.example}"

if ! command -v docker >/dev/null 2>&1; then
	printf '%s\n' 'Traefik security test requires Docker Compose' >&2
	exit 2
fi
if [ ! -f "$compose_file" ] || [ ! -f "$env_file" ]; then
	printf 'Compose or environment file is missing: %s %s\n' "$compose_file" "$env_file" >&2
	exit 2
fi

env_value() {
	local key="$1" value
	value="$(awk -F= -v key="$key" '$1 == key { value = substr($0, index($0, "=") + 1) } END { print value }' "$env_file")"
	printf '%s\n' "${value%$'\r'}"
}

ingress_network_name="$(env_value STEALTH_INGRESS_NETWORK_NAME)"
if [ -z "$ingress_network_name" ]; then
	ingress_network_name='stealth_ingress'
fi

# Include the optional tunnel profile so the rendered topology also proves the
# reserved Cloudflared peer. The profile is part of the ingress address model,
# even though it is not the active public origin during this migration.
compose=(docker compose --env-file "$env_file" -f "$compose_file" --profile cloudflare --profile maintenance)
"${compose[@]}" config --quiet
rendered="$(mktemp "${TMPDIR:-/tmp}/stealth-traefik-compose.XXXXXX")"
cleanup() {
	rm -f "$rendered"
}
trap cleanup EXIT
"${compose[@]}" config >"$rendered"

service_block() {
	local service="$1"
	awk -v wanted="$service" '
		/^services:[[:space:]]*$/ { in_services=1; next }
		in_services && $0 ~ "^  " wanted ":[[:space:]]*$" { found=1; print; next }
		found && $0 ~ /^  [^[:space:]][^:]*:[[:space:]]*$/ { exit }
		found { print }
	' "$rendered"
}

traefik_block="$(service_block traefik)"
if [ -z "$traefik_block" ]; then
	printf '%s\n' 'Traefik service is missing from rendered production Compose' >&2
	exit 1
fi
cloudflared_block="$(service_block cloudflared)"
if [ -z "$cloudflared_block" ]; then
	printf '%s\n' 'Cloudflared service is missing from rendered production Compose' >&2
	exit 1
fi
worker_block="$(service_block worker)"
if [ -z "$worker_block" ]; then
	printf '%s\n' 'worker service is missing from rendered production Compose' >&2
	exit 1
fi
buildkit_block="$(service_block buildkit)"
if [ -z "$buildkit_block" ]; then
	printf '%s\n' 'dedicated App BuildKit service is missing from rendered production Compose' >&2
	exit 1
fi
state_init_block="$(service_block traefik-state-init)"
if [ -z "$state_init_block" ]; then
	printf '%s\n' 'Traefik state initializer is missing from rendered production Compose' >&2
	exit 1
fi
cloudflare_state_init_block="$(service_block cloudflare-state-init)"
if [ -z "$cloudflare_state_init_block" ]; then
	printf '%s\n' 'Cloudflare setup-state initializer is missing from rendered production Compose' >&2
	exit 1
fi
ingress_control_block="$(service_block ingress-control)"
if [ -z "$ingress_control_block" ]; then
	printf '%s\n' 'one-shot ingress-control service is missing from rendered production Compose' >&2
	exit 1
fi

for required in \
	'network_mode: none' \
	'read_only: true' \
	'target: /state'; do
	if ! printf '%s\n' "$state_init_block" | grep -Fq -- "$required"; then
		printf 'Traefik state initializer is missing required setting: %s\n' "$required" >&2
		exit 1
	fi
done
if ! printf '%s\n' "$traefik_block" | grep -Fq 'stealth_ingress:' || ! printf '%s\n' "$cloudflared_block" | grep -Fq 'stealth_ingress:'; then
	printf '%s\n' 'Cloudflared and Traefik must share the private stealth_ingress network' >&2
	exit 1
fi
if ! printf '%s\n' "$state_init_block" | grep -Eq 'user: "?0:0"?'; then
	printf '%s\n' 'Traefik state initializer must run as container root' >&2
	exit 1
fi
if ! printf '%s\n' "$state_init_block" | grep -Eq 'restart: "?no"?'; then
	printf '%s\n' 'Traefik state initializer must be one-shot' >&2
	exit 1
fi
if ! printf '%s\n' "$state_init_block" | grep -Eq 'source: .*/traefik/dynamic([[:space:]]|$)'; then
	printf '%s\n' 'Traefik state initializer must mount only the dynamic state directory' >&2
	exit 1
fi
for required in \
	'network_mode: none' \
	'read_only: true' \
	'target: /state' \
	'source: .*/state([[:space:]]|$)' \
	'target: /output' \
	'source: .*/state/\.cloudflare-import([[:space:]]|$)' \
	'/usr/local/bin/stealth-cloudflare-import-init' \
	'FUNCTIONS_SECRET_KEY'; do
	if ! printf '%s\n' "$cloudflare_state_init_block" | grep -Eq -- "$required"; then
		printf 'Cloudflare setup-state initializer is missing required setting: %s\n' "$required" >&2
		exit 1
	fi
done
if ! printf '%s\n' "$cloudflare_state_init_block" | grep -Eq 'restart: "?no"?'; then
	printf '%s\n' 'Cloudflare state initializer must be one-shot' >&2
	exit 1
fi
if ! printf '%s\n' "$cloudflare_state_init_block" | awk '
/source: .*\/state([[:space:]]|$)/ { source=1 }
/target: \/state$/ { target=1 }
/read_only: true/ && source && target { readonly=1 }
END { exit(readonly ? 0 : 1) }'; then
	printf '%s\n' 'Cloudflare preparation source mount is not read-only' >&2
	exit 1
fi
if ! printf '%s\n' "$cloudflare_state_init_block" | grep -Eq 'user: "?0:0"?'; then
	printf '%s\n' 'Cloudflare setup-state initializer must run as container root' >&2
	exit 1
fi
for forbidden in \
	'/var/run/docker.sock' \
	'privileged:' \
	'network_mode: host' \
	'cap_add:' \
	'hostfs' \
	'CLOUDFLARE_API_TOKEN' \
	'DATABASE_URL' \
	'REDIS_URL' \
	'cloudflare-tunnel-token'; do
	if printf '%s\n' "$cloudflare_state_init_block" | grep -Fqi -- "$forbidden"; then
		printf 'Cloudflare setup-state initializer contains forbidden setting: %s\n' "$forbidden" >&2
		exit 1
	fi
done
for forbidden in \
	'/var/run/docker.sock' \
	'privileged:' \
	'network_mode: host' \
	'cap_add:' \
	'secrets:' \
	'/traefik/traefik.yaml' \
	'/traefik/dynamic/core.yaml' \
	'DATABASE_URL' \
	'REDIS_URL' \
	'CLOUDFLARE'; do
	if printf '%s\n' "$state_init_block" | grep -Fqi -- "$forbidden"; then
		printf 'Traefik state initializer contains forbidden setting: %s\n' "$forbidden" >&2
		exit 1
	fi
done
for required in \
	'restart: "no"' \
	'read_only: true' \
	'no-new-privileges:true' \
	'networks:' \
	'ingress_control_db:' \
	'stealth_ingress:'; do
	if ! printf '%s\n' "$ingress_control_block" | grep -Fq -- "$required"; then
		printf 'ingress-control is missing required setting: %s\n' "$required" >&2
		exit 1
	fi
done
if ! printf '%s\n' "$ingress_control_block" | grep -Eq 'user: "?10001:10001"?$'; then
	printf '%s\n' 'ingress-control must run as the dedicated non-root user' >&2
	exit 1
fi
if ! printf '%s\n' "$ingress_control_block" | grep -Eq 'cap_drop: \[ALL\]|^[[:space:]]*-[[:space:]]+ALL[[:space:]]*$'; then
	printf '%s\n' 'ingress-control must drop all Linux capabilities' >&2
	exit 1
fi
if printf '%s\n' "$ingress_control_block" | grep -Eq '^    volumes:|/var/run/docker.sock|privileged:|network_mode: host|cap_add:|/var/lib/stealth/(storage|runner-staging|traefik|cloudflare-import)|setup-state\.enc|cloudflare-tunnel-token'; then
	printf '%s\n' 'ingress-control has an unnecessary mount or elevated capability' >&2
	exit 1
fi
control_environment_keys="$(printf '%s\n' "$ingress_control_block" | awk '
/^    environment:[[:space:]]*$/ { in_environment=1; next }
in_environment && /^    [^[:space:]][^:]*:[[:space:]]*$/ { exit }
in_environment && /^      [A-Z][A-Z0-9_]*:/ { sub(/^[[:space:]]+/, ""); sub(/:.*/, ""); print }
' | sort -u)"
expected_control_environment_keys="$(printf '%s\n' CLOUDFLARE_API_BASE_URL DATABASE_URL FUNCTIONS_SECRET_KEY PUBLIC_APP_URL | sort -u)"
if [ "$control_environment_keys" != "$expected_control_environment_keys" ]; then
	printf 'ingress-control environment is broader than required; keys=%s\n' "$(printf '%s' "$control_environment_keys" | paste -sd, -)" >&2
	exit 1
fi
control_networks="$(printf '%s\n' "$ingress_control_block" | awk '
/^    networks:[[:space:]]*$/ { in_networks=1; next }
in_networks && $0 ~ /^    [^[:space:]][^:]*:[[:space:]]*$/ { exit }
in_networks && $0 ~ /^      [^[:space:]][^:]*:/ { sub(/^[[:space:]]+/, ""); sub(/:.*/, ""); print }
')"
if [ "$(printf '%s\n' "$control_networks" | sed '/^$/d' | sort -u | paste -sd, -)" != 'ingress_control_db,stealth_ingress' ]; then
	printf 'ingress-control must join only the database and local Traefik networks; rendered networks=%s\n' "$control_networks" >&2
	exit 1
fi


for required in \
	'image: moby/buildkit:v0.33.0-rootless@sha256:80b15f0735e87bab7bf59ec4d695dfb4a7cfb25521cf56dc75d6f256285b63ef' \
	'read_only: true' \
	'seccomp=unconfined' \
	'apparmor=unconfined' \
	'systempaths=unconfined'; do
	if ! printf '%s\n' "$buildkit_block" | grep -Fq -- "$required"; then
		printf 'App BuildKit service is missing required setting: %s\n' "$required" >&2
		exit 1
	fi
done
if ! printf '%s\n' "$buildkit_block" | grep -Eq '^[[:space:]]*user: "?1000:1000"?$'; then
	printf '%s\n' 'App BuildKit must run as the dedicated non-root user' >&2
	exit 1
fi
buildkit_state_volume="$(env_value APPS_BUILDKIT_STATE_VOLUME)"
if [ -z "$buildkit_state_volume" ]; then
	buildkit_state_volume='stealth_app_buildkit_state'
fi
if ! printf '%s\n' "$buildkit_block" | grep -Fq 'source: buildkit_state' ||
	! printf '%s\n' "$buildkit_block" | grep -Fq 'target: /home/user/.local/share/buildkit'; then
	printf '%s\n' 'App BuildKit must mount only its dedicated persistent cache volume' >&2
	exit 1
fi
buildkit_volume_block="$(awk '
/^volumes:[[:space:]]*$/ { in_volumes=1; next }
in_volumes && /^  buildkit_state:[[:space:]]*$/ { found=1; print; next }
found && /^  [^[:space:]][^:]*:[[:space:]]*$/ { exit }
found { print }
' "$rendered")"
if [ -z "$buildkit_volume_block" ] || ! printf '%s\n' "$buildkit_volume_block" | grep -Fq "name: $buildkit_state_volume"; then
	printf '%s\n' 'App BuildKit cache must use its configured persistent volume name' >&2
	exit 1
fi
if ! printf '%s\n' "$buildkit_block" | awk '
function check_mount() {
  if (mount_type == "bind" && source ~ /\/buildkit\/buildkitd\.toml$/ && target == "/etc/buildkit/buildkitd.toml" && read_only) found=1
}
/^    volumes:[[:space:]]*$/ { in_volumes=1; next }
in_volumes && /^    [^[:space:]][^:]*:[[:space:]]*$/ { exit }
in_volumes && /^      - type:/ { check_mount(); mount_type=$3; source=""; target=""; read_only=0; next }
in_volumes && /source:/ { sub(/^[[:space:]]*source:[[:space:]]*/, ""); source=$0 }
in_volumes && /target:/ { sub(/^[[:space:]]*target:[[:space:]]*/, ""); target=$0 }
in_volumes && /read_only:[[:space:]]*true/ { read_only=1 }
END { check_mount(); exit(found ? 0 : 1) }
'; then
	printf '%s\n' 'App BuildKit daemon configuration must be mounted read-only' >&2
	exit 1
fi
for forbidden in '/var/run/docker.sock' 'privileged:' 'network_mode: host' 'pid: host' 'ipc: host' 'ports:' 'stealth:' 'telemetry_store:' 'ingress_control_db:' 'stealth_storage:' 'app_build_staging:' 'DATABASE_URL' 'REDIS_URL' 'FUNCTIONS_SECRET_KEY' 'CLOUDFLARE'; do
	if printf '%s\n' "$buildkit_block" | grep -Fqi -- "$forbidden"; then
		printf 'App BuildKit service contains forbidden setting: %s\n' "$forbidden" >&2
		exit 1
	fi
done
if printf '%s\n' "$buildkit_block" | grep -Eq '^[[:space:]]*privileged:[[:space:]]*true|^[[:space:]]*ports:'; then
	printf '%s\n' 'App BuildKit must not be privileged or publish a host port' >&2
	exit 1
fi

service_networks() {
	local block="$1"
	printf '%s\n' "$block" | awk '
/^    networks:[[:space:]]*$/ { in_networks=1; next }
in_networks && /^    [^[:space:]][^:]*:[[:space:]]*$/ { exit }
in_networks && /^      [^[:space:]][^:]*:/ { sub(/^[[:space:]]+/, ""); sub(/:.*/, ""); print }
'
}
buildkit_networks="$(service_networks "$buildkit_block" | sort -u | paste -sd, -)"
worker_networks="$(service_networks "$worker_block" | sort -u | paste -sd, -)"
if [ "$buildkit_networks" != 'app_build' ]; then
	printf 'App BuildKit must join only app_build; rendered networks=%s\n' "$buildkit_networks" >&2
	exit 1
fi
if ! printf '%s\n' "$worker_networks" | tr ',' '\n' | grep -Fxq app_build; then
	printf 'worker must join app_build for private BuildKit access; rendered networks=%s\n' "$worker_networks" >&2
	exit 1
fi
while IFS= read -r service; do
	case "$service" in
		worker|buildkit) continue ;;
	esac
	block="$(service_block "$service")"
	if printf '%s\n' "$(service_networks "$block")" | grep -Fxq app_build; then
		printf 'App build network must not include service %s\n' "$service" >&2
		exit 1
	fi
done < <("${compose[@]}" config --services)

buildkit_config="$(dirname -- "$compose_file")/buildkit/buildkitd.toml"
if [ ! -f "$buildkit_config" ]; then
	printf 'BuildKit daemon configuration is missing: %s\n' "$buildkit_config" >&2
	exit 1
fi
for required in 'rootless = true' 'noProcessSandbox = false' 'gc = true' 'maxUsedSpace = "10GB"' 'max-parallelism = 2' '[frontend."dockerfile.v0"]'; do
	if ! grep -Fq -- "$required" "$buildkit_config"; then
		printf 'BuildKit daemon configuration is missing setting: %s\n' "$required" >&2
		exit 1
	fi
done
for forbidden in 'insecure-entitlements' 'security.insecure' 'network.host' 'gateway.v0'; do
	if grep -Fq -- "$forbidden" "$buildkit_config"; then
		printf 'BuildKit daemon config contains forbidden setting: %s\n' "$forbidden" >&2
		exit 1
	fi
done

if ! grep -Fq 'TRAEFIK_RELOAD_FILE: /var/lib/stealth/traefik/.reload.yaml' "$compose_file"; then
	printf '%s\n' 'Compose does not keep the reload sentinel at the top-level dynamic path' >&2
	exit 1
fi

for forbidden in \
	'/var/run/docker.sock' \
	'privileged: true' \
	'network_mode: host' \
	'/hostfs' \
	'cap_add:' \
	'providers.docker' \
	'api.insecure: true' \
	'dashboard: true' \
	'forwardedHeaders.insecure: true'; do
	if printf '%s\n' "$traefik_block" | grep -Fqi -- "$forbidden"; then
		printf 'Traefik service contains forbidden setting: %s\n' "$forbidden" >&2
		exit 1
	fi
done

for required in \
	'read_only: true' \
	'no-new-privileges:true' \
	'networks:' \
	'stealth_ingress:'; do
	if ! printf '%s\n' "$traefik_block" | grep -Fq -- "$required"; then
		printf 'Traefik service is missing required setting: %s\n' "$required" >&2
		exit 1
	fi
done
if ! printf '%s\n' "$traefik_block" | grep -Fq 'cap_drop:' || ! printf '%s\n' "$traefik_block" | grep -Eq 'cap_drop: \[ALL\]|^[[:space:]]*-[[:space:]]+ALL[[:space:]]*$'; then
	printf '%s\n' 'Traefik does not drop all Linux capabilities' >&2
	exit 1
fi
if ! printf '%s\n' "$traefik_block" | grep -Eq 'source: .*/traefik/traefik\.yaml'; then
	printf '%s\n' 'Traefik static file mount source is not the release-managed traefik directory' >&2
	exit 1
fi
if ! printf '%s\n' "$traefik_block" | grep -Fq 'target: /etc/traefik/traefik.yaml'; then
	printf '%s\n' 'Traefik static file mount target is incorrect' >&2
	exit 1
fi
if ! printf '%s\n' "$traefik_block" | grep -Eq 'source: .*/traefik/dynamic([[:space:]]|$)' || ! printf '%s\n' "$traefik_block" | grep -Fq 'target: /etc/traefik/dynamic'; then
	printf '%s\n' 'Traefik dynamic directory mount is incorrect' >&2
	exit 1
fi
if printf '%s\n' "$traefik_block" | grep -Eq 'source: .*/traefik/dynamic/(generated|\.reload\.yaml)|target: /var/lib/stealth/traefik/(generated|\.reload\.yaml)'; then
	printf '%s\n' 'Traefik must not receive a writable reconciler path mount' >&2
	exit 1
fi
if ! printf '%s\n' "$traefik_block" | grep -Fq 'read_only: true'; then
	printf '%s\n' 'Traefik mounts/root filesystem are not read-only in rendered Compose' >&2
	exit 1
fi

# The existing worker owns other bounded capabilities, including the Docker
# socket for Site/Function builds. Atomic replacement of the top-level reload
# sentinel requires the dynamic parent directory. core.yaml is overlaid at the
# same path as a read-only bind mount, while the static Traefik file and
# installation root are never mounted into the worker. The runtime smoke also
# proves that the nested read-only core mount rejects writes and replacement.
for required in \
	'source: .*/traefik/dynamic$' \
	'target: /var/lib/stealth/traefik' \
	'source: .*/traefik/dynamic/core\.yaml' \
	'target: /var/lib/stealth/traefik/core\.yaml' \
	'source: .*/state/\.cloudflare-import$' \
	'target: /var/lib/stealth/cloudflare-import'; do
	if ! printf '%s\n' "$worker_block" | grep -Eq -- "$required"; then
		printf 'worker is missing the required Traefik state mount: %s\n' "$required" >&2
		exit 1
	fi
done
if ! printf '%s\n' "$worker_block" | awk '
/source: .*\/state\/\.cloudflare-import$/ { source=1 }
/target: \/var\/lib\/stealth\/cloudflare-import$/ { target=1 }
/read_only: true/ && source && target { readonly=1 }
END { exit(readonly ? 0 : 1) }'; then
	printf '%s\n' 'worker Cloudflare import directory is not explicitly read-only' >&2
	exit 1
fi
if printf '%s\n' "$worker_block" | grep -Eqi '/run/secrets/cloudflare-tunnel-token|source: .*/state([[:space:]]|$)|setup-state\.enc|/var/lib/stealth/setup-state'; then
	printf '%s\n' 'worker receives the full setup state or Cloudflared tunnel-token mount' >&2
	exit 1
fi
if grep -Fq 'cp "$${source}"' "$compose_file" || grep -Fq 'setup-state.enc:ro' "$compose_file"; then
	printf '%s\n' 'Cloudflare initializer must derive a narrow artifact rather than copy full setup state' >&2
	exit 1
fi
if ! printf '%s\n' "$worker_block" | awk '
/source: .*[\\/]traefik[\\/]dynamic[\\/]core\.yaml/ { core_source=1 }
/target: \/var\/lib\/stealth\/traefik\/core\.yaml/ { core_target=1 }
/read_only: true/ && core_source && core_target { core_read_only=1 }
END { exit(core_read_only ? 0 : 1) }'; then
	printf '%s\n' 'worker core.yaml mount is not explicitly read-only' >&2
	exit 1
fi
if printf '%s\n' "$worker_block" | grep -Eq 'source: .*/traefik/traefik\.yaml|target: /etc/traefik|target: /var/lib/stealth/traefik/traefik\.yaml'; then
	printf '%s\n' 'worker has a static or installation-root Traefik mount' >&2
	exit 1
fi

if printf '%s\n' "$traefik_block" | grep -Eq '^      (stealth|telemetry_store|telemetry_ingest|telemetry_docker):'; then
	printf '%s\n' 'Traefik is attached to a prohibited private network' >&2
	exit 1
fi
if printf '%s\n' "$traefik_block" | grep -Eq '^    ports:'; then
	printf '%s\n' 'Traefik must not publish a host port in the parallel-ingress release' >&2
	exit 1
fi
traefik_networks="$(printf '%s\n' "$traefik_block" | awk '
/^    networks:[[:space:]]*$/ { in_networks=1; next }
in_networks && $0 ~ /^    [^[:space:]][^:]*:[[:space:]]*$/ { exit }
in_networks && $0 ~ /^      [^[:space:]][^:]*:[[:space:]]*$/ { sub(/^[[:space:]]+/, ""); sub(/:[[:space:]]*$/, ""); print }
')"
if [ "$(printf '%s\n' "$traefik_networks" | sed '/^$/d' | sort -u | paste -sd, -)" != 'stealth_ingress' ]; then
	printf 'Traefik must join only stealth_ingress; rendered networks=%s\n' "$traefik_networks" >&2
	exit 1
fi

ingress_block="$(awk '
	/^networks:[[:space:]]*$/ { in_networks=1; next }
	in_networks && $0 ~ /^  stealth_ingress:[[:space:]]*$/ { found=1; print; next }
	found && $0 ~ /^  [^[:space:]][^:]*:[[:space:]]*$/ { exit }
	found { print }
' "$rendered")"
if ! printf '%s\n' "$ingress_block" | grep -Fq 'internal: true'; then
	printf '%s\n' 'Traefik ingress network is not internal in rendered Compose' >&2
	exit 1
fi
if ! printf '%s\n' "$ingress_block" | grep -Fq "name: $ingress_network_name"; then
	printf 'rendered ingress network name does not match persisted %s\n' "$ingress_network_name" >&2
	exit 1
fi

ingress_subnet="$(env_value STEALTH_INGRESS_NETWORK_SUBNET)"
ingress_ip_range="$(env_value STEALTH_INGRESS_IP_RANGE)"
traefik_ingress_ip="$(env_value STEALTH_TRAEFIK_INGRESS_IP)"
cloudflared_ingress_ip="$(env_value STEALTH_CLOUDFLARED_INGRESS_IP)"
for value in "$ingress_subnet" "$ingress_ip_range" "$traefik_ingress_ip" "$cloudflared_ingress_ip"; do
	if [ -z "$value" ]; then
		printf '%s\n' 'configured Traefik ingress subnet, pool, and peer values are required' >&2
		exit 1
	fi
done
if ! python3 - "$ingress_subnet" "$ingress_ip_range" "$traefik_ingress_ip" "$cloudflared_ingress_ip" <<'PY'
import ipaddress
import sys

subnet = ipaddress.ip_network(sys.argv[1], strict=False)
pool = ipaddress.ip_network(sys.argv[2], strict=False)
traefik = ipaddress.ip_address(sys.argv[3])
cloudflared = ipaddress.ip_address(sys.argv[4])
private = (
    ipaddress.ip_network("10.0.0.0/8"),
    ipaddress.ip_network("172.16.0.0/12"),
    ipaddress.ip_network("192.168.0.0/16"),
)
if subnet.version != 4 or pool.version != 4 or not any(subnet.subnet_of(network) for network in private):
    raise SystemExit("ingress subnet must be an RFC1918 IPv4 network")
if pool.prefixlen <= subnet.prefixlen or not pool.subnet_of(subnet) or pool.prefixlen > 29:
    raise SystemExit("ingress dynamic pool is not a usable child subnet")
for name, address in (("Traefik", traefik), ("Cloudflared", cloudflared)):
    if address.version != 4 or address not in subnet:
        raise SystemExit(f"{name} peer is outside the ingress subnet")
    if address in (subnet.network_address, subnet.broadcast_address) or address in pool:
        raise SystemExit(f"{name} peer is not reserved outside the dynamic pool")
if traefik == cloudflared:
    raise SystemExit("Traefik and Cloudflared peers must be distinct")
PY
then
	printf '%s\n' 'configured ingress subnet/pool/peer model is invalid' >&2
	exit 1
fi
for required in \
	"subnet: $ingress_subnet" \
	"ip_range: $ingress_ip_range" \
	"ipv4_address: $traefik_ingress_ip" \
	"ipv4_address: $cloudflared_ingress_ip"; do
	if ! grep -Fq -- "$required" "$rendered"; then
		printf 'rendered Compose is missing persisted ingress setting: %s\n' "$required" >&2
		exit 1
	fi
done
for required in \
	'STEALTH_INGRESS_NETWORK_SUBNET:?set STEALTH_INGRESS_NETWORK_SUBNET' \
	'STEALTH_INGRESS_IP_RANGE:?set STEALTH_INGRESS_IP_RANGE' \
	'STEALTH_TRAEFIK_INGRESS_IP:?set STEALTH_TRAEFIK_INGRESS_IP' \
	'STEALTH_CLOUDFLARED_INGRESS_IP:?set STEALTH_CLOUDFLARED_INGRESS_IP'; do
	if ! grep -Fq -- "$required" "$compose_file"; then
		printf 'Compose does not require configurable ingress setting: %s\n' "$required" >&2
		exit 1
	fi
	done
if grep -Fq '172.31.0.0/24' "$compose_file" || grep -Fq '172.31.0.10' "$compose_file" || grep -Fq '172.31.0.254' "$compose_file"; then
	printf '%s\n' 'Compose contains an install-wide hard-coded ingress subnet or peer' >&2
	exit 1
fi

static_file="$(dirname -- "$compose_file")/traefik/traefik.yaml"
core_file="$(dirname -- "$compose_file")/traefik/dynamic/core.yaml"
for file in "$static_file" "$core_file"; do
	if [ ! -f "$file" ]; then
		printf 'Traefik managed configuration is missing: %s\n' "$file" >&2
		exit 1
	fi
done

for required in \
	'providers:' \
	'directory: /etc/traefik/dynamic' \
	'dashboard: false' \
	'insecure: false' \
	'entryPoint: health' \
	'trustedIPs:' \
	'__STEALTH_CLOUDFLARED_TRUSTED_CIDR__' \
	'format: json' \
	'Authorization: drop' \
	'Cookie: drop' \
	'Set-Cookie: drop' \
	'Proxy-Authorization: drop'; do
	if ! grep -Fq -- "$required" "$static_file"; then
		printf 'Traefik static configuration is missing: %s\n' "$required" >&2
		exit 1
	fi
done
if grep -Eiq 'docker[[:space:]]*:' "$static_file" || grep -Eiq 'forwardedHeaders:[[:space:]]*$' "$static_file" && grep -Fq 'insecure: true' "$static_file"; then
	printf '%s\n' 'Traefik static configuration enables Docker discovery or insecure forwarded headers' >&2
	exit 1
fi
if ! grep -Fq '__STEALTH_CLOUDFLARED_TRUSTED_CIDR__' "$static_file"; then
	printf '%s\n' 'Traefik static configuration does not use an installation-rendered Cloudflared peer' >&2
	exit 1
fi

for required in \
	'routers:' \
	'middlewares:' \
	'stealth-security-headers:' \
	'stealth-admin-realtime:' \
	'stealth-project-realtime:' \
	'Path(`/v1/admin/realtime`)' \
	'PathRegexp(`^/v1/projects/[0-9a-fA-F-]{36}/realtime$`)' \
	'X-Content-Type-Options: "nosniff"' \
	'Referrer-Policy: "strict-origin-when-cross-origin"' \
	'Permissions-Policy: "camera=(), microphone=(), geolocation=(), payment=()"' \
	'X-Frame-Options: "DENY"' \
	'Content-Security-Policy:' \
	'stealth-api:' \
	'stealth-console:' \
	'url: http://api:8080' \
	'url: http://console:3000'; do
	if ! grep -Fq -- "$required" "$core_file"; then
		printf 'Traefik core dynamic configuration is missing: %s\n' "$required" >&2
		exit 1
	fi
done
if grep -Fq 'api@internal' "$core_file" || grep -Fq 'docker@internal' "$core_file"; then
	printf '%s\n' 'Traefik core dynamic configuration exposes an internal dashboard/Docker service' >&2
	exit 1
fi
if grep -Eq '/(healthz|readyz|version)' "$core_file" && grep -Fq 'stealth-api:' "$core_file"; then
	printf '%s\n' 'Traefik public API router exposes an internal health or version endpoint' >&2
	exit 1
fi
if ! grep -Fq 'stealth-security-headers' "$core_file"; then
	printf '%s\n' 'Traefik core routers are missing the release-managed security-header middleware' >&2
	exit 1
fi
if grep -Fq 'stealth-request-body-limit' "$core_file" || grep -Fq 'maxRequestBodyBytes' "$core_file" || grep -Fq 'buffering:' "$core_file"; then
	printf '%s\n' 'Traefik core routes must not use full-request buffering; application streaming limits are authoritative' >&2
	exit 1
fi
if grep -Fq 'Strict-Transport-Security:' "$core_file"; then
	printf '%s\n' 'Traefik internal HTTP core configuration unconditionally emits HSTS' >&2
	exit 1
fi

printf '%s\n' 'Traefik security regression passed: file provider only, private configurable ingress, reserved peers, protected worker route writer, no socket, no dashboard'
