# Traefik ingress and Console origin cutover

The production stack supports a reversible Console/API origin switch from
Nginx to Traefik. Existing installations retain Nginx as their desired public
origin until an operator runs the host-side cutover command. Both origins stay
running after cutover so rollback changes only the existing Cloudflare Tunnel
configuration.

## Current, migration, and target paths

The production topology serves Console traffic externally through:

```text
Cloudflare Named Tunnel (optional profile)
        |
        v
Nginx proxy:80 on the private `stealth` network
        |-- /v1/* -> stealth-api:8080
        `-- / and Console fallback -> stealth-web:3000
```

The default and upgrade-safe public paths are:

```text
cloud.example.com -> Named Tunnel -> proxy/Nginx -> API/Console
*.apps.example.com -> same Named Tunnel -> Traefik
                                           |-- API Site listener :8082 (Sites)
                                           `-- eligible App container port (Apps)
```

Traefik also retains its core routes. The workload wildcard path is reconciled
from PostgreSQL desired state and does not change the Console origin. The
Console and wildcard names may use separate Cloudflare DNS zones when the
scoped API token can access both.

An explicit host-side cutover changes only the Console rule in the same named
tunnel:

```text
PostgreSQL desired Site routes -> worker reconciler -> atomic file-provider files
                                                          |
Cloudflare -> Traefik -> API/Console/workloads ------------+
```

`stealth ingress cutover` preflights the local Traefik core route and existing
public HTTPS behavior, persists `traefik` as the desired origin, uses the
origin-only Cloudflare reconciler under the existing PostgreSQL advisory lock,
then verifies the provider read-back and public HTTPS Console/API routes,
browser security headers, and HSTS. The Console root may safely redirect (for
example, `307 /organizations`); verification follows at most five redirects
only when they remain on the configured HTTPS hostname and port. Every hop
must preserve the required security headers and HSTS. If public verification
fails, it persists `proxy`, uses the same origin-only operation to restore
Nginx, and verifies public recovery. `stealth ingress rollback` provides
host-side recovery without depending on the public Console or API.

Emergency Console-origin reconciliation changes only the Console Tunnel rule
and preserves the current workload wildcard and final 404 catch-all. It does
not call workload DNS, certificate-pack, or retiring-record APIs, so workload
DNS conflicts or TLS inspection failures cannot block restoration to Nginx.
Before manual rollback, the host checks only local rollback dependencies:
healthy proxy/Nginx, running Cloudflared, and healthy bundled PostgreSQL when
PostgreSQL is bundled. Public API, Console, and Traefik health are not required.
An upgrade never changes the stored desired origin; migration defaults
existing installations to `proxy`.

Nginx and its health checks remain installed, running, and available as the
immediate rollback origin. Platform Site hostnames keep their wildcard rule on
the same tunnel, targeting `http://traefik:8080`; Console cutover does not
change it. No Site-specific DNS records are created. Custom-domain DNS and
public routing remain user-managed and deferred.

The setup Compose project remains separate. Its temporary browser setup UI/API
continues to use its own Nginx service and has no Docker socket, Docker CLI,
privileged mode, or broad host access.

## File-provider ownership

The installed layout is:

```text
<STEALTH_INSTALL_ROOT>/traefik/traefik.yaml                  release-managed static config
<STEALTH_INSTALL_ROOT>/traefik/dynamic/core.yaml             release-managed core routes
<STEALTH_INSTALL_ROOT>/traefik/dynamic/.reload.yaml           worker-writable reload sentinel
<STEALTH_INSTALL_ROOT>/traefik/dynamic/generated/            worker-writable route files
```

Traefik mounts the static file and dynamic tree read-only. These files are
derived configuration, never the business source of truth:

- release assets are authoritative for static runtime settings and core
  API/Console routes;
- PostgreSQL Stealth state is authoritative for platform Site and App
  eligibility;
- the worker renders independent complete snapshots into
  `generated/platform-sites.yaml` and `generated/platform-apps.yaml`;
- the writer contract is render -> validate -> write a temporary file ->
  fsync/close -> atomic rename -> file-provider reload. A live YAML file must
  never be partially rewritten in place;
- Traefik only consumes the resulting files and never mutates desired state.

