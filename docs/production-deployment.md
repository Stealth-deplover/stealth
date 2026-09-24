# Production deployment

This is the supported portable self-hosting baseline for Stealth. It keeps
the existing topology: a reverse proxy serves the standalone Next.js Console
and sends `/v1/*` directly to the Go API; PostgreSQL is the durable control
plane, Redis backs distributed rate limits, and the worker is a separate Go
process.

The control plane models persistent Apps and their normalized runtime intent.
Uploaded App source can be built by a dedicated rootless BuildKit service into
a durable OCI archive. App execution remains a subsequent runtime capability:
building or selecting an image does not create a container or a public route.

```text
TLS terminator / Nginx
  ├── /      → stealth-console:3000
  └── /v1/* → stealth-api:8080
                         ├── PostgreSQL
                         ├── Redis
                         ├── ClickHouse (private telemetry store)
                         └── worker → Docker runner + persistent storage
                                      ├── telemetry-docker-proxy (read-only Docker API)
                                      │   └── telemetry-docker → otel-collector
                                      ├── telemetry-host → otel-collector
                                      └── telemetry-docker-logs → otel-collector
worker → app_build network → dedicated rootless BuildKit → OCI archive in Stealth storage
OTLP / Prometheus → otel-collector → ClickHouse
```

The worker and BuildKit authenticate the private TCP control endpoint with
mutual TLS. The BuildKit certificate is valid for the `buildkit` DNS name; the
daemon verifies client certificates against the installation's BuildKit CA.
Private Docker networking is not treated as authentication.

```text
                  Stealth BuildKit CA
                   /              \
         server identity       worker identity
                │                    │
                ▼                    ▼
            BuildKit  ◀── mTLS ─── worker
                │
                ▼
         untrusted Dockerfile build
                └── no BuildKit client key
```

The installer creates and validates `private/buildkit-mtls`, outside the
legacy setup-state directory, during install,
repair, and upgrade. The CA key stays on the host at mode `0600` and is not
mounted into any container. A networkless, one-shot initializer copies only
the server and dedicated health-client identities into BuildKit's private
credential volume; a separate initializer copies only the worker identity
into the worker's private volume. Runtime volumes are mounted read-only and
private keys are owned by their single service user at mode `0400`. The API
receives no BuildKit key. Tenant build contexts and `RUN` steps receive none
of these control credentials.

The CA key remains in the installation state so the installer can renew the
CA and issue a new leaf set before the CA's final year. Leaf identities renew
when fewer than 30 days remain; renewal uses the same CA, updates role-specific
volumes, and recreates the worker and BuildKit together. Complete valid state
is preserved across routine updates. A partial or corrupt bundle fails closed
with repair guidance instead of replacing one member independently. Backup
and restore of the same installation should include `private/buildkit-mtls`
as internal control-plane credential state. It is not tenant data, BuildKit
cache, or a release asset. A data-preserving uninstall keeps it; destructive
purge removes it. Earlier development installs may have a complete bundle at
`state/buildkit-mtls`; the installer validates and atomically relocates that
bundle without changing its CA or leaf identities. Corrupt bundles or
simultaneous old and new bundles stop installation rather than silently
creating another CA. If the old and new paths are on different filesystems,
installation stops rather than copying private keys nonatomically. Loss of the
private identity requires a new trust set for future builds but does not
invalidate persisted OCI artifacts.

The BuildKit service joins only the private `app_build` network with the
worker. It has no PostgreSQL/Redis/control-plane network, Docker socket, host
port, Stealth artifact storage mount, or platform credentials. The worker
transfers a private source context through BuildKit's client protocol. Cache
state is a separate bounded disposable volume; completed OCI archives in
Stealth storage are authoritative and survive cache loss or a BuildKit
container replacement. RootlessKit's transient state and user runtime
directory use small memory-backed tmpfs mounts owned by UID 1000; explicit
ownership is required because those mounts hide the image's pre-owned paths.

