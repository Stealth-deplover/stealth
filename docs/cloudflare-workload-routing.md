# Cloudflare workload routing

Cloudflare Tunnel installations can expose platform Site hostnames through the
existing named tunnel. The tunnel identity is instance-wide and reused for all
Sites.

## Request paths

For a Console hostname `cloud.example.com` and workload base domain
`apps.example.com`, the tunnel ingress is ordered as follows:

```text
cloud.example.com       -> http://proxy:80       -> Nginx -> API/Console
*.apps.example.com      -> http://traefik:8080   -> generated Site route
                                                     -> API Site listener :8082
catch-all               -> http_status:404
```

Cloudflare DNS points both `cloud.example.com` and `*.apps.example.com` at
`<tunnel-id>.cfargotunnel.com` with proxying enabled. One wildcard CNAME covers
platform Sites such as `portfolio.apps.example.com`; Stealth does not create
one DNS record per Site. Custom Site domains remain user-managed and
TXT-verified. This capability does not add custom-domain Traefik routing.

The Console hostname remains routed to `http://proxy:80`. Nginx stays in
production with its health checks and remains the rollback anchor. Moving the
Console/API origin to Traefik is deferred until a separate public E2E and
rollback validation phase.

Traefik stays private on the existing ingress network. Cloudflared uses its
reserved fixed ingress IP, which remains the only trusted forwarded-header
peer. The reconciler does not publish Traefik ports or broaden trusted proxy
ranges.

## Desired state and provider state

`instance_domain_settings.workload_base_domain` in PostgreSQL is the desired
domain. Changing or clearing it commits independently of Cloudflare API
availability. The API returns the saved domain; `/v1/admin/cloudflare` reports
provider convergence as `unconfigured`, `pending`, `ready`, or `error`, with
the wildcard hostname, discovered zone, last reconcile time, and a sanitized
error when applicable.

The worker reconciles immediately at startup and then on a configurable
bounded cadence (`CLOUDFLARE_RECONCILE_INTERVAL`, default `1m`, allowed range
`15s` to `5m`). A PostgreSQL session advisory lock ensures only one worker
changes this instance's tunnel and DNS. A second worker skips its pass. Desired
PostgreSQL state remains authoritative, so restart or a later polling pass
recovers from provider failures without requiring the Owner to save again.

When a workload domain changes, reconciliation discovers the target zone,
creates or adopts the new wildcard record, updates and verifies tunnel
ingress, saves the observed provider record identity, then revalidates and
removes the prior Stealth-managed record. Clearing the domain removes the
wildcard tunnel rule and then removes the stored wildcard record after the
same identity checks. Console DNS, the named tunnel, and the connection are
retained.

Zone discovery uses the longest accessible Cloudflare zone name that is a
valid DNS suffix of the workload domain. The Console and workload names may be
in different zones; no shared eTLD+1 is required. If the token cannot read the
containing workload zone, status reports an error and the reconciler leaves
existing resources intact.

An exact existing wildcard CNAME for the Stealth tunnel is adopted and its
proxy and TTL flags are repaired. An A/AAAA or other incompatible record, a
different CNAME target, or a stored record ID whose identity has drifted is a
conflict. Stealth will not overwrite or delete that operator state. Retiring
records are deleted only by stored ID after a fresh check of their hostname
and tunnel target. API operations are at-least-once and idempotent; retries
rediscover provider resources after a crash.

## Connection storage and upgrade

The Cloudflare API token is stored only as ciphertext in the singleton
`cloudflare_connections` row, encrypted with the existing `functionsecret`
cipher. Public projections and API responses expose `configured: true` and
non-secret routing state only. Tokens are not sent to the Console after setup,
written to `config.env`, logged, included in audit metadata, or written to
Traefik files.

During onboarding, the setup service already holds the token in memory while
it provisions the Console tunnel and DNS. The production worker imports the
token and complete tunnel binding from the encrypted `state/setup-state.enc`
snapshot on first start after migrations have created the production table.
The Compose initializer copies only that encrypted setup snapshot into a
dedicated read-only worker mount; the cloudflared tunnel token remains outside
the worker mount. Import runs only when the production connection has no
credential or tunnel identity and is idempotent. It does not remove setup
state or replace a newer production connection.

If the setup snapshot is absent, unreadable, or has no recoverable API token,
the existing tunnel is not recreated or altered. Status remains unconfigured
or error and an Instance Owner can reconnect using the existing account and
tunnel identity. Where no identity could be recovered, the Owner must supply
the existing Cloudflare account ID and tunnel ID. Stealth validates the token
and existing Console route/DNS before saving it; it never creates a new tunnel
as part of reconnection.

Only an Instance Owner can mutate the connection. The Instance Admin role can
read status but cannot replace credentials. Reconnection validates access to
the expected account, existing tunnel, Console zone and DNS, and configured
workload zone before transactionally replacing the encrypted credential and
writing an audit event. A failed candidate leaves the working credential and
provider state untouched.

## Scoped token permissions

Create a custom API token with only the permissions used by Stealth:

- Account: Cloudflare Tunnel Edit.
- Account: Account Settings Read, for account discovery.
- Zone: Zone Read, for Console and workload zone discovery.
- Zone: DNS Edit, for Console and wildcard CNAME operations.

Scope Zone Read and DNS Edit to both the Console zone and workload zone when
they are different. Never use a Global API Key. Cloudflare OAuth is not an
active setup or production connection path.

## Deferred work

This routing path does not move the Console/API origin from Nginx to Traefik,
remove Nginx, provision DNS for arbitrary custom domains, or add a generic DNS
provider layer. BuildKit, Apps runtime, OCI, and gVisor work are also outside
this release.
