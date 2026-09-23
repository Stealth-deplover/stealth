#!/usr/bin/env python3
"""Validate one public-hosting Console redirect and print its pinned URL."""

import ipaddress
import re
import sys
from urllib.parse import unquote, urljoin, urlsplit, urlunsplit


def canonical_host(value):
    return value.rstrip(".").encode("idna").decode("ascii").lower()


def safe_redirect(current_url, location, expected_host, expected_port="443"):
    location = location.strip()
    if not location or location.startswith("//") or "\\" in location:
        raise ValueError("redirect Location uses an unsafe URL form")
    if any(ord(char) < 32 or ord(char) == 127 for char in location):
        raise ValueError("redirect Location contains control characters")
    if re.search(r"%(?![0-9a-fA-F]{2})", location):
        raise ValueError("redirect Location contains an invalid escape")
    reference = urlsplit(location)
    if reference.username is not None or reference.password is not None or reference.fragment:
        raise ValueError("redirect Location contains credentials or a fragment")
    if reference.scheme and (reference.scheme.lower() != "https" or not reference.netloc):
        raise ValueError("absolute redirects must use a complete HTTPS authority")
    if reference.netloc and not reference.hostname:
        raise ValueError("redirect Location has no hostname")
    target = urlsplit(urljoin(current_url, location))
    if target.scheme.lower() != "https" or target.username is not None or target.password is not None or target.fragment:
        raise ValueError("redirect must remain on HTTPS without credentials or fragments")
    try:
        actual_host = canonical_host(target.hostname or "")
        configured_host = canonical_host(expected_host)
        actual_port = target.port or 443
        configured_port = int(expected_port)
    except ValueError as error:
        raise ValueError("redirect hostname or port is invalid") from error
    try:
        ipaddress.ip_address(actual_host)
    except ValueError:
        pass
    else:
        raise ValueError("redirect must use a DNS hostname, not an IP address")
    if not actual_host or actual_host == "localhost" or actual_host.endswith((".localhost", ".local", ".internal", ".lan")):
        raise ValueError("redirect must use the configured public DNS hostname")
    if actual_host != configured_host:
        raise ValueError("redirect changed the configured Console hostname")
    if ":" in target.netloc and target.netloc.rsplit(":", 1)[1] != str(actual_port):
        raise ValueError("redirect port is not canonical")
    if actual_port != configured_port:
        raise ValueError("redirect changed the configured HTTPS port")
    path = target.path or "/"
    decoded_target = unquote(path + "?" + target.query)
    if "\\" in decoded_target or any(ord(char) < 32 or ord(char) == 127 for char in decoded_target):
        raise ValueError("redirect target contains an unsafe path")
    authority = configured_host if configured_port == 443 else f"{configured_host}:{configured_port}"
    return urlunsplit(("https", authority, path, target.query, ""))


def main(argv):
    if len(argv) != 5:
        raise SystemExit("usage: public_hosting_redirect.py CURRENT_URL LOCATION HOST PORT")
    try:
        print(safe_redirect(*argv[1:]))
    except ValueError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)


if __name__ == "__main__":
    main(sys.argv)
