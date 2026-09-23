#!/usr/bin/env bash

# select_public_addresses selects only rows for the requested kind. The DNS
# preparation step has already canonicalized the host and validated every IP;
# this helper keeps the per-probe selection deterministic and preserves every
# validated address for curl --resolve.
select_public_addresses() {
	local wanted_kind="$1" addresses_file="$2"
	local address_label address_host address_ip normalized_host found=0
	PUBLIC_SELECTED_HOST=""
	PUBLIC_RESOLVE_ARGS=()

	if [ "$wanted_kind" != "console" ] && [ "$wanted_kind" != "site" ]; then
		printf 'unknown public address kind: %s\n' "$wanted_kind" >&2
		return 1
	fi
	if [ ! -r "$addresses_file" ]; then
		printf 'validated public address table is unavailable\n' >&2
		return 1
	fi
	while IFS=$'\t' read -r address_label address_host address_ip; do
		if [ "$address_label" != "$wanted_kind" ]; then
			continue
		fi
		normalized_host="${address_host%.}"
		normalized_host="${normalized_host,,}"
		if [ -z "$normalized_host" ] || [[ ! "$normalized_host" =~ ^[a-z0-9.-]+$ ]] || [[ "$normalized_host" == .* ]] || [[ "$normalized_host" == *..* ]]; then
			printf 'invalid canonical hostname in validated public address table\n' >&2
			PUBLIC_SELECTED_HOST=""
			PUBLIC_RESOLVE_ARGS=()
			return 1
		fi
		if [ -z "$PUBLIC_SELECTED_HOST" ]; then
			PUBLIC_SELECTED_HOST="$normalized_host"
		elif [ "$PUBLIC_SELECTED_HOST" != "$normalized_host" ]; then
			printf 'inconsistent hostnames in validated public address set for %s\n' "$wanted_kind" >&2
			PUBLIC_SELECTED_HOST=""
			PUBLIC_RESOLVE_ARGS=()
			return 1
		fi
		if [ -z "$address_ip" ]; then
			printf 'missing IP address in validated public address table\n' >&2
			PUBLIC_SELECTED_HOST=""
			PUBLIC_RESOLVE_ARGS=()
			return 1
		fi
		if [[ "$address_ip" == *:* ]]; then
			if [[ ! "$address_ip" =~ ^[0-9A-Fa-f:.]+$ ]]; then
				printf 'invalid IPv6 address in validated public address table\n' >&2
				PUBLIC_SELECTED_HOST=""
				PUBLIC_RESOLVE_ARGS=()
				return 1
			fi
			address_ip="[$address_ip]"
		elif [[ ! "$address_ip" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]]; then
			printf 'invalid IPv4 address in validated public address table\n' >&2
			PUBLIC_SELECTED_HOST=""
			PUBLIC_RESOLVE_ARGS=()
			return 1
		fi
		PUBLIC_RESOLVE_ARGS+=(--resolve "$PUBLIC_SELECTED_HOST:443:$address_ip")
		found=$((found + 1))
	done < "$addresses_file"
	if [ "$found" -eq 0 ] || [ -z "$PUBLIC_SELECTED_HOST" ]; then
		printf 'no validated public IP addresses exist for %s\n' "$wanted_kind" >&2
		PUBLIC_SELECTED_HOST=""
		PUBLIC_RESOLVE_ARGS=()
		return 1
	fi
}
