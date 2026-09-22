# Instance domain foundation

Stealth keeps the Console hostname and workload hosting domain as separate
concepts.

`PUBLIC_APP_URL` remains the operator-configured public Console URL. The API
derives `instance_hostname` from its hostname using canonical DNS semantics.
The URL is not copied into PostgreSQL.

The optional `workload_base_domain` value is stored in the singleton
`instance_domain_settings` table. It is canonicalized to lowercase ASCII DNS
form, including IDNA/punycode conversion, before persistence. The value must
have an operator-owned registrable domain according to the public suffix list,
so a public suffix such as `co.uk` cannot be stored by itself. A configured
base that is the Console hostname, or a parent of it, is rejected because a
future wildcard workload route would overlap the Console route.

The current API is intentionally narrow:

- `GET /v1/admin/domain-settings` is visible to Instance Owner and Instance
  Admin sessions.
- `PATCH /v1/admin/domain-settings` requires the current database
  `instance_owner` role. The request must contain `workload_base_domain`; send
  a JSON `null` to clear it. An omitted field is not interpreted as a clear.

The API only records desired instance configuration. It does not provision DNS,
rewrite Site custom domains, create workload hostnames, mutate Cloudflare
records or Tunnel origins, or generate Traefik files.

Deferred follow-up work includes platform hostname allocation, a PostgreSQL
backed Traefik route reconciler, Cloudflare wildcard DNS and tunnel ingress,
and the Cloudflare-to-Traefik origin cutover.
