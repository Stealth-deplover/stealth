# Stealth Console

Presentation layer for the Stealth Go control-plane API. The browser talks directly to the Go API through `/v1/*`; this application does not add a Next.js API, proxy, Server Action, or second backend.

## Development

```bash
npm ci
cp .env.example .env.local
npm run api:generate
npm run dev
```

When running the API on `http://localhost:8080`, set `NEXT_PUBLIC_API_BASE_URL=http://localhost:8080` in `.env.local` and include the console origin in the Go API's `CONSOLE_CORS_ORIGINS`. The checked-in [`./.env.example`](.env.example) documents the browser-safe variables.

For same-origin production routing, leave `NEXT_PUBLIC_API_BASE_URL` empty. The browser then requests `/v1/*`, and Nginx/Caddy forwards that path to the Go API. The Go API owns the HttpOnly session cookie; the console never writes session tokens to localStorage.

## Contract and architecture

`npm run api:generate` reads the backend contract from `../openapi/openapi.yaml` and regenerates `src/api/generated/schema.ts`. Feature hooks call the typed `openapi-fetch` client directly. There are no Next.js API routes, proxy handlers, Server Actions, or duplicated backend business rules.

The layout follows the backend hierarchy: account → organizations → projects → project resources. Resource pages only use operations present in the generated contract. Known backend boundaries are recorded in [`docs/backend-gaps.md`](docs/backend-gaps.md).

## Verification

```bash
npm run api:generate
npm run typecheck
npm run lint
npm run test
npm run test:e2e
```

The Playwright smoke suite and the fixture-backed critical navigation flow run without a Go API. For live authenticated coverage, point the same suite at a test API/account rather than committing credentials or tokens.

CI runs the same API generation, stale-generated-file check, typecheck, lint, unit test, production build, Playwright setup, E2E suite, and root-context Docker build used by the repository workflow.

## Production

The app uses Next.js standalone output and is designed to sit behind Nginx or Caddy:

```text
/      -> stealth-web
/v1/*  -> stealth-api
```

Build the self-hosted image from the repository root so the Docker build can read the OpenAPI contract:

```bash
docker build -f console/Dockerfile -t stealth-console .
docker run --rm -p 3000:3000 stealth-console
```

For a separate API origin, pass `--build-arg NEXT_PUBLIC_API_BASE_URL=https://api.example.com`; for the preferred same-origin setup, omit it. An Nginx upstream example lives at [`deploy/nginx.conf`](deploy/nginx.conf).

The OpenAPI input defaults to `../openapi/openapi.yaml`. For a separate checkout, set `OPENAPI_INPUT` to the contract location before running `npm run api:generate`.
