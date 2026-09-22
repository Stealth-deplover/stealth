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
base that is the Console hostname, or a parent of it, is rejected because its
wildcard workload route would overlap the Console route.

The current API is intentionally narrow:

- `GET /v1/admin/domain-settings` is visible to Instance Owner and Instance
  Admin sessions.
- `PATCH /v1/admin/domain-settings` requires the current database
  `instance_owner` role. The request must contain `workload_base_domain`; send
  a JSON `null` to clear it. An omitted field is not interpreted as a clear.

The API records the desired instance configuration. Site creation allocates a
stable globally unique `platform_label`; Site responses expose the derived
canonical `platform_hostname` when the workload base is configured. Renaming a
Site does not change that label. The reserved platform labels are `api`,
`admin`, `console`, `status`, and `www`; a reserved or colliding name receives
a deterministic UUID-suffixed alternative.

The existing worker reconciles enabled active Sites from PostgreSQL into the
single generated file `traefik/dynamic/generated/platform-sites.yaml`. A
complete snapshot is rendered deterministically, validated, published with
fsync and atomic rename, and followed by an atomic reload-sentinel update.
Deleting the generated file and restarting/reconciling the worker reconstructs
it from PostgreSQL. Generated YAML is never authoritative, and stale routes
disappear from the next successful snapshot.

Platform routes target a private Site-serving listener that contains no
Console/API, health, metrics, or version routes. The listener checks current
Site state and active artifacts in PostgreSQL before serving content. For
Cloudflare Tunnel installations, the worker asynchronously reconciles one
wildcard DNS record and one wildcard ingress rule on the existing named tunnel
to private Traefik. The Console origin remains Nginx.

See [Cloudflare workload routing](cloudflare-workload-routing.md) for provider
state and reconciliation semantics. Custom-domain Traefik lifecycle,
Console/API Cloudflare origin cutover, and workload runtime/networking remain
deferred.