The repository includes [`compose.production.yaml`](../compose.production.yaml)
and [`.env.production.example`](../.env.production.example). The Compose file
uses versioned images; it does not build from a mutable `latest` tag.
Use `stealth install` for fresh installations and the managed repair/update
commands for existing installations so host PKI issuance and role-volume
refresh complete before BuildKit starts. The one-shot credential initializers
do not generate or rotate certificates; they only copy the validated,
installer-owned identities into separate runtime volumes.

`stealth uninstall --keep-data` preserves the host PKI, runtime credential
volumes, and BuildKit cache for a restorable installation. `--purge` removes
the generated identities and these Compose-owned volumes. Losing the BuildKit
PKI requires issuing a new trust set for future builds but does not invalidate
completed OCI artifacts.

Traefik serves platform Site hostnames through the Cloudflare Tunnel wildcard
route. The Console/API hostname defaults to Nginx and can be switched to
Traefik explicitly with the reversible host-side ingress commands below. See
the [Traefik ingress guide](traefik-ingress.md) for the trust boundary and
rollback path.

## Fresh install

Prerequisites:

- Docker Engine with Compose v2.
- A DNS name and TLS termination in front of the proxy. The bundled Nginx
  config is HTTP on the internal/public port and is suitable behind an
  existing TLS terminator; do not expose plain HTTP to the Internet.
- A host Docker socket for the current function/site runner design. The
  worker remains a non-root container, so `DOCKER_GID` must match the group ID
  of `/var/run/docker.sock` on the host.

From the repository checkout:

```bash
cp .env.production.example .env.production
# Replace all CHANGE_ME values, image tags, URLs, and DOCKER_GID.
stat -c '%g' /var/run/docker.sock

docker login ghcr.io
docker compose --env-file .env.production -f compose.production.yaml config
docker compose --env-file .env.production -f compose.production.yaml pull
docker compose --env-file .env.production -f compose.production.yaml up -d postgres redis clickhouse
docker compose --env-file .env.production -f compose.production.yaml up migrate
docker compose --env-file .env.production -f compose.production.yaml run --rm --no-deps -e STEALTH_TRAEFIK_HOST_UID="$(id -u)" traefik-state-init
docker compose --env-file .env.production -f compose.production.yaml run --rm --no-deps cloudflare-setup-state-init
docker compose --env-file .env.production -f compose.production.yaml run --rm --no-deps cloudflare-state-init
docker compose --env-file .env.production -f compose.production.yaml run --rm --no-deps buildkit-worker-credentials-init
docker compose --env-file .env.production -f compose.production.yaml run --rm --no-deps buildkit-server-credentials-init
docker compose --env-file .env.production -f compose.production.yaml up -d api worker buildkit console proxy traefik otel-collector telemetry-host telemetry-docker-logs telemetry-docker-proxy telemetry-docker
./scripts/production-smoke.sh
```

The migration service is a one-shot container. API and worker startup retain
the same idempotent migration check as a safety net, but the release procedure
is explicit: migrate first, then start the application processes. The embedded
migration runner takes a PostgreSQL advisory lock, so concurrent invocations
are serialized and a failed migration exits non-zero without serving traffic.

The Compose `depends_on` health conditions order the initial startup, but they
are not a replacement for monitoring. API `/readyz` checks PostgreSQL, the
required storage implementation, function/site stores, and Redis. API `/healthz`
is liveness only. The worker exposes `/healthz` on its private metrics
listener; a failed worker loop exits so the container supervisor can restart
it. Build metadata is available at API `/version` and in structured startup
logs. `stealth doctor` reports App BuildKit readiness separately; a BuildKit
outage leaves queued App builds available for later retry and does not couple
the Function, Site, webhook, or messaging workers to that service.

## App builds

