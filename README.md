# Stealth

Stealth is a developer cloud control plane. The repository is a monorepo:
the Go API and workers remain the application layer, while `console/` is a
self-hostable Next.js presentation layer for that API.

## Architecture

```text
Browser
  │
  ▼
console/ (Next.js, React, TypeScript)
  │  HTTP + generated OpenAPI client
  ▼
Go API (/v1/*)
  ├── PostgreSQL
  ├── Redis
  ├── workers
  └── local/S3-compatible object storage
```

The API is the platform and the console is an interface to it. The console
does not add Next.js API routes, proxy handlers, Server Actions, or a second
business-logic backend. Session authentication is owned by the Go API through
an HttpOnly cookie.

## Repository layout

```text
stealth/
├── cmd/          Go API, worker, migration, and CLI entrypoints
├── internal/     backend implementation, repositories, workers, auth
├── openapi/      versioned REST API contract
├── console/      Next.js developer console
├── migrations/   embedded database migrations
└── ...
```

### Backend

- Go 1.26, Chi, pgx/v5, sqlc, and Redis
- PostgreSQL-backed organizations, projects, resources, sessions, and audit data
- Dedicated workers for Functions, Sites, Agents, webhooks, and messaging
- `/metrics`, health/readiness endpoints, and optional OpenTelemetry tracing

### Console

`console/` uses Next.js App Router, strict TypeScript, Tailwind, Radix-style
primitives, TanStack Query/Table, React Hook Form, Zod, React Flow,
`openapi-fetch`, Vitest, and Playwright. It talks directly to the Go API:

```text
openapi/openapi.yaml
        ↓ npm run api:generate
console/src/api/generated/
        ↓ openapi-fetch
TanStack Query hooks
        ↓
Stealth Console UI
```

Generated files are never edited manually. Run the generator after changing
the contract and commit the resulting files.

## Development

### Backend

```bash
cp .env.example .env
go run ./cmd/api       # API: http://localhost:8080
go run ./cmd/worker    # background workers
```

PostgreSQL and Redis must be available at the URLs in `.env`. Migrations are
applied by the API on startup.

### Console

```bash
cd console
cp .env.example .env.local
npm ci
npm run api:generate
npm run dev             # Console: http://localhost:3000
```

For a separate API origin, set `NEXT_PUBLIC_API_BASE_URL` in
`console/.env.local` and add the console origin to the API's
`CONSOLE_CORS_ORIGINS`. For the preferred same-origin deployment, leave the
value empty and route `/v1/*` to the Go API.

The API's `PUBLIC_APP_URL` should point to the console origin so verification
and recovery links return to the console.

Backend execution-path mapping, recovery semantics, rate-limit scopes, health
checks, metrics access, proxy trust, and remaining operational gaps are
documented in [`docs/backend-production-readiness.md`](docs/backend-production-readiness.md).
The repeatable self-hosting path is documented in
[`docs/production-deployment.md`](docs/production-deployment.md), with
upgrade/rollback guidance in [`docs/upgrade.md`](docs/upgrade.md) and the
backup runbook in [`docs/backup-restore.md`](docs/backup-restore.md).
The Go CLI and interactive installer are documented in
[`docs/cli.md`](docs/cli.md).

## Quick install

On a Linux amd64 or arm64 host with Docker and Docker Compose already
installed, start the interactive installer with:

```bash
curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/init/backend-import/scripts/bootstrap.sh | sh
```

The bootstrap downloads a versioned `stealth` CLI from GitHub Releases,
verifies its SHA-256 checksum, and lets the CLI run the existing production
Compose deployment. Inspect the script first when preferred:

```bash
curl -fsSL https://raw.githubusercontent.com/Stealth-deplover/stealth/init/backend-import/scripts/bootstrap.sh -o bootstrap.sh
less bootstrap.sh
sh bootstrap.sh
```

## API contract

`openapi/openapi.yaml` is the source of truth for every console request. The
TypeScript client is generated into `console/src/api/generated/`:

```bash
cd console
npm run api:generate
git diff --exit-code -- src/api/generated
```

