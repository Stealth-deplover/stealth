# Repository Guidelines

Stealth combines Go control-plane/workers with a Next.js console. Work from the repository root unless a command says `cd console`.

## Project Structure & Module Organization

- `cmd/` contains the API, worker, migration, and CLI entrypoints.
- `internal/` contains backend packages, handlers, repositories, workers, auth, and config; tests live beside the code they cover.
- `internal/migrate/migrations/` contains embedded, numbered SQL migrations.
- `openapi/openapi.yaml` is the REST contract source of truth.
- `console/src/` contains the App Router, features, components, API hooks, and frontend tests. `console/src/api/generated/` is generated output.
- `docs/`, `scripts/`, `compose.production.yaml`, and `.github/workflows/` cover operations, deployment, smoke scripts, and CI. Read [`console/AGENTS.md`](console/AGENTS.md) before changing the console.

## Build, Test, and Development Commands

Use Go 1.26, Node 24, and running PostgreSQL/Redis services.

```bash
cp .env.example .env
go run ./cmd/api       # API on :8080
go run ./cmd/worker
go vet ./...
go test ./... -count=1
go build ./cmd/stealth
```

For the console, run `cd console && npm ci`, then the needed `npm run` scripts: `api:generate`, `dev`, `typecheck`, `lint`, `test`, `build`, or `test:e2e`. Regenerate and commit the OpenAPI client after contract edits. Validate production Compose with `docker compose --env-file .env.production.example -f compose.production.yaml config --quiet`.

## Coding Style & Naming Conventions

Run `gofmt`; CI rejects unformatted Go files. Use idiomatic Go names and lowercase packages. Console code uses strict TypeScript, ESLint, and Prettier; use PascalCase for components/types, `use...` for hooks, and lower camelCase for helpers. Never hand-edit generated API files.

## Testing Guidelines

Go tests use `*_test.go`; `internal/httpapi` integration tests include `Integration` in their names and require `TEST_DATABASE_URL` (and `TEST_REDIS_URL` where relevant). Console unit tests match `src/**/*.test.{ts,tsx}` and use Vitest; E2E tests live in `console/tests/e2e/` and use Playwright. No coverage threshold is configured, but all CI checks must pass.

## Commit & Pull Request Guidelines

Use Conventional Commits, matching history such as `fix(realtime): bound prune batches` or `feat(cli): add interactive installer`. PRs should explain scope and behavior, list validation commands, call out migrations/API/environment changes, link related issues when applicable, and include screenshots for UI changes. Keep generated API changes and tests in the same PR as contract changes.

## Security & Configuration

Copy example env files for local work and never commit secrets. Keep private values out of `NEXT_PUBLIC_*`. Treat sessions and API keys as secrets; configure CORS and trusted proxy CIDRs narrowly. Read `docs/production-deployment.md` before production changes.

## Project Agent Skills

The project-local skills under `.agents/skills/` and `.claude/skills/` are committed for Codex and Claude Code. They are advisory workflows, not permission to redesign Stealth.

- `vercel-react-best-practices`: Use when editing `console/`, React components, Next.js routes or layouts, client/server boundaries, rendering, or data fetching. Do not use it for unrelated Go backend work.
- `web-design-guidelines`: Use when creating or modifying Console UI, reviewing accessibility, visual hierarchy, forms, responsive layouts, navigation, or design-system usage. Preserve the established Stealth design language and do not use it to justify unnecessary visual redesigns.
- `diagnosing-bugs`: Use for non-trivial defects such as failing CI, installer or worker failures, regressions, concurrency or performance problems, intermittent failures, and difficult Console bugs. Do not invoke the full workflow for trivial typos or obvious one-line fixes. For substantial bugs, establish a reproducible red/green feedback loop first and redact secrets from logs and captured output.
- `improve-codebase-architecture`: Use only for explicit architecture audits, refactor planning, coupling or testability reviews, module-boundary work, or making the codebase easier for humans and agents to navigate. Propose broad architecture changes before implementing them; do not refactor architecture during ordinary feature work.

Repository instructions and established architectural decisions take precedence over generic skill recommendations. If a skill conflicts with `AGENTS.md`, nested instructions, existing architecture, API compatibility, security boundaries, or ADR/project documentation, follow the repository-specific rule and report the conflict. Keep every change scoped to the task.

Use this hierarchy: the specific task or user request, repository-specific instructions, applicable nested instructions, relevant skill guidance, then generic agent defaults.

### Skill Maintenance and Security

Inspect installed project skills with:

```bash
npx skills@latest list
```

Update them with:

```bash
npx skills@latest update
```

Review skill updates like dependency updates. Changes to `.agents/skills/`, `.claude/skills/`, or `skills-lock.json` require inspection of `SKILL.md`, `scripts/`, hooks, shell commands, and external network calls before they are committed. Do not commit credentials, tokens, caches, session history, agent logs, or machine-local configuration.
