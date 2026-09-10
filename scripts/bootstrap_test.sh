#!/bin/sh
set -eu

script="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/bootstrap.sh"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/stealth-bootstrap-test.XXXXXX")"
trap 'rm -rf "$temporary_dir"' EXIT

mock_bin="${temporary_dir}/bin"
mkdir -p "$mock_bin"

printf '%s\n' '#!/bin/sh' 'case "$1" in -s) printf "Linux" ;; -m) printf "x86_64" ;; esac' > "${mock_bin}/uname"
chmod 0755 "${mock_bin}/uname"

unsupported_bin="${temporary_dir}/unsupported-bin"
mkdir -p "$unsupported_bin"
printf '%s\n' '#!/bin/sh' 'case "$1" in -s) printf "Darwin" ;; -m) printf "arm64" ;; esac' > "${unsupported_bin}/uname"
chmod 0755 "${unsupported_bin}/uname"
if PATH="${unsupported_bin}:${PATH}" "$script" --version v1.2.3 >"${temporary_dir}/unsupported.out" 2>&1; then
	printf '%s\n' 'unsupported OS was accepted' >&2
	exit 1
fi
grep -q 'unsupported operating system' "${temporary_dir}/unsupported.out"

architecture_bin="${temporary_dir}/architecture-bin"
mkdir -p "$architecture_bin"
printf '%s\n' '#!/bin/sh' 'case "$1" in -s) printf "Linux" ;; -m) printf "ppc64le" ;; esac' > "${architecture_bin}/uname"
chmod 0755 "${architecture_bin}/uname"
if PATH="${architecture_bin}:${PATH}" "$script" --version v1.2.3 >"${temporary_dir}/architecture.out" 2>&1; then
	printf '%s\n' 'unsupported architecture was accepted' >&2
	exit 1
fi
grep -q 'unsupported architecture' "${temporary_dir}/architecture.out"

if PATH="${mock_bin}:${PATH}" "$script" --version v1.2.3-rc.1 >"${temporary_dir}/version.out" 2>&1; then
	printf '%s\n' 'pre-release version was accepted' >&2
	exit 1
fi
grep -q 'release version must match' "${temporary_dir}/version.out"

printf '%s\n' '#!/bin/sh' \
	'output=""' \
	'while [ "$#" -gt 0 ]; do' \
	'  if [ "$1" = "-o" ]; then output="$2"; shift 2; else shift; fi' \
	'done' \
	'case "$output" in' \
	'  *checksums.txt) printf "%064d  stealth_Linux_x86_64.tar.gz\n" 0 > "$output" ;;' \
	'  *) printf "%s" invalid-archive > "$output" ;;' \
	'esac' > "${mock_bin}/curl"
chmod 0755 "${mock_bin}/curl"

if PATH="${mock_bin}:${PATH}" HOME="$temporary_dir" "$script" --version v1.2.3 >"${temporary_dir}/checksum.out" 2>&1; then
	printf '%s\n' 'checksum mismatch was accepted' >&2
	exit 1
fi
grep -q 'download verification failed' "${temporary_dir}/checksum.out"

failure_bin="${temporary_dir}/failure-bin"
mkdir -p "$failure_bin"
printf '%s\n' '#!/bin/sh' 'exit 1' > "${failure_bin}/curl"
chmod 0755 "${failure_bin}/curl"
if PATH="${failure_bin}:${PATH}" "$script" --version v1.2.3 >"${temporary_dir}/download.out" 2>&1; then
	printf '%s\n' 'download failure was accepted' >&2
	exit 1
fi
grep -q 'could not download' "${temporary_dir}/download.out"

printf '%s\n' 'bootstrap tests passed'