An AppDeployment captures immutable uploaded source bytes, Dockerfile build
options, and the App's normalized WorkloadSpec snapshot before it enters the
PostgreSQL build queue. The trusted worker verifies the source checksum,
extracts it in a private bounded workspace, and invokes the pinned `buildctl`
client against the dedicated rootless BuildKit daemon. Only an OCI archive is
exported; the worker verifies BuildKit's metadata digest and the OCI layout
before publishing the archive through the durable artifact cleanup-reservation
flow. The metadata image digest and the checksum of the persisted tar archive
are separate identities.

The `buildkit_state` volume holds bounded disposable cache only. Removing it
may make a later build slower, but it does not remove completed AppDeployment
artifacts. App source and successful OCI archives use separate private
`app-sources/` and `app-images/` namespaces in Stealth artifact storage. The
default source archive limit is 128 MiB, expanded source is limited to 1 GiB
and 8,192 files, OCI archives are limited to 2 GiB, and each App has a default
5 GiB source-plus-image artifact quota. Operators can adjust the corresponding
`APPS_*` settings within validated bounds.

BuildKit receives no tenant-selected frontend, insecure entitlement, SSH
forwarding, build secret, build argument, or platform credential. The Dockerfile
frontend is the enabled `dockerfile.v0` frontend from the pinned daemon. The
worker process invokes `buildctl` with an explicit minimal environment and no
shell. Every readiness and build invocation supplies the configured CA,
worker certificate, and worker private key; missing TLS configuration makes
the builder unavailable and there is no plaintext fallback. Build logs are
bounded and sanitized. This boundary executes untrusted
Dockerfile build instructions inside rootless BuildKit; it is not an App
runtime.

On Ubuntu hosts where `apparmor_restrict_unprivileged_userns` is enabled, the
installer installs and loads a release-managed AppArmor profile that grants the
BuildKit container's rootlesskit process the `userns` permission. The profile
keeps the same unconfined AppArmor mode required by the official rootless image
and adds no capability, network, mount, or file rules. It is stored under
`/etc/apparmor.d` so the kernel loads it after host reboot. Other hosts retain
the existing `apparmor=unconfined` setting. Manual Compose deployments on
restricted Ubuntu hosts must load
`buildkit/stealth-buildkit-rootless.apparmor` with `apparmor_parser` and set
`APPS_BUILDKIT_APPARMOR_PROFILE=stealth-buildkit-rootless` before starting
BuildKit.

When a build succeeds, its immutable digest and OCI archive are persisted.
Selecting that deployment records the desired image and advances the App's
desired generation. It does not advance observed generation or mark the App
running. Apps remain `not_deployed`, no App container is created or loaded into
Moby, and App hostnames are still absent from the platform Site routes.

## Configuration

Required production values:

- `STEALTH_API_IMAGE`, `STEALTH_WORKER_IMAGE`, `STEALTH_INGRESS_CONTROL_IMAGE`,
  `STEALTH_MIGRATE_IMAGE`,
  `STEALTH_CONSOLE_IMAGE`, `OTEL_COLLECTOR_IMAGE`,
  `OTEL_HOST_COLLECTOR_IMAGE`, `OTEL_DOCKER_COLLECTOR_IMAGE`,
  `OTEL_DOCKER_LOGS_COLLECTOR_IMAGE`, and
  `STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE`, all on the same immutable release
  tag. The four Collector images are built from the pinned
  `otel/opentelemetry-collector-contrib:0.161.0` base; the Docker-log image is
  the only one with the narrow file capability.
- `TRAEFIK_IMAGE`, pinned to the exact v3.7.13 version and manifest digest
  shown in [`.env.production.example`](../.env.production.example).
