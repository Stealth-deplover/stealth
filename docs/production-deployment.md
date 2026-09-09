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
                         └── worker → Docker runner + persistent storage
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
docker compose --env-file .env.production -f compose.production.yaml up -d postgres redis
docker compose --env-file .env.production -f compose.production.yaml up migrate
docker compose --env-file .env.production -f compose.production.yaml up -d api worker console proxy
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

- `STEALTH_API_IMAGE`, `STEALTH_WORKER_IMAGE`, `STEALTH_MIGRATE_IMAGE`, and
  `STEALTH_CONSOLE_IMAGE`, all on the same immutable release tag.
- `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `REDIS_PASSWORD`.
- `FUNCTIONS_SECRET_KEY`, generated with `openssl rand -base64 32`.
- `PUBLIC_APP_URL`, normally `https://console.example.com`.
- `DOCKER_GID`, from `stat -c '%g' /var/run/docker.sock`, while the existing
  Docker-backed function runner is enabled.

Strongly recommended values:

- `METRICS_TOKEN`, generated with `openssl rand -hex 32`; keep metrics on a
  private network and protect the endpoint.
- `TRUSTED_PROXY_CIDRS`, limited to the network(s) of trusted forwarding
  peers.
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

Redis is authenticated but intentionally has no volume in this baseline. It
stores distributed rate-limit windows, not the durable job state. Losing Redis
causes a readiness/operational failure while unavailable and resets ephemeral
rate-limit counters after recovery; it does not replace PostgreSQL queue
state. Use an external Redis service if the deployment requires managed
availability.

For serious production installations, managed PostgreSQL and an
S3-compatible object store are recommended. Stealth does not claim HA for the
bundled single PostgreSQL, Redis, or local storage services.

## Release and upgrade

Release tags use SemVer, for example `v0.1.0`. The release workflow publishes
API, worker, migration, and Console images with both the version tag and a
commit tag (`sha-<short sha>`), then creates a GitHub Release. Pin the version
tag (or a digest) in `.env.production`; do not mix `api:v1.2.0` with
`worker:latest`.

Follow [`docs/upgrade.md`](upgrade.md) for backup, migration, coordinated
restart, verification, and rollback boundaries. API and worker releases are
coordinated with the schema migration; rolling mixed application versions are
not guaranteed compatible.

## Smoke and troubleshooting

The smoke script uses bounded `curl` polling only. It checks API `/healthz`,
API `/readyz`, API `/version`, and the Console root; it does not use
Playwright. For an isolated Compose check:

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
