# Production deployment

This is the supported portable self-hosting baseline for Stealth. It keeps
the existing topology: a reverse proxy serves the standalone Next.js Console
and sends `/v1/*` directly to the Go API; PostgreSQL is the durable control
plane, Redis backs distributed rate limits, and the worker is a separate Go
process.

```text
TLS terminator / Nginx
  ├── /      → stealth-console:3000
  └── /v1/* → stealth-api:8080
                         ├── PostgreSQL
                         ├── Redis
                         ├── ClickHouse (private telemetry store)
                         └── worker → Docker runner + persistent storage
                                      └── telemetry-docker-proxy (read-only Docker API)
                                          └── telemetry-docker → OTel collector
OTLP / hostmetrics / Prometheus → otel-collector → ClickHouse
```

The repository includes [`compose.production.yaml`](../compose.production.yaml)
and [`.env.production.example`](../.env.production.example). The Compose file
uses versioned images; it does not build from a mutable `latest` tag.

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
docker compose --env-file .env.production -f compose.production.yaml up -d api worker console proxy otel-collector telemetry-docker-proxy telemetry-docker
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
logs.

## Configuration

Required production values:

- `STEALTH_API_IMAGE`, `STEALTH_WORKER_IMAGE`, `STEALTH_MIGRATE_IMAGE`,
  `STEALTH_CONSOLE_IMAGE`, `OTEL_COLLECTOR_IMAGE`, and
  `STEALTH_TELEMETRY_DOCKER_PROXY_IMAGE`, all on the same immutable release
  tag. `OTEL_DOCKER_COLLECTOR_IMAGE` is pinned separately to the matching
  upstream Collector Contrib release.
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
  offsets.

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
as UID 10001. `otelcol-state-init` owns only the named Collector state volume
and assigns it to UID/GID 10001; the Collector itself remains non-root. The
main Collector uses the Stealth wrapper image, which adds only a static Go
probe for the live `health_check` endpoint because the upstream image has no
shell or HTTP client. The isolated Docker stats Collector remains on the
upstream image and uses exec-form config validation.

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
`telemetry-docker` is intentionally separate from the main Collector because
the adjacent `telemetry-docker-proxy` is the only telemetry process that reads
`/var/run/docker.sock`. The stats Collector has only its `docker_stats`
receiver and an OTLP exporter on the internal network; neither service has a
public listener. See the [telemetry architecture guide](telemetry-architecture.md)
for the schema pin, query boundary, retention, and backup separation.

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
verifies the token, discovers accounts and domains, and sends the selected
account, domain, and dashboard hostname to the setup API. The API creates and
configures the named tunnel and proxied DNS record, and writes the private
cloudflared token file. The host CLI starts the production tunnel, verifies
tunnel health and the production hostname, and removes the Quick Tunnel only
after those checks pass. The minimum custom-token permissions are Account:
Cloudflare Tunnel Edit, Account Settings Read, Zone: Zone Read, and Zone: DNS
Edit, scoped to the resources used by the installation.

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