- `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `REDIS_PASSWORD`.
- `FUNCTIONS_SECRET_KEY`, generated with `openssl rand -base64 32`.
- `BOOTSTRAP_CLI_KEY`, generated with `openssl rand -base64 32`; this is the
  dedicated local-CLI proof key for first-run Instance Owner onboarding and
  encryption of short-lived GitHub browser-authorization state. It is never
  reused as a Functions secret, and `FUNCTIONS_SECRET_KEY` is never accepted
  as a fallback.
- `GITHUB_APP_CLIENT_ID`, for an existing/manual production App configuration.
  Fresh browser setup creates a private GitHub App through the Manifest flow,
  configures its HTTPS callback URL, and stores the resulting App identifier
  server-side; it does not ask the operator to enable Device Flow.
- `PUBLIC_APP_URL`, normally `https://console.example.com`.
- `DOCKER_GID`, from `stat -c '%g' /var/run/docker.sock`, while the existing
  Docker-backed function runner is enabled. The same numeric group is used by
  the internal telemetry proxy, but the proxy is not published to the host.

Strongly recommended values:

- `METRICS_TOKEN`, generated with `openssl rand -hex 32`; keep metrics on a
  private network and protect the endpoint.
- `TRUSTED_PROXY_CIDRS`, limited to the network(s) of trusted forwarding
  peers.
- `CLICKHOUSE_PASSWORD`, `CLICKHOUSE_MEMORY_LIMIT`, and a reviewed
  `TELEMETRY_RETENTION` value for the private telemetry services.
- `STORAGE_DRIVER=s3` with provider-specific `STORAGE_S3_*` credentials for a
  production object-store service. The bundled local mode is a persistent
  single-host volume, not highly available object storage.

Development-only defaults remain in [`.env.example`](../.env.example). Do not
copy its local database password or `COOKIE_SECURE=false` setting into a
public deployment. Secrets must never be placed in `NEXT_PUBLIC_*` variables.

## Proxy and cookies

The recommended same-origin public URL is:

```text
https://console.example.com/      → Console
https://console.example.com/v1/*  → Go API
```

Terminate TLS before the bundled Nginx (or replace it with an equivalent
Nginx/Caddy configuration), preserve the normalized HTTPS scheme, and set
`COOKIE_SECURE=true`. The current Nginx config forwards `X-Real-IP`,
`X-Forwarded-For`, and `X-Forwarded-Proto` to the API; the preceding ingress
must sanitize those headers.

`TRUSTED_PROXY_CIDRS` is a trust boundary, not a convenience switch. The Go
API uses `RemoteAddr` unless the direct peer is in this list. It then walks a
validated forwarded chain from the nearest hop outwards. For a topology such
as:

```text
user → Cloudflare/LB → Nginx → Go API
```

include the private Nginx network and the Cloudflare/LB address ranges that
appear in the sanitized forwarding chain. Do not use `0.0.0.0/0`; if the list
is empty, forwarded client-IP headers are ignored and public rate limits use
the direct peer.

## Cloudflare Console origin cutover

New and upgraded installations use the existing Cloudflare Named Tunnel with
the Console hostname pointed at `http://proxy:80` (Nginx). The platform Site
wildcard continues to point at `http://traefik:8080` on that same tunnel.
Migration defaults the durable Console origin to `proxy`; installing or
updating a release never changes it remotely.

After public HTTPS, API, Traefik, Cloudflared, and proxy health checks pass,
request the explicit cutover from the installation host:

```bash
stealth ingress status
stealth ingress verify
stealth ingress cutover
stealth ingress verify --site-hostname portfolio.apps.example.com
```

The host invokes a restricted one-shot `ingress-control` Compose service. It
uses configured PostgreSQL and the encrypted Cloudflare connection, takes the
existing reconciler's PostgreSQL advisory lock, changes only the Console ingress
inside the existing tunnel, verifies the provider configuration, and probes
public DNS and HTTPS. The Console root may return its normal `307 /organizations`
redirect; public verification follows at most five same-host HTTPS redirects
and rejects host/port changes, IP literals, loops, and downgrades. Every hop
must retain the required browser security headers and HSTS. The public checks
include the Console root, unauthenticated
`/v1/account`, `/healthz`, `/readyz`, `/version`, an unknown path, the existing
browser security headers, and HSTS with `max-age >= 31536000; includeSubDomains`.
When a Site hostname is supplied it must be exactly one label below the
configured `workload_base_domain`, and its public HTTPS response must succeed.