The generated directory is separate from release-managed files so update and
repair do not overwrite platform routes. The host installer only validates and
creates this layout; it never needs to chown it to the worker UID. A one-shot
`traefik-state-init` Compose service performs the narrow Docker-side handoff
with `user: 0:0`, `network_mode: none`, and only the dynamic directory mounted
at `/state`. It preserves the invoking host user as owner, assigns the fixed
worker group `10001`, and prepares `dynamic/` and `generated/` as `0775` plus
the reload sentinel as `0664`. It does not recursively change ownership and
never mounts or changes `core.yaml` or `traefik.yaml`. The worker receives the
dynamic directory as a read-write bind mount because atomic replacement of the
top-level reload sentinel requires write access to its parent. `core.yaml` is
overlaid at the same container path as a read-only bind mount, while
`traefik.yaml` and the installation root are not mounted into the worker. The
runtime smoke verifies that the worker can replace generated state and the
reload sentinel but cannot write or replace `core.yaml`. Traefik mounts the
complete dynamic tree read-only. Upgrade and repair run the initializer before
dependent services, validate paths with `Lstat`, reject symlinks or
non-directories, and preserve generated content. The generated snapshot is
derived state, not a route registry or database.

## Platform Site routes

Only eligible Sites appear in `platform-sites.yaml` and reach the private
static Site listener on `:8082`. Site behavior stays on its existing listener
and generated file.

For every enabled active Site with a persisted `platform_label` and configured
`workload_base_domain`, the worker generates an exact `Host()` router. Router
and service identifiers are derived from immutable UUIDs, while the Host rule
uses the canonical hostname. Routes are sorted before YAML rendering, so the
same PostgreSQL snapshot produces byte-stable output. Clearing or changing the
instance workload domain, disabling/deleting a Site, or removing its
eligibility removes the old router from the next complete snapshot.

The generated service points to the API container's private Site listener on
`:8082`, not the control-plane listener on `:8080`. That listener has only the
static Site-serving graph and resolves the Host against current PostgreSQL
state before opening the active immutable artifact. A stale generated router
therefore cannot make a disabled/deleted Site public or expose `/v1/*`,
`/healthz`, `/readyz`, `/version`, or `/metrics` from the control plane.

If `platform-sites.yaml` or the top-level `.reload.yaml` is deleted, restart or
allow the worker's bounded reconcile loop to reconstruct the generated state.
Operators must not edit generated YAML by hand.

## Platform App routes and network boundary

Apps reserve labels in the same global hostname namespace as Sites. An App
hostname is published only when PostgreSQL reports the App enabled with a
ready selected deployment, matching desired and observed generations, matching
applied workload identity, a current managed container identity, and healthy
state tied to that exact generation, deployment, and container. Runtime
`running` means process liveness and does not make a route eligible. The
configured health probe must converge first.

The worker inspects the managed container on the owned App bridge and validates
its private address before the App can be routed. The route target uses the
deterministic Docker container name derived from the trusted App UUID plus the
validated `WorkloadSpec.port`; the inspected address remains a health/runtime
identity check, not a Traefik target. User-defined Docker bridge DNS resolves
the managed container by name, so reuse of an old App IP by another App cannot
silently retarget a stale route to that other App. Tenant input never supplies
a URL, address, router rule, or YAML fragment. Hostnames are canonicalized
before typed Traefik configuration is rendered. HTTP health probes use the
current inspected App address and configured port, accept 2xx responses, do
not follow redirects, and carry no tenant or platform credentials.

Apps have no published host ports and attach only to `stealth_app_runtime`.
The trusted worker and hardened Traefik join that separately owned bridge
dynamically through worker-owned Docker CLI calls. Before connecting either
peer, the worker verifies the network ownership labels and checks the exact
Compose service labels and relevant container security settings. It retries
peer convergence after service recreation or Docker daemon restart. Traefik
remains attached to the private `stealth_ingress` network as well; the worker
does not join ingress. API, Console, BuildKit, and the remaining backend
services are not attached to the App bridge, and neither Traefik nor an App
receives the Docker socket. Uninstall preflight allows only these validated
worker/Traefik peers plus Stealth-owned App containers on the owned bridge.

All Apps share this bridge with each other, the worker, and Traefik. This
network is not per-App isolation or a sandbox boundary. Route reconciliation
uses an authoritative PostgreSQL snapshot and a distributed lock. Site and App
files are rendered and atomically published independently, so an App snapshot
failure preserves its last-known-good App file without blocking Site routing.
An unhealthy App leaves the next App snapshot; before an exited managed
container is restarted, its prior health result and address are cleared even
when Docker keeps the same container ID. The configured initial delay and a
fresh probe must pass before PostgreSQL makes it eligible again. A recreated or
drifted container also has its address and health identity cleared before it
can become eligible. File-provider reload is asynchronous, so an
already-published router may remain in Traefik until the next complete snapshot
reloads; the production smoke verifies withdrawal before the configured initial
delay ends. A stale snapshot targets the App's Docker name and cannot follow a
reused IP into a different App. This design does not claim zero-downtime
deployment or HA.

