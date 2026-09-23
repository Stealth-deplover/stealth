#!/usr/bin/env bash
set -Eeuo pipefail

console_url="${STEALTH_CONSOLE_URL:-}"
site_url="${STEALTH_SITE_URL:-}"
workload_domain="${STEALTH_WORKLOAD_BASE_DOMAIN:-}"
site_sha256="${STEALTH_SITE_SHA256:-}"

if [ -z "$console_url" ] || [ -z "$site_url" ] || [ -z "$workload_domain" ]; then
	cat >&2 <<'EOF'
Set STEALTH_CONSOLE_URL, STEALTH_SITE_URL, and STEALTH_WORKLOAD_BASE_DOMAIN.
Optionally set STEALTH_SITE_SHA256 to verify a deterministic Site response.
EOF
	exit 2
fi
if ! command -v curl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
	printf '%s\n' 'curl and python3 are required' >&2
	exit 2
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/stealth-public-acceptance.XXXXXX")"
trap 'rm -rf -- "$tmp_dir"' EXIT
chmod 700 "$tmp_dir"

# Validate that both inputs are public HTTPS origins, resolve through public
# DNS, and that the Site host is exactly one label below the declared platform
# workload domain. Pin curl to these validated public answers (while retaining
# the hostname for TLS SNI and Host) to prevent DNS rebinding.
python3 - "$console_url" "$site_url" "$workload_domain" "$site_sha256" "$tmp_dir/public-addresses.tsv" <<'PY'
import ipaddress
import re
import socket
import sys
from urllib.parse import urlsplit

console_url, site_url, workload_domain, digest, address_file = sys.argv[1:]

def canonical(value):
    return value.rstrip(".").encode("idna").decode("ascii").lower()

def parse_origin(value, label):
    parsed = urlsplit(value)
    try:
        port = parsed.port
    except ValueError:
        raise SystemExit(f"{label} URL has an invalid port")
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None or
            parsed.password is not None or parsed.query or parsed.fragment or
            parsed.path not in ("", "/") or port not in (None, 443)):
        raise SystemExit(f"{label} must be an HTTPS origin without credentials, path, query, or custom port")
    try:
        host = canonical(parsed.hostname)
        ipaddress.ip_address(host)
    except ValueError:
        pass
    else:
        raise SystemExit(f"{label} must use a DNS hostname, not an IP address")
    return host

def resolve_public(host, label):
    try:
        answers = socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM)
    except OSError:
        raise SystemExit(f"{label} DNS lookup failed")
    addresses = sorted({answer[4][0] for answer in answers})
    if not addresses:
        raise SystemExit(f"{label} DNS returned no addresses")
    for raw in addresses:
        address = ipaddress.ip_address(raw)
        if not address.is_global:
            raise SystemExit(f"{label} resolves to a non-public address; request refused")
    return addresses

console_host = parse_origin(console_url, "STEALTH_CONSOLE_URL")
site_host = parse_origin(site_url, "STEALTH_SITE_URL")
base = canonical(workload_domain)
if len(base) > 253 or not base or any(not part or len(part) > 63 for part in base.split(".")):
    raise SystemExit("STEALTH_WORKLOAD_BASE_DOMAIN is invalid")
suffix = "." + base
if not site_host.endswith(suffix):
    raise SystemExit("STEALTH_SITE_URL hostname is outside STEALTH_WORKLOAD_BASE_DOMAIN")
label = site_host[:-len(suffix)]
if not label or "." in label or not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label):
    raise SystemExit("STEALTH_SITE_URL must be exactly one platform label below STEALTH_WORKLOAD_BASE_DOMAIN")
if digest and not re.fullmatch(r"[0-9a-fA-F]{64}", digest):
    raise SystemExit("STEALTH_SITE_SHA256 must contain 64 hexadecimal characters")

console_addresses = resolve_public(console_host, "Console hostname")
site_addresses = resolve_public(site_host, "Site hostname")
with open(address_file, "w", encoding="ascii") as target:
    for label, host, addresses in (("console", console_host, console_addresses), ("site", site_host, site_addresses)):
        for address in addresses:
            target.write(f"{label}\t{host}\t{address}\n")
print("Public DNS resolved to globally routable addresses for Console and Site hosts.")
PY

response_header() {
	python3 - "$1" "$2" <<'PY'
import sys

path, wanted = sys.argv[1:]
blocks = []
current = {}
with open(path, "r", encoding="iso-8859-1") as source:
    for line in source:
        if line.startswith("HTTP/"):
            if current:
                blocks.append(current)
            current = {}
        elif ":" in line:
            key, value = line.split(":", 1)
            current.setdefault(key.strip().lower(), []).append(value.strip())
if current:
    blocks.append(current)
if blocks:
    values = blocks[-1].get(wanted.lower(), [])
    print(", ".join(values))
PY
}

