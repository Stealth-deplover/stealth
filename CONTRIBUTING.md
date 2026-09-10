# Contributing to Stealth

Thanks for helping improve Stealth. Keep changes focused, technically grounded, and compatible with the self-hosted Docker Compose model.

## Repository map

- `cmd/` contains Go entrypoints for the API, worker, migrations, and CLI.
- `internal/` contains backend packages and adjacent `*_test.go` files.
- `openapi/openapi.yaml` is the API contract consumed by `console/`.
- `console/src/` contains the Next.js App Router, features, components, and tests.
- `docs/` and `scripts/` contain operations guides and smoke/bootstrap tooling.

## Local development

For the backend, use Go 1.26 with PostgreSQL and Redis available:

```bash
cp .env.example .env
go run ./cmd/api
go run ./cmd/worker
```

Run `go fmt ./...`, `go vet ./...`, and `go test ./... -count=1`. Integration tests in `internal/httpapi` use `TEST_DATABASE_URL` and, where needed, `TEST_REDIS_URL`; they are skipped when those services are not configured.

For the console:

```bash
cd console
npm ci
npm run api:generate
npm run typecheck
npm run lint
npm run test
npm run test:e2e
```

Use `npm run format:check` for formatting. When `openapi/openapi.yaml` changes, regenerate the client and include the resulting `console/src/api/generated/` diff. Generated files are not edited manually. Read [`console/AGENTS.md`](console/AGENTS.md) before frontend work.

## Pull requests

Describe the behavior change and scope, list validation commands, and call out migrations, API contract changes, environment variables, or security implications. Include screenshots for Console UI changes and update relevant docs. Do not include credentials, tokens, or real customer data.

## Commits

Use the Conventional Commit style already present in history, for example `feat(cli): add interactive installer`, `fix(realtime): bound prune batches`, or `test(console): cover capability fixtures`. Prefer small, reviewable commits.