The production Compose smoke checks the real topology, including an App that
reaches running, pending then healthy, serves through Traefik, loses its route
after the configured failure threshold, recovers, and returns after worker
restart. It stops the App process and verifies pending health, route withdrawal,
same-container restart, and fresh health before the public route returns. It
also checks no host App port is published and that the App hostname never
enters the Site snapshot.

## Core routing parity

The release-managed core file renders the hostname from `PUBLIC_APP_URL` and
matches the current Nginx behavior:

- `Host(PUBLIC_APP_HOST) && PathPrefix(`/v1/`)` routes to `api:8080` with the
  original path preserved;
- `Host(PUBLIC_APP_HOST) && PathPrefix(`/`)` routes to `console:3000`;
- therefore `/healthz`, `/readyz`, and `/version` are not public API routes.
  With the current Console fallback they receive the same Console response as
  Nginx, while API readiness/liveness/build checks remain direct internal
  Compose checks;
- project/Admin SSE requests use explicit higher-priority API routers with the
  security-header middleware only. They bypass request-body buffering so the
  initial headers and subsequent flushes are delivered promptly. The ordinary
  API and Console routes are also streaming at the proxy boundary; request
  bodies are enforced by the API's application middleware and endpoint limits;
- unmatched hosts fail closed with Traefik's 404. The legacy Nginx server has a
  default server block and can accept an unmatched Host; this intentional
  hardening difference is covered by the parity smoke and is not used as the
  future external contract;
- unknown paths on the matched host retain the Console fallback behavior.

All release core routers use the same release-managed security-header
middleware. None uses full-request proxy buffering. The production smoke
compares Nginx and Traefik status/body behavior for `/`, `/v1/account`,
`/healthz`, `/readyz`, `/version`, and an unknown path, plus the browser-facing
security headers on representative Console and API responses and an Admin SSE
connection. It also runs a temporary echo backend through Traefik to verify the
forwarded-header trust boundary at runtime: an untrusted ingress peer cannot
preserve spoofed forwarding metadata, while the configured Cloudflared `/32`
can. For `/`, it observes the first `307 /organizations` response and the
successful final document response on both origins.

## Browser security headers and HSTS

Traefik's `stealth-security-headers` middleware preserves the Nginx policy:

- `X-Content-Type-Options: nosniff`;
- `Referrer-Policy: strict-origin-when-cross-origin`;
- `Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=()`;
- `X-Frame-Options: DENY`; and
- the existing restrictive Content Security Policy.

The parallel Traefik entrypoint is plain internal HTTP. It deliberately does
not emit `Strict-Transport-Security`, because unconditional HSTS would pin
local HTTP clients and Traefik cannot safely infer the external HTTPS scheme
from an untrusted forwarded header. Before cutover Nginx keeps its existing
conditional HSTS behavior. After cutover, the Cloudflare HTTPS edge remains the
HSTS owner. The host command requires externally visible
`max-age >= 31536000; includeSubDomains` and the current browser security
policy on both Console and API responses. It does not change zone-wide
Cloudflare settings. Missing HSTS or weaker headers fail the cutover and
trigger automatic provider rollback to Nginx. Traefik's browser security
headers remain active on its core routers.

## Request-body policy

Nginx currently applies `client_max_body_size 100m` as an outer edge ceiling.
Traefik deliberately does not reproduce that ceiling with a full-request
buffering middleware: the pinned Traefik container has a bounded memory model,
and buffering a valid 100 MiB upload before the API receives it would make
memory/tmpfs consumption depend on proxy concurrency. The parallel Traefik
routes therefore remain unbuffered.

The API remains authoritative and streaming. Its `http.MaxBytesReader`
middleware selects the configured multipart limit for Storage, Functions, and
Sites, while the endpoint-specific artifact/file stores enforce their own
limits during streaming. Existing Storage, Functions, and Sites integration
tests cover rejected oversized uploads without committing partial artifacts.
The production smoke validates routing and SSE behavior rather than pretending
that a proxy-side 100 MiB buffer is equivalent. Any future external edge
limit must be documented and bounded independently of Traefik's container
memory/tmpfs budget.

## Ingress network and reserved peers

The installer persists these installation-specific values in `config.env`:

```text
STEALTH_INGRESS_NETWORK_NAME
STEALTH_INGRESS_NETWORK_SUBNET
STEALTH_INGRESS_IP_RANGE
STEALTH_TRAEFIK_INGRESS_IP
STEALTH_CLOUDFLARED_INGRESS_IP
```

`STEALTH_INGRESS_NETWORK_NAME` is an installation identity choice and is
independent from addressing intent. A custom name does not suppress fresh
installation subnet collision detection or automatic candidate selection. If
the operator supplies an explicit subnet, pool, or fixed peer addressing set,
that addressing is validated as chosen state and an overlap fails rather than
silently selecting a different subnet. The name alone can therefore be
changed for a second installation while the installer still selects the first
free bounded candidate. This does not claim complete multi-install support:
other Compose-wide resource names remain shared unless separately configured.

The checked-in `172.31.0.0/24`, `.64/26`, `.254`, and `.10` values are only
defaults/candidates, not host-wide constants. The default layout is:

```text
subnet:       172.31.0.0/24
dynamic pool: 172.31.0.64/26
Cloudflared:  172.31.0.10       (reserved exact peer)
Traefik:      172.31.0.254      (reserved exact peer)
```

The dynamic pool intentionally excludes both fixed infrastructure addresses.
Future routable workloads must receive an address from the persisted dynamic
pool and attach only to the ingress network; their databases and internal
services must not attach to it.

On fresh installation the host installer queries Docker network IPAM state and
performs real CIDR overlap checks. If the default candidate is occupied, it
selects the first free candidate from the bounded deterministic set
`172.31.0.0/24` through `172.31.31.0/24`, then `10.250.0.0/24` through
`10.250.15.0/24`, and persists the result. If an explicitly configured subnet
overlaps an existing network, installation fails with an actionable error; it
does not silently choose another subnet. A conflicting existing network name
also requires a unique `STEALTH_INGRESS_NETWORK_NAME`.

Upgrade and repair preserve persisted subnet, pool, and peer values. For an
older installation that has no ingress values, migration adopts the existing
named Docker network's IPv4 subnet when available, otherwise it uses the same
bounded collision-aware selection. It never reselects a subnet merely because
the release default changed. Invalid CIDRs, IPv6, outside-subnet peers,
network/broadcast addresses, identical peers, and peers inside the dynamic
pool are rejected before Compose is invoked.

Traefik trusts forwarded headers only from the persisted Cloudflared `/32`.
The API trusts the existing Nginx path plus the persisted Traefik `/32`; it
does not trust the entire ingress subnet. The static Traefik asset is rendered
by the installer from the persisted Cloudflared peer, and its placeholder is
rejected if it remains at activation time.

## Runtime and security boundary

Traefik v3.7.13 is pinned by version and multi-architecture manifest digest in
`TRAEFIK_IMAGE`. The Docker provider is disabled; the file provider is the only
provider. The container runs as `65532:65532` with all capabilities dropped,
`no-new-privileges`, a read-only root filesystem, and a bounded `/tmp`. It has
no Docker socket, privileged mode, host network, broad host mount, or public
host port. It attaches only to the internal `stealth_ingress` network.

Only Traefik, API, Console, and the optional Cloudflared profile attach to the
ingress network. PostgreSQL, Redis, ClickHouse, telemetry networks, Nginx,
worker, and the Docker API proxy remain off it. Traefik does not depend on
ClickHouse or telemetry availability for request routing.

The dashboard and insecure API are disabled. Structured application and access
logs are enabled; `Authorization`, `Cookie`, `Set-Cookie`, and
`Proxy-Authorization` are dropped and request bodies are not logged. No ACME,
certificate resolver, port 443, or certificate volume is introduced here; the
current Cloudflare edge remains responsible for public TLS.

## Installer lifecycle

The managed-asset manifest includes the static file, core file, and generated
directory placeholder. Fresh install downloads and renders them, repair
restores missing/damaged release files using the persisted network values, and
update migrates release assets while preserving the generated directory and
operator-owned values. Uninstall removes known release-managed files and only
removes empty Traefik directories; it does not wildcard-delete generated or
operator files. No persistent Traefik state is added to purge handling.

## Deferred work

This capability intentionally does not implement custom-domain Traefik route
lifecycle, Docker discovery, or custom certificates. The planned custom-domain lifecycle is
`requested -> ownership verification -> DNS verified -> route generated ->
proxy health verified -> active`; deletion disables the route, waits for
reconciliation, then finalizes metadata. A later focused PR can add this
custom-domain lifecycle while retaining the file-provider, network, and
security contracts established here.