probe() {
	local label="$1" url="$2" kind="$3" file_base="$4" address_kind="$5" headers body
	local result rc status remote_ip location address_label address_host address_ip
	local -a resolve_args=()
	while IFS=$'\t' read -r address_label address_host address_ip; do
		if [ "$address_label" = "$address_kind" ]; then
			if [[ "$address_ip" == *:* ]]; then
				address_ip="[$address_ip]"
			fi
			resolve_args+=(--resolve "$address_host:443:$address_ip")
		fi
	done < "$tmp_dir/public-addresses.tsv"
	if [ "${#resolve_args[@]}" -eq 0 ]; then
		printf '%s: no previously validated public DNS address is available\n' "$label" >&2
		return 1
	fi
	headers="$tmp_dir/$file_base.headers"
	body="$tmp_dir/$file_base.body"
	set +e
	result="$(curl --noproxy '*' --proto '=https' --silent --show-error \
		--connect-timeout 5 --max-time 15 --max-filesize 65536 \
		"${resolve_args[@]}" \
		--dump-header "$headers" --output "$body" \
		--write-out $'%{http_code}\t%{remote_ip}' "$url" 2>/dev/null)"
	rc=$?
	set -e
	if [ "$rc" -ne 0 ]; then
		case "$rc" in
			6) printf '%s: DNS failure\n' "$label" >&2 ;;
			35|51|58|60|77) printf '%s: TLS certificate/handshake failure\n' "$label" >&2 ;;
			28) printf '%s: public HTTPS request timed out\n' "$label" >&2 ;;
			*) printf '%s: public HTTPS transport failure (curl exit %s)\n' "$label" "$rc" >&2 ;;
		esac
		return 1
	fi
	IFS=$'\t' read -r status remote_ip <<<"$result"
	if [ -z "$status" ] || [ -z "$remote_ip" ]; then
		printf '%s: public HTTPS response did not include an HTTP status and remote address\n' "$label" >&2
		return 1
	fi
	if ! python3 - "$remote_ip" <<'PY'
import ipaddress
import sys

try:
    if not ipaddress.ip_address(sys.argv[1]).is_global:
        raise SystemExit(1)
except ValueError:
    raise SystemExit(1)
PY
	then
		printf '%s: connected address was not globally routable\n' "$label" >&2
		return 1
	fi
	case "$status" in
		3*)
			location="$(response_header "$headers" Location)"
			if [[ "${location,,}" == http://* ]]; then
				printf '%s: redirect downgrade to HTTP was rejected\n' "$label" >&2
			else
				printf '%s: unexpected redirect (not followed), HTTP %s\n' "$label" "$status" >&2
			fi
			return 1
			;;
		5*) printf '%s: public origin returned HTTP %s\n' "$label" "$status" >&2; return 1 ;;
	esac
	case "$kind" in
		root|site)
			if [ "$status" -lt 200 ] || [ "$status" -ge 300 ]; then
				printf '%s: expected a successful response, got HTTP %s\n' "$label" "$status" >&2
				return 1
			fi
			;;
		api)
			if [ "$status" != 401 ]; then
				printf '%s: expected unauthenticated API HTTP 401, got HTTP %s\n' "$label" "$status" >&2
				return 1
			fi
			;;
		*)
			if [ "$status" -lt 200 ] || [ "$status" -ge 500 ]; then
				printf '%s: unexpected HTTP status %s\n' "$label" "$status" >&2
				return 1
			fi
			;;
	esac
	printf '%s: HTTP %s over verified public HTTPS\n' "$label" "$status"
	if [ "$kind" = root ] || [ "$kind" = api ]; then
		check_browser_headers "$headers" "$label"
		check_hsts "$headers" "$label"
	fi
	if [ "$kind" = site ] && [ -n "$site_sha256" ]; then
		local actual
		actual="$(sha256sum "$body" | awk '{print $1}')"
		if [ "${actual,,}" != "${site_sha256,,}" ]; then
			printf '%s: response body SHA-256 did not match the requested digest\n' "$label" >&2
			return 1
		fi
		printf '%s: deterministic response SHA-256 matched\n' "$label"
	fi
}

check_browser_headers() {
	local headers="$1" label="$2" name actual expected
	while IFS=$'\t' read -r name expected; do
		actual="$(response_header "$headers" "$name")"
		if [ "$actual" != "$expected" ]; then
			printf '%s: security header mismatch for %s\n' "$label" "$name" >&2
			return 1
		fi
	done <<'EOF'
X-Content-Type-Options	nosniff
Referrer-Policy	strict-origin-when-cross-origin
Permissions-Policy	camera=(), microphone=(), geolocation=(), payment=()
X-Frame-Options	DENY
Content-Security-Policy	default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self';
EOF
}

check_hsts() {
	local headers="$1" label="$2" value
	value="$(response_header "$headers" Strict-Transport-Security)"
	if ! python3 - "$value" <<'PY'
import sys

directives = {}
for item in sys.argv[1].split(";"):
    parts = item.strip().split("=", 1)
    directives[parts[0].lower()] = parts[1].strip() if len(parts) == 2 else None
try:
    max_age = int(directives.get("max-age", "-1"))
except (TypeError, ValueError):
    max_age = -1
if max_age < 31536000 or "includesubdomains" not in directives or directives["includesubdomains"] is not None:
    raise SystemExit(1)
PY
	then
		printf '%s: HSTS must include max-age >= 31536000 and includeSubDomains\n' "$label" >&2
		return 1
	fi
}

console_origin="${console_url%/}"
site_origin="${site_url%/}"
probe 'Console root' "$console_origin/" root console-root console
probe 'Console API route /v1/account' "$console_origin/v1/account" api console-api console
probe 'Console /healthz routing' "$console_origin/healthz" path console-healthz console
probe 'Console /readyz routing' "$console_origin/readyz" path console-readyz console
probe 'Console /version routing' "$console_origin/version" path console-version console
probe 'Console unknown-path fallback' "$console_origin/__stealth_public_hosting_acceptance_unknown__" path console-unknown console
probe 'Platform Site root' "$site_origin/" site platform-site site

printf '%s\n' 'Public hosting acceptance passed. Run `stealth ingress verify --site-hostname <label>.<workload-domain>` on the host to confirm provider-observed routing and edge TLS readiness.'