The generated-client check is also part of GitHub Actions.

## Environment variables

Backend variables are documented in `.env.example` and validated by
`internal/config`. The most important values are:

- `DATABASE_URL` and `REDIS_URL`
- `DATABASE_MAX_CONNS`, `DATABASE_MIN_CONNS`, `DATABASE_MAX_CONN_LIFETIME`,
  and `DATABASE_MAX_CONN_IDLE_TIME` for bounded PostgreSQL pools
- `FUNCTIONS_SECRET_KEY`
- `PROJECT_OPERATION_RATE_LIMIT` and `PROJECT_OPERATION_RATE_WINDOW` for
  per-project/per-actor safety limits on expensive operations
- `HTTP_ADDR`
- `METRICS_TOKEN` to explicitly enable protected Prometheus output
- `PUBLIC_APP_URL`
- `CONSOLE_CORS_ORIGINS` when the console is served from another origin
- `STORAGE_*`, `SMTP_*`, and `AUTH_*` for optional storage and email flows

Console variables are documented in [`console/.env.example`](console/.env.example):

- `NEXT_PUBLIC_API_BASE_URL` — optional API origin; empty means same-origin `/v1`
- `NEXT_PUBLIC_APP_NAME` — optional console label

Never put session secrets, API key secrets, or other private backend values in
`NEXT_PUBLIC_*` variables.

## Docker deployment

Build the backend images from the repository root:

```bash
docker build --target api -t stealth-api .
docker build --target worker -t stealth-worker .
docker build --target migrate -t stealth-migrate .
```

Build the self-hosted console image:

```bash
docker build -f console/Dockerfile -t stealth-console .
docker run --rm -p 3000:3000 stealth-console
```

The console uses Next.js `output: "standalone"`, a multi-stage image, and a
non-root runtime user. It has no dependency on Vercel Functions, KV, Blob,
Postgres, Edge, or other Vercel-only infrastructure.

Put a reverse proxy in front of the two services:

```text
/      → stealth-console:3000
/v1/*  → stealth-api:8080
```

An Nginx example is available at [`console/deploy/nginx.conf`](console/deploy/nginx.conf).
It includes baseline browser security headers. The example normalizes
`X-Forwarded-Proto` from a trusted TLS terminator before forwarding it to both
the console and Go API; the ingress must sanitize that header before traffic
reaches Nginx. HSTS uses the same normalized protocol, so it is emitted only
for externally HTTPS requests and never pins a plain HTTP development host.
The CSP assumes same-origin `/v1/*` routing. If the API is deployed on a
separate origin, add that exact origin to `connect-src` in the proxy config.

For rate-limit client-IP resolution, configure `TRUSTED_PROXY_CIDRS` with the
IP/CIDR of the direct Nginx peers as seen by the Go API (for example, the
private network used by the reverse-proxy containers). It is empty by default,
which trusts no forwarded client-IP headers. The API accepts the current
Nginx `X-Forwarded-For` chain first, then standards-based `Forwarded`, then
`X-Real-IP`, and falls back to the direct peer on malformed input. Do not use
`0.0.0.0/0` or expose the API listener directly while trusting forwarded
headers.

For a versioned production deployment, copy
[`.env.production.example`](.env.production.example) to `.env.production` and
follow [`docs/production-deployment.md`](docs/production-deployment.md).

## Verification

```bash
go vet ./...
go test ./... -count=1
go build ./cmd/stealth
GOOS=linux GOARCH=amd64 go build ./cmd/stealth
GOOS=linux GOARCH=arm64 go build ./cmd/stealth

cd console
npm run api:generate
npm run typecheck
npm run lint
npm run test
npm run build
npm run test:e2e
```

GitHub Actions runs backend vet/tests/integration/Docker checks and the full
console generation, typecheck, lint, unit, production build, Playwright, and
Docker checks on every push and pull request, independent of the default branch
name.

## Backend boundaries

The console intentionally does not invent capabilities that are absent from
the API contract. Current boundaries and recommendations are tracked in
[`console/docs/backend-gaps.md`](console/docs/backend-gaps.md).