If any post-cutover public check fails, Stealth stores `proxy` as the desired
origin, reconciles the same tunnel back to Nginx, then verifies recovery. A
successful recovery still reports the cutover as failed. If automatic rollback
cannot be confirmed, the command reports a high-severity error; run
`stealth ingress rollback` from the host. Emergency rollback uses the narrow
Console-origin provider operation and preserves workload wildcard/catch-all
rules; it does not call workload DNS or certificate APIs. The host preflight
requires healthy proxy/Nginx, running Cloudflared, and healthy bundled
PostgreSQL when bundled, but does not require the public API, Console, or
Traefik. Manual rollback does not depend on the public Console or API. Neither command enables or changes Cloudflare HSTS
or any other zone-wide security setting. Nginx remains installed and running
after successful cutover so rollback requires no rebuild or service recreation.

For an external public-network acceptance, follow the
[release checklist](RELEASING.md) and run
[`scripts/public-hosting-acceptance.sh`](../scripts/public-hosting-acceptance.sh)
with explicit Console and platform Site URLs. CI uses fake Cloudflare clients
and local Compose/Traefik checks; it does not claim a real Cloudflare E2E.

## Persistence and external services

The production Compose baseline keeps these named volumes:

- `stealth_postgres_data`: PostgreSQL schema and data. Never remove it during
  a normal container recreation.
- `stealth_storage`: local function/site/storage artifacts and staging data
  when `STORAGE_DRIVER=local`.
- `stealth_function_runner_staging`: the shared staging volume referenced by
  Docker-launched build/execution containers.
- `stealth_clickhouse_data`: ClickHouse logs, metrics, and traces. It is not a
  PostgreSQL volume and should be backed up with a telemetry-specific policy.
- `stealth_otelcol_state`: the Collector's crash-safe sending queue and file-log
  offsets for the main Collector.
- `stealth_otel_docker_logs_state`: Docker file-log offsets for the isolated
  Docker-log Collector.

The `telemetry-docker-proxy` service is the only telemetry service with a
Docker socket mount. It is attached only to the internal telemetry network,
has no host `ports` mapping, drops capabilities, and permits only read-only
Docker API requests needed by `docker_stats`: `/_ping`, `/version`,
`/events`, `/containers/json`, `/containers/{id}/json`, and
`/containers/{id}/stats` (including their API-version prefixes). The `/events`
filter is restricted to the
container lifecycle actions used by the receiver: `destroy`, `die`, `pause`,
`rename`, `stop`, `start`, `unpause`, and `update`. Mutation endpoints such as
create, start, exec, stop, remove, and image operations are rejected. The only
accepted query parameters are `since`, `until`, and the restricted `filters`
for events; `all`, `limit`, `size`, and `filters` for listing; `size` for
inspect; and `stream` or `one-shot` for stats. Inspect responses also remove
environment, command, mounts, and non-Compose labels before they reach the
collector. The worker's Docker socket remains a separate, existing trust
boundary for function execution.

The official OpenTelemetry Collector Contrib image is scratch-based and runs
as UID 10001. The main Collector, host-metrics Collector, and Docker-metrics
Collector use a capability-free Stealth wrapper with a static Go probe for the
live `health_check` endpoint and retain `no-new-privileges:true`.

Docker file logs use a dedicated scratch wrapper with the pinned binary's
narrow `DAC_READ_SEARCH` file capability. Only `telemetry-docker-logs` uses
that image; it mounts only `/var/lib/docker/containers:ro`, so the capability
cannot be combined with a broad host-root mount. That service intentionally
omits `no-new-privileges` so the file capability can become effective for UID
10001. Host metrics use a separate `/:/hostfs:ro` mount without the capability.

