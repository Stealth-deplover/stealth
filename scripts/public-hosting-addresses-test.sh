#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/public-hosting-addresses.sh"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/stealth-address-selection.XXXXXX")"
trap 'rm -rf -- "$tmp_dir"' EXIT

fail() {
	printf 'public address selection test failed: %s\n' "$1" >&2
	exit 1
}

assert_selected() {
	local kind="$1" table="$2" expected_host="$3"
	shift 3
	if ! select_public_addresses "$kind" "$table"; then
		fail "$kind selection unexpectedly failed"
	fi
	if [ "$PUBLIC_SELECTED_HOST" != "$expected_host" ]; then
		fail "$kind selected '$PUBLIC_SELECTED_HOST', expected '$expected_host'"
	fi
	if [ "${#PUBLIC_RESOLVE_ARGS[@]}" -ne "$#" ]; then
		fail "$kind retained ${#PUBLIC_RESOLVE_ARGS[@]} resolve arguments, expected $#"
	fi
	local index expected_count="$#"
	for ((index = 0; index < expected_count; index++)); do
		if [ "${PUBLIC_RESOLVE_ARGS[$index]}" != "$1" ]; then
			fail "$kind resolve argument $index was '${PUBLIC_RESOLVE_ARGS[$index]}', expected '$1'"
		fi
		shift
	done
}

cat >"$tmp_dir/addresses.tsv" <<'EOF'
console	cloud.example.com	203.0.113.10
site	portfolio.apps.example.com	203.0.113.20
console	CLOUD.example.com.	172.18.2.3
site	portfolio.apps.example.com	2606:4700::2222
console	cloud.example.com	2606:4700::1111
EOF

assert_selected console "$tmp_dir/addresses.tsv" cloud.example.com \
	--resolve cloud.example.com:443:203.0.113.10 \
	--resolve cloud.example.com:443:172.18.2.3 \
	--resolve 'cloud.example.com:443:[2606:4700::1111]'
console_url="https://cloud.example.com/"
normalized_console_url="$(python3 "$SCRIPT_DIR/public_hosting_redirect.py" "$console_url" "$console_url" "$PUBLIC_SELECTED_HOST" 443)"
if [ "$normalized_console_url" != "$console_url" ]; then
	fail "Console probe URL '$console_url' was validated against '$PUBLIC_SELECTED_HOST' as '$normalized_console_url'"
fi
assert_selected site "$tmp_dir/addresses.tsv" portfolio.apps.example.com \
	--resolve portfolio.apps.example.com:443:203.0.113.20 \
	--resolve 'portfolio.apps.example.com:443:[2606:4700::2222]'
site_url="https://portfolio.apps.example.com/"
normalized_site_url="$(python3 "$SCRIPT_DIR/public_hosting_redirect.py" "$site_url" "$site_url" "$PUBLIC_SELECTED_HOST" 443)"
if [ "$normalized_site_url" != "$site_url" ]; then
	fail "Site probe URL '$site_url' was validated against '$PUBLIC_SELECTED_HOST' as '$normalized_site_url'"
fi

cat >"$tmp_dir/reordered.tsv" <<'EOF'
site	portfolio.apps.example.com	203.0.113.20
console	cloud.example.com	203.0.113.10
site	portfolio.apps.example.com	2606:4700::2222
console	cloud.example.com	172.18.2.3
EOF
assert_selected console "$tmp_dir/reordered.tsv" cloud.example.com \
	--resolve cloud.example.com:443:203.0.113.10 \
	--resolve cloud.example.com:443:172.18.2.3
assert_selected site "$tmp_dir/reordered.tsv" portfolio.apps.example.com \
	--resolve portfolio.apps.example.com:443:203.0.113.20 \
	--resolve 'portfolio.apps.example.com:443:[2606:4700::2222]'

cat >"$tmp_dir/inconsistent.tsv" <<'EOF'
console	cloud.example.com	203.0.113.10
site	portfolio.apps.example.com	203.0.113.20
console	other.example.com	172.18.2.3
EOF
if select_public_addresses console "$tmp_dir/inconsistent.tsv" 2>"$tmp_dir/inconsistent.err"; then
	fail 'inconsistent Console hostnames were accepted'
fi
if ! grep -Fq 'inconsistent hostnames in validated public address set for console' "$tmp_dir/inconsistent.err"; then
	fail 'inconsistent Console hostnames did not produce an actionable failure'
fi
if [ -n "$PUBLIC_SELECTED_HOST" ] || [ "${#PUBLIC_RESOLVE_ARGS[@]}" -ne 0 ]; then
	fail 'failed selection retained a partial hostname or address list'
fi

printf '%s\n' 'Public acceptance address-selection regressions passed.'
