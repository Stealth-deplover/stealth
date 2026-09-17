#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
workflow="$repo_root/.github/workflows/release.yml"

if grep -Fq 'raw.githubusercontent.com/${GITHUB_REPOSITORY}/HEAD/scripts/bootstrap.sh' "$workflow"; then
	printf '%s\n' 'release installer smoke test must not fetch bootstrap.sh from HEAD' >&2
	exit 1
fi
if ! grep -Fq 'raw.githubusercontent.com/${GITHUB_REPOSITORY}/${RELEASE_VERSION}/scripts/bootstrap.sh' "$workflow"; then
	printf '%s\n' 'release installer smoke test is not pinned to RELEASE_VERSION' >&2
	exit 1
fi

printf '%s\n' 'release workflow bootstrap revision test passed'
