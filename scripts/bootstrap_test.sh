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

latest_bin="${temporary_dir}/latest-bin"
mkdir -p "$latest_bin"
printf '%s\n' '#!/bin/sh' 'case "$1" in -s) printf "Linux" ;; -m) printf "x86_64" ;; esac' > "${latest_bin}/uname"
chmod 0755 "${latest_bin}/uname"
cat > "${latest_bin}/curl" <<'MOCK_EOF'
#!/bin/sh
latest_requested=0
for arg in "$@"; do
	case "$arg" in
		*releases/latest*) latest_requested=1 ;;
	esac
done
if [ -n "${STEALTH_TEST_CURL_LOG:-}" ]; then
	printf '%s\n' "$*" >> "$STEALTH_TEST_CURL_LOG"
fi
if [ "$latest_requested" = "1" ]; then
	if [ -n "${STEALTH_TEST_MARKER:-}" ]; then
		: > "$STEALTH_TEST_MARKER"
	fi
	if [ "${STEALTH_TEST_LATEST_FAIL:-0}" = "1" ]; then
		exit 22
	fi
	printf '%s' "${STEALTH_TEST_LATEST_URL:-}"
	exit 0
fi
exit 1
MOCK_EOF
chmod 0755 "${latest_bin}/curl"

if STEALTH_VERSION= STEALTH_TEST_LATEST_URL="https://github.com/Stealth-deplover/stealth/releases/tag/v0.1.0" \
	STEALTH_TEST_LATEST_FAIL=0 PATH="${latest_bin}:${PATH}" \
	"$script" >"${temporary_dir}/latest-valid.out" 2>&1; then
	printf '%s\n' 'valid latest redirect was not handled' >&2
	exit 1
fi
grep -q 'could not download .* for v0\.1\.0' "${temporary_dir}/latest-valid.out"
if grep -q 'release version must match' "${temporary_dir}/latest-valid.out"; then
	printf '%s\n' 'valid latest redirect hit version validation' >&2
	exit 1
fi
if grep -q 'no stable GitHub release' "${temporary_dir}/latest-valid.out"; then
	printf '%s\n' 'valid latest redirect reported no release' >&2
	exit 1
fi
if grep -q 'failed to determine latest release' "${temporary_dir}/latest-valid.out"; then
	printf '%s\n' 'valid latest redirect reported undetermined release' >&2
	exit 1
fi

if STEALTH_VERSION= STEALTH_TEST_LATEST_URL="https://github.com/Stealth-deplover/stealth/releases/tag/v10.20.30" \
	STEALTH_TEST_LATEST_FAIL=0 PATH="${latest_bin}:${PATH}" \
	"$script" >"${temporary_dir}/latest-multidigit.out" 2>&1; then
	printf '%s\n' 'multi-digit latest redirect was not handled' >&2
	exit 1
fi
grep -q 'could not download .* for v10\.20\.30' "${temporary_dir}/latest-multidigit.out"

if STEALTH_VERSION= STEALTH_TEST_LATEST_URL="https://github.com/Stealth-deplover/stealth/releases" \
	STEALTH_TEST_LATEST_FAIL=0 PATH="${latest_bin}:${PATH}" \
	"$script" >"${temporary_dir}/no-release.out" 2>&1; then
	printf '%s\n' 'no-release redirect was accepted' >&2
	exit 1
fi
grep -q 'no stable GitHub release is available yet' "${temporary_dir}/no-release.out"
if grep -q 'release version must match' "${temporary_dir}/no-release.out"; then
	printf '%s\n' 'no-release case leaked parser output' >&2
	exit 1
fi

if STEALTH_VERSION= STEALTH_TEST_LATEST_URL="https://github.com/Stealth-deplover/stealth/releases/" \
	STEALTH_TEST_LATEST_FAIL=0 PATH="${latest_bin}:${PATH}" \
	"$script" >"${temporary_dir}/no-release-slash.out" 2>&1; then
	printf '%s\n' 'trailing-slash no-release redirect was accepted' >&2
	exit 1
fi
grep -q 'no stable GitHub release is available yet' "${temporary_dir}/no-release-slash.out"

if STEALTH_VERSION= STEALTH_TEST_LATEST_URL="https://example.com/unexpected" \
	STEALTH_TEST_LATEST_FAIL=0 PATH="${latest_bin}:${PATH}" \
	"$script" >"${temporary_dir}/malformed.out" 2>&1; then
	printf '%s\n' 'malformed redirect was accepted' >&2
	exit 1
fi
grep -q 'failed to determine latest release' "${temporary_dir}/malformed.out"