Redis is authenticated but intentionally has no volume in this baseline. It
stores distributed rate-limit windows, not the durable job state. Losing Redis
causes a readiness/operational failure while unavailable and resets ephemeral
rate-limit counters after recovery; it does not replace PostgreSQL queue
state. Use an external Redis service if the deployment requires managed
availability.

For serious production installations, managed PostgreSQL and an
S3-compatible object store are recommended. Stealth does not claim HA for the
bundled single PostgreSQL, Redis, or local storage services.

### Telemetry operations

ClickHouse and the Collector are internal-only services. The standard Compose
file does not publish ports `9000`, `4317`, `4318`, or `13133` to the host.
`telemetry-host` and `telemetry-docker-logs` are intentionally separate from
the main Collector: the former owns only the read-only host-root mount, while
the latter owns only the Docker JSON-log mount and its narrow file capability.
`telemetry-docker` remains separate because the adjacent
`telemetry-docker-proxy` is the only telemetry process that reads
`/var/run/docker.sock`. All three isolated collectors forward to the main
Collector over a dedicated internal telemetry-ingest network and expose no
host port. The host and Docker-log collectors join only that network; the
Docker-metrics collector also joins its separate Docker-proxy network. See the
[telemetry architecture guide](telemetry-architecture.md) for the schema pin,
query boundary, retention, and security separation.

## First-run onboarding

Fresh `stealth install` starts a separate setup Compose project after host
Docker and Docker Compose checks. It contains only the setup API, setup
Console, setup proxy, PostgreSQL, and Redis. The CLI requests a single-use
setup session, displays a 15-minute setup code, and starts a temporary,
digest-pinned `cloudflare/cloudflared:2026.9.0` Quick Tunnel to the setup proxy
when possible. The Console setup page is `/setup` on that temporary URL. The
browser wizard reviews the public URL, GitHub App, networking, database, Redis,
storage, and final production tunnel settings before persisting
`install_requested`.

The host CLI is the production installer. It reloads and validates the
finalized state, claims the InstallRunID under the installer lock, invokes the
shared Go install engine, runs host Docker Compose and production health
checks, waits for the one-time handoff, and removes the temporary setup API,
Console, and proxy. The setup image has no `/var/run/docker.sock`, Docker CLI,
or Compose plugin. The production API and Console images do not receive the
socket. The production worker intentionally retains it for the existing
Docker-backed function and site runner and runs non-root with the configured
`DOCKER_GID`.

The operator enters the Stealth setup code first. The API stores only a hash of
the code and encrypts provider credentials, short-lived OAuth state, and the
PKCE verifier with the configured Functions secret. GitHub App Manifest
registration returns credentials to the server callback, which immediately
starts GitHub's browser Web Application Flow for the first owner. GitHub
access tokens are used only for the server-side `/user` lookup and are
discarded, never returned to the browser or persisted.

Cloudflare setup uses a scoped API token, not a Global API Key. The Console
verifies the token, discovers accounts and zones, and sends the selected
account, zone, and Console hostname to the setup API. The API creates and
configures the named tunnel with the Console route to `http://proxy:80`, the
catch-all 404, and its proxied DNS record. The host CLI starts the production
tunnel, verifies tunnel health and the production hostname, and removes the
Quick Tunnel only after those checks pass. Before the worker starts, the
networkless `cloudflare-setup-state-init` copies the optional encrypted
setup snapshot into a dedicated named volume; `cloudflare-state-init` reads
only that volume and decrypts the legacy setup
snapshot and atomically creates a versioned, Cloudflare-only encrypted import
artifact. It contains the existing Cloudflare connection identity and API
token required for migration. The worker mounts only this narrow artifact;
the Cloudflared tunnel token, GitHub credentials, setup database and Redis
URLs, S3 credentials, and bootstrap/session state are excluded from it. The
worker still receives its separate PostgreSQL and Redis runtime configuration.
It imports the artifact
only when the durable connection is absent, then reconciles one wildcard DNS
record and `*.workload_base_domain` tunnel ingress to `http://traefik:8080`
asynchronously from PostgreSQL desired state.
The initializer removes old full-snapshot copies from the worker import
directory and fails closed if other unexpected entries remain.

