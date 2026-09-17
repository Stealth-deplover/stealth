# Audit remediation — 2026-09-17

This document preserves the historical context in
[`docs/audit-report-2026-09-17.md`](https://github.com/Stealth-deplover/stealth/blob/010250f5a3c6f1407aeabb6335b189d9b3b55822/docs/audit-report-2026-09-17.md)
and records the re-verification and remediation performed against the latest
`main`.

## Re-verification

- Main before remediation: `157b1ba0903f2f1dc0e7be96e6a7b3d0a387360e`
- Original audit revision: `010250f5a3c6f1407aeabb6335b189d9b3b55822`
- Remediation branch: `fix/audit-2026-09-remediation`
- Pre-fix classification: all six reported findings were **CONFIRMED** on the
  current main revision.

The verification used current function behavior rather than the historical
line numbers. The cursor reproduction covered quoted, escaped, Unicode, and
empty text boundaries through both sort directions. The storage trace found
the same metadata-then-best-effort-physical-delete ordering in storage files
and buckets, function deployments/functions, site deployments/sites,
database backups, and project namespace deletion. The realtime trace followed
the durable event row through `ShouldFanout`, the SSE filter, the Console
subscription list, semantic event mapping, and TanStack Query keys. The Agent,
release workflow, and setup checklist reproductions were likewise run against
their current implementations.

## Remediation status

### 1. Cursor pagination — CONFIRMED + FIXED

Root cause: `internal/httpapi.canonicalCursorValue` JSON-decoded text and then
used `strings.Trim(text, "\"")`, changing legitimate boundary values.

Fix: text/varchar/datetime cursors now use the already decoded string directly;
numeric and boolean values use typed scalar formatting; JSON values retain
`UseNumber` decoding. `RowCursor.value` is no longer omitted for an empty
string. The comparison value is parsed once using the column definition.

Files: `internal/httpapi/server_databases.go`,
`internal/repository/databases.go`.

Regression coverage: `TestCanonicalCursorValuePreservesTextBoundaries`,
`TestCanonicalCursorValueNormalizesSupportedScalarTypes`,
`TestRowCursorRoundTripPreservesTextValues`, and the real database pagination
coverage in `TestProjectDatabasesCoreIntegration`.

### 2. Artifact deletion — CONFIRMED + FIXED

Root cause: metadata transactions committed before handlers attempted physical
deletion, with no durable retry record. Database deletion also cascaded backup
metadata without retaining a physical cleanup request.

Fix: migration `000041_artifact_cleanup.up.sql` adds a durable, idempotent,
leased cleanup queue. Delete transactions enqueue UUID-derived relative paths
or project namespaces in the same transaction as metadata deletion. The
trusted worker in `internal/artifactcleanup` retries physical failures with
bounded backoff, recovers stale leases, treats missing objects as success,
and validates paths before dispatch. The HTTP delete handlers no longer own
physical cleanup.

Files: `internal/migrate/migrations/000041_artifact_cleanup.up.sql`,
`internal/repository/artifact_cleanup.go`, `internal/artifactcleanup/`,
the storage/function/site/database/project repositories and delete handlers,
and `cmd/worker/main.go`.

Regression coverage: `internal/artifactcleanup/worker_test.go`,
`TestArtifactCleanupQueueRetryAndLeaseRecoveryIntegration`, the function and
project deletion integration tests, and path-escape/missing-artifact tests.

### 3. Realtime consistency — CONFIRMED + FIXED

Root cause: the backend fanout allow-list and the Console event subscription
and cache mapping did not agree. Row/file events and several existing schema,
credential, domain, and Agent-related events could be generated but dropped or
not mapped to the affected query keys.

Fix: fanout now includes the generated row, storage-file, schema, backup,
function-variable, site-domain, API-key, project-user, and Agent-run domains.
The repositories add only safe scope metadata needed by the Console. The
Console subscribes to the same event set and maps database rows, storage files
and buckets, schema, deployments, webhooks, credentials, and Agent runs to
scoped query invalidation keys. Unknown or unrelated events produce no keys.

Files: `internal/realtime/event.go`, affected repository event metadata
including `internal/repository/database_rows_realtime_test.go`,
`console/src/realtime/invalidation.ts`,
`console/src/api/cache-coherence.ts`, and storage/Agent mutation adapters.

Regression coverage: `internal/realtime/event_test.go`,
`console/src/realtime/invalidation.test.ts`, and
`console/src/api/cache-coherence.test.ts`.

### 4. Queued Agent status — CONFIRMED + FIXED

Root cause: `CreateAgentRun` inserted a queued run without invoking the
existing `refreshAgentStatusTx`, and Console mutations invalidated only run
queries.

Fix: run creation refreshes the parent Agent status in the same transaction.
The existing canonical derivation remains authoritative: running takes
precedence, queued maps to the supported `active` status, and no active work
maps to `idle`. Console create/cancel mutations and realtime Agent-run events
invalidate the parent list and detail queries.

Files: `internal/repository/agent_runs.go`,
`console/src/api/mutations/agents.ts`, and
`console/src/features/agents/agent-runs-view.tsx`.

Regression coverage: the queued Agent assertion in
`internal/httpapi/agents_integration_test.go` and parent-query coverage in
`console/src/api/cache-coherence.test.ts`.

### 5. Release bootstrap revision — CONFIRMED + FIXED

Root cause: the release smoke job checked out the release revision but fetched
`scripts/bootstrap.sh` from the mutable `HEAD` ref.

Fix: the workflow fetches the script from the exact
`${RELEASE_VERSION}` raw GitHub revision. `scripts/release_workflow_test.sh`
fails if the old `HEAD` URL returns or the release-version URL disappears;
both CI and the release workflow run that guard.

Files: `.github/workflows/release.yml`, `.github/workflows/ci.yml`,
`scripts/release_workflow_test.sh`.

Regression coverage: the workflow guard and shell syntax checks.

### 6. Setup checklist — CONFIRMED + FIXED

Root cause: the checklist compared each label only with the current step, so
completed steps lost their completed state as the current step advanced.

Fix: `installationStepState` uses the canonical ordered step definition and
explicit lifecycle aliases for handoff/cleanup. Completed prefixes remain
complete across refresh/reconnect, complete/handoff show all steps complete,
and a later failure marks only the failed current step.

Files: `console/src/features/auth/browser-setup-model.ts` and
`console/src/features/auth/browser-setup-view.tsx`.

Regression coverage: `console/src/features/auth/browser-setup-model.test.ts`
covers forward progress, complete/handoff, failure, install request, and
cleanup failure.

## Validation record

Passed locally:

- `PATH=/tmp/stealth-go-1.26.0/bin:$PATH go test ./... -count=1`
- `PATH=/tmp/stealth-go-1.26.0/bin:$PATH go vet ./...`
- repository-wide `gofmt` check, `git diff --check`, and `go mod verify`
- Console `npm run format:check`, `npm run typecheck`, `npm run lint`,
  `npm run test` (33 files, 144 tests), `npm run build`, and
  `npm run api:generate` with generated-file cleanliness check
- `./scripts/release_workflow_test.sh`, `./scripts/bootstrap_test.sh`,
  `./scripts/setup-security-test.sh`, and shell syntax checks
- `npm audit --omit=dev --audit-level=high`

The full Console Playwright run completed 29 of 33 tests; four existing
environment/fixture checks failed (queued-Agent timing, the Next.js dev-tools
button colliding with a generic `Next` locator, and two execution/deployment
status timing assertions). No setup checklist test failed. PostgreSQL-backed
integration tests were skipped because `TEST_DATABASE_URL` was not set. Race
testing was attempted but the environment has no C compiler (`-race` cannot
build without cgo/gcc). Docker/Compose image validation, CodeQL, and a fresh
VPS test were not available locally. No merge or VPS success claim is made by
this document.

Remediation PR: to be added after the branch is pushed.
