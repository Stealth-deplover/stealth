#!/usr/bin/env bash
set -Eeuo pipefail

# Validate the production Traefik boundary against the rendered Compose model
# and the release-managed static/dynamic files. This is intentionally separate
# from the running smoke: a topology violation must fail before containers
# start, while the smoke proves the read-only runtime boundary in Docker.
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

compose=(docker compose --env-file "$env_file" -f "$compose_file")
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
if ! printf '%s\n' "$traefik_block" | grep -Fq 'read_only: true'; then
	printf '%s\n' 'Traefik mounts/root filesystem are not read-only in rendered Compose' >&2
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

for required in \
	'routers:' \
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

printf '%s\n' 'Traefik security regression passed: file provider only, private ingress, read-only config, no socket, no dashboard'
