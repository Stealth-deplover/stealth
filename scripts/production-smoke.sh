#!/usr/bin/env bash
set -Eeuo pipefail

api_url="${API_URL:-http://127.0.0.1:18080}"
console_url="${CONSOLE_URL:-http://127.0.0.1:13000}"
proxy_url="${PROXY_URL:-}"
attempts="${SMOKE_ATTEMPTS:-60}"
interval="${SMOKE_INTERVAL_SECONDS:-2}"

if ! [[ "$attempts" =~ ^[1-9][0-9]*$ && "$interval" =~ ^[1-9][0-9]*$ ]]; then
	printf 'SMOKE_ATTEMPTS and SMOKE_INTERVAL_SECONDS must be positive integers\n' >&2
	exit 2
fi

wait_for() {
	local name="$1"
	local url="$2"
	local response
	for attempt in $(seq 1 "$attempts"); do
		if response="$(curl --fail --silent --show-error --max-time 5 "$url")"; then
			printf '%s: %s\n' "$name" "$url"
			printf '%s\n' "$response" | head -c 300
			printf '\n'
			return 0
		fi
		if [ "$attempt" -lt "$attempts" ]; then
			sleep "$interval"
		fi
	done
	printf 'smoke check timed out after %s attempts: %s\n' "$attempts" "$url" >&2
	return 1
}

wait_for "API liveness" "${api_url%/}/healthz"
wait_for "API readiness" "${api_url%/}/readyz"
wait_for "API build metadata" "${api_url%/}/version"
wait_for "Console" "${console_url%/}/"

if [ -n "$proxy_url" ]; then
	wait_for "Reverse proxy" "${proxy_url%/}/"
fi

printf 'HTTP production smoke checks passed\n'
