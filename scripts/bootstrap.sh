#!/bin/sh
set -eu

repository="Stealth-deplover/stealth"
release_root="https://github.com/${repository}/releases/download"
tmp_root="${TMPDIR:-/tmp}"
temporary_dir=""

fail() {
	printf 'stealth bootstrap: %s\n' "$1" >&2
	exit 1
}

cleanup() {
	if [ -n "$temporary_dir" ] && [ -d "$temporary_dir" ]; then
		rm -rf "$temporary_dir"
	fi
}
trap cleanup EXIT

version="${STEALTH_VERSION:-}"
explicit_version=0
[ -n "$version" ] && explicit_version=1
while [ "$#" -gt 0 ]; do
	case "$1" in
		--version)
			[ "$#" -ge 2 ] || fail "--version requires a value"
			version="$2"
			explicit_version=1
			shift 2
			;;
		--version=*)
			version=${1#--version=}
			explicit_version=1
			shift
			;;
		-h|--help)
			printf '%s\n' 'Usage: bootstrap.sh [--version vMAJOR.MINOR.PATCH[(-rc.N)]]'
			exit 0
			;;
		*)
			fail "unknown option: $1"
			;;
	esac
done

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"
command -v awk >/dev/null 2>&1 || fail "awk is required"

os_name="$(uname -s 2>/dev/null || true)"
arch_name="$(uname -m 2>/dev/null || true)"
[ "$os_name" = "Linux" ] || fail "unsupported operating system: ${os_name:-unknown}; Linux is supported"

case "$arch_name" in
	x86_64|amd64)
		asset="stealth_Linux_x86_64.tar.gz"
		;;
	aarch64|arm64)
		asset="stealth_Linux_arm64.tar.gz"
		;;
	*)
		fail "unsupported architecture: ${arch_name:-unknown}; use Linux amd64 or arm64"
		;;
esac

if [ -z "$version" ]; then
	latest_url="$(curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
		--output /dev/null --write-out '%{url_effective}' \
		"https://github.com/${repository}/releases/latest")" || fail "could not resolve the latest stable release"
	while [ "$latest_url" != "${latest_url%/}" ]; do
		latest_url=${latest_url%/}
	done
	case "$latest_url" in
		"https://github.com/${repository}/releases/tag/"*)
			tag=${latest_url##*/releases/tag/}
			case "$tag" in
				''|*/*|*\?*|*#*) fail "failed to determine latest release" ;;
			esac
			version=$tag
			;;
		"https://github.com/${repository}/releases"|"https://github.com/${repository}/releases/latest")
			fail "no stable GitHub release is available yet"
			;;
		*)
			fail "failed to determine latest release"
			;;
	esac
fi

version_error="release version must match vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N: $version"
case "$version" in
	v*) ;;
	*) fail "$version_error" ;;
esac
version_parts=${version#v}
release_candidate_number=""
case "$version_parts" in
	*-rc.*)
		stable_version_parts=${version_parts%%-rc.*}
		release_candidate_number=${version_parts#*-rc.}
		;;
	*-*)
		fail "$version_error"
		;;
	*)
		stable_version_parts=$version_parts
		;;
esac
case "$stable_version_parts" in
	*.*.*) ;;
	*) fail "$version_error" ;;
esac
major=${stable_version_parts%%.*}
version_remainder=${stable_version_parts#*.}
minor=${version_remainder%%.*}
patch_version=${version_remainder#*.}
case "$major:$minor:$patch_version" in
	''|*[!0-9:]*|*:*:*:*) fail "$version_error" ;;
esac
if [ -n "$release_candidate_number" ]; then
	case "$release_candidate_number" in
		''|*[!0-9]*) fail "$version_error" ;;
		*) ;;
	esac
fi
if [ "$explicit_version" -eq 0 ] && [ -n "$release_candidate_number" ]; then
	fail "latest GitHub release is not stable"
fi

temporary_dir="$(mktemp -d "${tmp_root%/}/stealth-bootstrap.XXXXXX")" || fail "could not create a temporary directory"
archive="${temporary_dir}/${asset}"
checksums="${temporary_dir}/checksums.txt"

curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
	"${release_root}/${version}/${asset}" -o "$archive" || fail "could not download ${asset} for ${version}"
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
	"${release_root}/${version}/checksums.txt" -o "$checksums" || fail "could not download release checksums"

expected="$(awk -v wanted="$asset" '$2 == wanted { print $1; exit }' "$checksums")"
case "$expected" in
	'') fail "checksum for ${asset} is missing from checksums.txt" ;;
	*[!0-9a-fA-F]*) fail "checksum for ${asset} is invalid" ;;
	*) ;;
esac
[ "${#expected}" -eq 64 ] || fail "checksum for ${asset} is invalid"
actual="$(sha256sum "$archive" | awk '{print $1}')"
[ "$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')" = "$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')" ] || fail "download verification failed"

extract_dir="${temporary_dir}/extract"
mkdir -p "$extract_dir"
tar -xzf "$archive" -C "$extract_dir" stealth || fail "release archive does not contain the stealth binary"
[ -f "$extract_dir/stealth" ] || fail "release archive did not produce a stealth binary"

user_home="${HOME:-}"
[ -n "$user_home" ] || fail 'HOME is not set; choose a writable user home before installing'

if [ -n "${STEALTH_BIN_DIR:-}" ]; then
	bin_dir="$STEALTH_BIN_DIR"
	mkdir -p "$bin_dir" 2>/dev/null || fail "cannot create STEALTH_BIN_DIR: $bin_dir"
else
	bin_dir="${user_home%/}/.local/bin"
	if ! mkdir -p "$bin_dir" 2>/dev/null || [ ! -w "$bin_dir" ]; then
		bin_dir="${user_home%/}/.stealth/bin"
		mkdir -p "$bin_dir" 2>/dev/null || fail "no writable CLI install directory under $user_home"
	fi
fi

temporary_binary="${bin_dir}/.stealth.$$"
cp "$extract_dir/stealth" "$temporary_binary" || fail "could not install the CLI binary"
chmod 0755 "$temporary_binary" || fail "could not make the CLI executable"
mv -f "$temporary_binary" "${bin_dir}/stealth" || fail "could not activate the CLI binary"

printf 'Stealth CLI %s installed at %s/stealth\n' "$version" "$bin_dir"
case ":${PATH:-}:" in
	*":${bin_dir}:"*) ;;
	*) printf 'Add %s to PATH to call `stealth` directly.\n' "$bin_dir" ;;
esac

cleanup
temporary_dir=""
if [ -t 1 ] && [ -t 2 ]; then
	exec "${bin_dir}/stealth" install
fi
exec "${bin_dir}/stealth" install --no-wait