The minimum custom-token permissions are:

- Account: Cloudflare Tunnel Edit.
- Account: Account Settings Read.
- Zone: Zone Read.
- Zone: DNS Edit.
- Zone: SSL and Certificates Read, required for workload edge TLS readiness
  inspection.

Scope Zone Read and DNS Edit to both the Console and workload zones when they
are different. Scope SSL and Certificates Read only to the workload zone. If
both hostnames use the same zone, one zone scope is sufficient. A wildcard
does not create per-Site records, and the existing named tunnel is reused.
See [Cloudflare workload routing](cloudflare-workload-routing.md) for setup
import, reconnection, and safe cleanup behavior.

Cloudflare OAuth remains experimental and inactive. The setup Console does
not offer it, the inactive endpoint never builds an authorization redirect,
and a random `*.trycloudflare.com` hostname is neither a Stealth-controlled
OAuth callback domain nor a valid redirect for a shared Cloudflare OAuth
client. GitHub's browser callback is configured by the Manifest itself; a
random setup hostname is never used as a redirect for a shared official OAuth
client.
The current image reference uses the multi-architecture manifest digest
`sha256:ff69a2225ad7c6f85ed84fbd5f3087df46202426b2388ec60214098e0adf05e9`;
maintainers should update the version and digest together after verifying the
official Cloudflare image manifest.

Quick Tunnels are for temporary onboarding only. They are not production
ingress, have no production SLA, and are stopped and removed after production
verification. If the tunnel cannot be started, use the local setup URL shown
by the CLI. Canceling the CLI preserves the installation and allows
`stealth setup` to resume while bootstrap remains unsealed. See the [browser
setup guide](web-setup.md) for wizard stages, external infrastructure tests,
idempotent retry, and final session handoff.

The first Instance Owner is an instance-level role and is not automatically a
member of every organization. On upgrade, migration does not expose `/setup`
for an existing database and does not automatically choose an account. It
marks the installation as `legacy_installation`; the local operator can run
`stealth setup --adopt-owner`, review the account list, and type the exact
`ADOPT <account-id>` confirmation. Adoption is audited and does not alter
organization membership.

## Release and upgrade

Release tags use SemVer, for example `v0.1.0`. The release workflow publishes
API, setup, worker, migration, and Console images with both the version tag and a
commit tag (`sha-<short sha>`), then creates a GitHub Release. Pin the version
tag (or a digest) in `.env.production`; do not mix `api:v1.2.0` with
`worker:latest`.

Follow [`docs/upgrade.md`](upgrade.md) for backup, migration, coordinated
restart, verification, and rollback boundaries. API and worker releases are
coordinated with the schema migration; rolling mixed application versions are
not guaranteed compatible.

## Smoke and troubleshooting

The smoke script uses bounded `curl` polling only. It checks API `/healthz`,
API `/readyz`, API `/version`, the Console root, the proxy root, and the
proxy's `/v1/account` routing (expected unauthenticated `401`); it does not
use Playwright. For an isolated Compose check:

```bash
ENV_FILE=.env.production ./scripts/compose-production-smoke.sh
```

Useful commands:

```bash
docker compose --env-file .env.production -f compose.production.yaml ps
docker compose --env-file .env.production -f compose.production.yaml logs api worker migrate
curl --fail http://127.0.0.1:18080/healthz
```

Health is intentionally probed directly: the same-origin proxy sends `/` to
Next.js and `/v1/*` to the API, so it does not expose a public liveness route.
Probe the API's loopback port from the host or run `curl` inside the API
container. Do not expose the direct API port publicly just to make health
checks convenient.
