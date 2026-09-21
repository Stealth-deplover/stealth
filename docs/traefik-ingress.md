# Traefik ingress foundation

This release establishes the production Traefik data-plane foundation while
leaving the current Nginx edge in place. It is a migration stage of the final
architecture, not a disposable canary topology.

## Current request path

The checked-in production topology currently serves external traffic through:

```text
Cloudflare Named Tunnel (optional profile)
        |
        v
Nginx proxy:80 on the private `stealth` network
        |-- /v1/* -> stealth-api:8080
        `-- / and Console fallback -> stealth-web:3000
```

The setup Compose project is separate. Its temporary browser setup UI/API
continues to use its own Nginx service and has no Docker socket, Docker CLI,
privileged mode, or broad host access.

## Migration and target paths

During this release the paths coexist:

```text
Cloudflare -> Nginx -> API/Console       (active external path)
                         ^
                         |
             Traefik -> API/Console      (internal production-parity path)
```

The eventual cutover changes the Cloudflare Tunnel origin from the Nginx
service to the internal `traefik:8080` service. It does not change the core
route contract or require a DNS change. Rollback before that cutover is simply
to stop/remove Traefik; Nginx and its application attachments remain usable.
After cutover, rollback is the explicit Cloudflare origin change back to
Nginx.

Traefik has no published host port in this release. Its `web` entrypoint is
available on the dedicated internal `stealth_ingress` network, and its private
`health` entrypoint is used only by the container healthcheck.

## File-provider ownership

The installed layout is:

```text
<STEALTH_INSTALL_ROOT>/traefik/traefik.yaml                 release-managed static config
<STEALTH_INSTALL_ROOT>/traefik/dynamic/core.yaml             release-managed core routes
<STEALTH_INSTALL_ROOT>/traefik/dynamic/.reload.yaml          host-managed reload sentinel
<STEALTH_INSTALL_ROOT>/traefik/dynamic/generated/            future generated route files
```

The host installer owns the corresponding `traefik/` files under the
installation root. Traefik mounts the static and dynamic trees read-only. The
files are derived configuration, never the business source of truth:

- release assets are authoritative for static runtime settings and core
  API/Console routes;
- PostgreSQL Stealth state will be authoritative for future workload and
  verified-domain ownership;
- a future route reconciler will render generated files into `generated/`;
- the writer must render, validate, write a temporary file, fsync/close it,
  and atomically rename it into `generated/`. It must then atomically replace
  the top-level `.reload.yaml` sentinel. Traefik v3 recursively loads the
  directory but watches the root and its immediate files, so the sentinel is
  the durable reload edge for changes inside `generated/`;
- it must never rewrite a live YAML file in place;
- Traefik only consumes the resulting files and never mutates desired state.

The generated directory is deliberately separate from release-managed files so
updates cannot overwrite workload/domain routes. The reload sentinel is
runtime configuration rather than a release asset and is preserved across
asset updates. The current release does not introduce a persistent Traefik
data volume or an application/custom-domain route database.

## Core routing contract

The release-managed core file renders the configured `PUBLIC_APP_URL` hostname
and provides:

- `/v1/*` to `api:8080` with the path preserved;
- `/healthz`, `/readyz`, and `/version` to `api:8080`;
- `/` and Console fallback to `console:3000`;
- long-lived Admin/project SSE requests through the same API service without a
  short response write timeout;
- explicit `Host` matching so unrelated hosts fail closed.

Traefik trusts forwarded headers only from the fixed Cloudflare Tunnel peer
`172.31.0.10/32`. The API trusts the existing Nginx network and the fixed
Traefik peer `172.31.0.254/32`. Neither component enables a global insecure
forwarded-header mode.

Traefik emits JSON application and access logs. Authorization, Cookie,
Set-Cookie, and Proxy-Authorization headers are dropped, and request bodies
are not logged. Telemetry is not a routing dependency and Traefik has no
attachment to PostgreSQL, Redis, ClickHouse, telemetry networks, or the
Docker API proxy network.

## Security and lifecycle

Traefik v3.7.13 is pinned by version and multi-architecture manifest digest in
`TRAEFIK_IMAGE`. The Docker provider is disabled; the file provider is the
only provider. The container runs non-root with all capabilities dropped,
`no-new-privileges`, a read-only root filesystem, and no Docker socket,
privileged mode, host network, or broad host mount. The dashboard and insecure
API are disabled, and port 8080 is not published on the host.

The installer manifest includes the static file, core file, and generated
directory placeholder. Fresh installation downloads them, repair restores
missing/damaged release files, and update migrates them while preserving
generated route files. Uninstall removes known release-managed files and only
removes empty Traefik directories; unknown/generated files are not wildcard
deleted. No Traefik persistent state is added to purge handling.

ACME, port 443, certificate resolvers, custom-domain lifecycle, workload route
reconciliation, and the Cloudflare origin cutover are intentionally deferred
to the later ingress-routing phase. The planned custom-domain lifecycle is
`requested -> ownership verification -> DNS verified -> route generated ->
proxy health verified -> active`; deletion disables the route, waits for
reconciliation, then finalizes metadata.
