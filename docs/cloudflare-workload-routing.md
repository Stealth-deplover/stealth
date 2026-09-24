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

The upgrade-safe default Console route remains `http://proxy:80`. Nginx stays
installed, healthy, and available as the rollback origin after cutover. An
operator can switch the Console rule in the same named tunnel with
`stealth ingress cutover`; the workload wildcard rule and DNS record remain
unchanged. The migration sets existing installations' desired origin to
`proxy`, and installation/update never cuts over automatically.

The cutover command checks the local Traefik Console and API routes, captures
the current public HTTPS behavior, persists `traefik` as desired state, then
uses the origin-only Cloudflare reconciler and PostgreSQL advisory lock to
update and verify just the Console rule in the existing tunnel. Console public
verification follows at most five same-host HTTPS redirects, including the
Console root's expected `307 /organizations`, while checking browser security
headers and HSTS on each hop. If a post-cutover check fails, it changes the
desired origin back to `proxy`, reconciles the same tunnel through the same
narrow path, and verifies public recovery. This emergency operation preserves
the workload wildcard and catch-all without calling wildcard DNS, certificate,
or retiring-record APIs. Manual `stealth ingress rollback` requires healthy
local Nginx, running Cloudflared, and healthy bundled PostgreSQL when used; it
does not require public API, Console, or Traefik health. The operator command
never changes zone-wide Cloudflare security settings.

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
error when applicable. It also exposes `edge_tls_status` as `not_applicable`,
`pending`, `ready`, `action_required`, or `error`, plus a sanitized
`edge_tls_error` when certificate readiness needs attention. Overall routing is
`ready` only when DNS, tunnel ingress, and Cloudflare edge TLS are ready.

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

## Cloudflare edge TLS readiness

The reconciler reads the workload zone's production certificate packs and only
reports edge TLS `ready` when an active, unexpired certificate explicitly
covers the required wildcard, such as `*.apps.example.com`. A parent wildcard
such as `*.example.com` does not cover the deeper workload wildcard. Coverage
uses the DNS rule that a wildcard matches exactly one label. A pending
certificate is reported as `pending`; missing coverage is `action_required`,
and certificate API failures are `error`. These states do not remove working
wildcard DNS or tunnel ingress.

Cloudflare Universal SSL for a full zone generally covers the apex and its
first-level subdomains. A dedicated workload zone such as `apps.example.com`
may provide a suitable wildcard shape, but Stealth still checks that the
provider reports active certificate coverage before declaring readiness.
Total TLS is inspected only for context; its enabled setting is not treated as
coverage for Cloudflare Tunnel hostnames. Stealth does not enable Total TLS,
order certificates, or purchase a Cloudflare feature. If deeper wildcard
coverage is missing, an operator must arrange a Cloudflare edge certificate or
zone configuration that actually covers the workload wildcard, then allow the
worker to reconcile again.

## Connection storage and upgrade

The Cloudflare API token is stored only as ciphertext in the singleton
`cloudflare_connections` row, encrypted with the existing `functionsecret`
cipher. Public projections and API responses expose `configured: true` and
non-secret routing state only. Tokens are not sent to the Console after setup,
written to `config.env`, logged, included in audit metadata, or written to
Traefik files.

During onboarding, the setup service already holds the token in memory while
it provisions the Console tunnel and DNS. Before worker startup, a separate
networkless, read-only source initializer copies only the optional encrypted
`state/setup-state.enc` into the dedicated `cloudflare_setup_state_input`
named volume. It cannot decrypt the snapshot and cannot see BuildKit PKI,
which lives under `private/buildkit-mtls`. The isolated, network-free
`cloudflare-state-init` helper reads only that handoff volume, decrypts the
legacy snapshot, and atomically writes a versioned,
Cloudflare-only encrypted import artifact. That artifact contains only the
existing account, Console zone/hostname, tunnel identity, Console DNS record
ID, and Cloudflare API token required for migration. It excludes the
cloudflared tunnel token, GitHub credentials, database and Redis URLs, S3
credentials, and other setup/bootstrap state. The worker mounts only this
narrow artifact directory read-only; it never receives the complete setup
snapshot. The helper removes the exact full-snapshot copy names used by the
previous initializer and blocks worker startup if unexpected entries remain
in that directory. Import runs only when the production connection has no
credential or tunnel identity and is idempotent. The encrypted artifact is
retained for recovery, does not remove original setup state, and cannot
replace a newer production connection.

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
- Zone: SSL and Certificates Read, to inspect active and pending production
  certificate packs for workload edge TLS readiness (workload zone only).

Scope Zone Read and DNS Edit to both the Console and workload zones when they
are different. Scope SSL and Certificates Read only to the workload zone.
Never use a Global API Key. Cloudflare OAuth is not an active setup or
production connection path.

## Deferred work

This routing path does not remove Nginx or provision DNS and Traefik routes
for arbitrary custom domains. Custom domains remain user-managed and
TXT-verified. BuildKit, Apps runtime, OCI, and gVisor work are also outside
this release.