if STEALTH_VERSION= STEALTH_TEST_LATEST_URL="https://github.com/Stealth-deplover/stealth/releases/tag/" \
	STEALTH_TEST_LATEST_FAIL=0 PATH="${latest_bin}:${PATH}" \
	"$script" >"${temporary_dir}/empty-tag.out" 2>&1; then
	printf '%s\n' 'empty tag redirect was accepted' >&2
	exit 1
fi
grep -q 'failed to determine latest release' "${temporary_dir}/empty-tag.out"

if STEALTH_VERSION= STEALTH_TEST_LATEST_FAIL=1 PATH="${latest_bin}:${PATH}" \
	"$script" >"${temporary_dir}/latest-fail.out" 2>&1; then
	printf '%s\n' 'latest network failure was accepted' >&2
	exit 1
fi
grep -q 'could not resolve the latest stable release' "${temporary_dir}/latest-fail.out"

for invalid_version in latest releases main v1 v1.2 1.2.3 v1.2.3-beta; do
	if PATH="${mock_bin}:${PATH}" "$script" --version "$invalid_version" >"${temporary_dir}/invalid-${invalid_version}.out" 2>&1; then
		printf '%s\n' "invalid version ${invalid_version} was accepted" >&2
		exit 1
	fi
	grep -q 'release version must match' "${temporary_dir}/invalid-${invalid_version}.out"
done

rm -f "${temporary_dir}/explicit-marker"
if STEALTH_VERSION= STEALTH_TEST_MARKER="${temporary_dir}/explicit-marker" \
	STEALTH_TEST_LATEST_FAIL=1 PATH="${latest_bin}:${PATH}" \
	"$script" --version v0.1.0 >"${temporary_dir}/explicit-valid.out" 2>&1; then
	printf '%s\n' 'explicit valid version was accepted without download stub failure' >&2
	exit 1
fi
grep -q 'could not download .* for v0\.1\.0' "${temporary_dir}/explicit-valid.out"
if [ -e "${temporary_dir}/explicit-marker" ]; then
	printf '%s\n' 'explicit --version contacted /releases/latest' >&2
	exit 1
fi

rm -f "${temporary_dir}/explicit-env-marker"
if STEALTH_TEST_MARKER="${temporary_dir}/explicit-env-marker" \
	STEALTH_TEST_LATEST_FAIL=1 PATH="${latest_bin}:${PATH}" \
	STEALTH_VERSION=v0.1.0 "$script" >"${temporary_dir}/explicit-env.out" 2>&1; then
	printf '%s\n' 'STEALTH_VERSION was accepted without download stub failure' >&2
	exit 1
fi
grep -q 'could not download .* for v0\.1\.0' "${temporary_dir}/explicit-env.out"
if [ -e "${temporary_dir}/explicit-env-marker" ]; then
	printf '%s\n' 'STEALTH_VERSION contacted /releases/latest' >&2
	exit 1
fi

if STEALTH_VERSION= PATH="${latest_bin}:${PATH}" "$script" --version=v1.2.3 >"${temporary_dir}/version-equals.out" 2>&1; then
	printf '%s\n' '--version= form unexpectedly succeeded without download stub' >&2
	exit 1
fi
grep -q 'could not download' "${temporary_dir}/version-equals.out"

arm_bin="${temporary_dir}/arm-bin"
mkdir -p "$arm_bin"
printf '%s\n' '#!/bin/sh' 'case "$1" in -s) printf "Linux" ;; -m) printf "aarch64" ;; esac' > "${arm_bin}/uname"
chmod 0755 "${arm_bin}/uname"
cp "${latest_bin}/curl" "${arm_bin}/curl"
chmod 0755 "${arm_bin}/curl"

rm -f "${temporary_dir}/curl-amd64.log"
if STEALTH_TEST_CURL_LOG="${temporary_dir}/curl-amd64.log" PATH="${latest_bin}:${PATH}" \
	"$script" --version v1.2.3 >"${temporary_dir}/arch-amd64.out" 2>&1; then
	printf '%s\n' 'amd64 mapping unexpectedly succeeded without download stub' >&2
	exit 1
fi
grep -q 'stealth_Linux_x86_64.tar.gz' "${temporary_dir}/curl-amd64.log"

rm -f "${temporary_dir}/curl-arm64.log"
if STEALTH_TEST_CURL_LOG="${temporary_dir}/curl-arm64.log" PATH="${arm_bin}:${PATH}" \
	"$script" --version v1.2.3 >"${temporary_dir}/arch-arm64.out" 2>&1; then
	printf '%s\n' 'arm64 mapping unexpectedly succeeded without download stub' >&2
	exit 1
fi
grep -q 'stealth_Linux_arm64.tar.gz' "${temporary_dir}/curl-arm64.log"

printf '%s\n' 'bootstrap tests passed'
