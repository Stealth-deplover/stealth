#!/bin/sh
set -eu

compose_file=${STEALTH_SETUP_COMPOSE_FILE_FOR_TEST:-compose.setup.yaml}
dockerfile=${STEALTH_DOCKERFILE_FOR_TEST:-Dockerfile}

if [ ! -f "$compose_file" ]; then
	printf 'setup security check: missing %s\n' "$compose_file" >&2
	exit 1
fi

setup_service=$(awk '
	/^  setup:/ { in_setup = 1 }
	in_setup && NR != 1 && /^  [A-Za-z0-9_.-]+:/ && $0 !~ /^  setup:/ { exit }
	in_setup { print }
' "$compose_file")
if printf '%s\n' "$setup_service" | grep -E '/var/run/docker\.sock|privileged:[[:space:]]*true' >/dev/null 2>&1; then
	printf 'setup security check: setup service regained Docker authority\n' >&2
	exit 1
fi

setup_image=$(awk '
	/^FROM runtime-base AS setup$/ { in_setup = 1; next }
	in_setup && /^FROM / { exit }
	in_setup { print }
' "$dockerfile")
if printf '%s\n' "$setup_image" | grep -E 'docker-cli|docker[[:space:]]+compose|/usr/bin/docker' >/dev/null 2>&1; then
	printf 'setup security check: setup image still installs or mounts Docker tooling\n' >&2
	exit 1
fi

if [ -n "${STEALTH_SETUP_IMAGE_FOR_TEST:-}" ]; then
	if ! command -v docker >/dev/null 2>&1; then
		printf 'setup security check: Docker is required for image binary validation\n' >&2
		exit 1
	fi
	if docker run --rm --entrypoint /bin/sh "$STEALTH_SETUP_IMAGE_FOR_TEST" -ec '
		if command -v docker >/dev/null 2>&1; then exit 1; fi
		if command -v docker-compose >/dev/null 2>&1; then exit 1; fi
		if docker compose version >/dev/null 2>&1; then exit 1; fi
	'; then
		printf 'setup security check: setup image has no Docker binaries\n'
	else
		printf 'setup security check: setup image contains Docker tooling\n' >&2
		exit 1
	fi
fi

printf 'setup security check: no Docker socket, privileged mode, or setup Docker tooling\n'
