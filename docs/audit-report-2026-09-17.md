# Read-only repository audit report

Date: 2026-09-17
Audited revision: `b7c7848573caa92dc3e3be40d5caa79b25732eb5`
Branch: `refactor/console-midnight-ui`

This report records a read-only audit of the Go backend, CLI and installer,
setup flow, storage, realtime system, Console frontend, release workflow,
configuration, scripts, and documentation. No application source was changed
during the audit.

## Confirmed findings

### Medium: database row cursor pagination strips valid quote characters

Locations:

- [`internal/httpapi/server_databases.go:904`](../internal/httpapi/server_databases.go#L904)
- [`internal/repository/databases.go:138`](../internal/repository/databases.go#L138)
- [`internal/repository/database_rows.go:255`](../internal/repository/database_rows.go#L255)

`DecodeRowCursor` already JSON-decodes the cursor value. For text and varchar
columns, `canonicalCursorValue` then calls `strings.Trim(text, "\"")`.
This removes quote characters that may legitimately be the first or last
characters of a user's value. An ordered query whose boundary value is,
for example, `"quoted"`, can therefore produce a second-page cursor with the
wrong comparison value, causing rows to be skipped or duplicated.

### Medium: post-commit artifact cleanup can leave permanent orphans

Representative locations:

- [`internal/httpapi/server_storage.go:332`](../internal/httpapi/server_storage.go#L332)
- [`internal/httpapi/server_storage.go:740`](../internal/httpapi/server_storage.go#L740)
- [`internal/repository/storage.go:470`](../internal/repository/storage.go#L470)
- [`internal/repository/storage.go:865`](../internal/repository/storage.go#L865)
- [`internal/httpapi/server_functions.go:406`](../internal/httpapi/server_functions.go#L406)
- [`internal/httpapi/server_sites.go:301`](../internal/httpapi/server_sites.go#L301)
- [`internal/httpapi/server_database_backups.go:294`](../internal/httpapi/server_database_backups.go#L294)

Delete handlers commit metadata deletion first and remove the corresponding
blob or artifact afterward. If the filesystem or S3 operation fails, the API
returns an error although the metadata is already gone. A normal retry then
returns not found, while the physical object can remain indefinitely. No
general retry queue or orphan reconciler was found for this post-delete path.

### Medium: Console realtime subscriptions and cache mapping are incomplete

Locations:

- [`console/src/realtime/invalidation.ts:114`](../console/src/realtime/invalidation.ts#L114)
- [`console/src/realtime/invalidation.ts:139`](../console/src/realtime/invalidation.ts#L139)
- [`console/src/api/cache-coherence.ts:247`](../console/src/api/cache-coherence.ts#L247)
- [`internal/realtime/event.go:95`](../internal/realtime/event.go#L95)

The Console subscribes to an explicit event list, but it omits database row
events and several existing resource events such as `storage_file.*`, table
schema changes, function variables, site domains, API keys, and project-user
changes. The backend emits several of these events, and database row events
are explicitly marked for fanout. A second browser can therefore remain stale
until manual navigation or refetch.

There are also mapping gaps for subscribed events: storage events are mapped
to `storage-bucket` only even though the cache layer has a `storage-file`
change type, and webhook delivery events invalidate delivery/list queries but
not the open webhook detail query.

### Medium: Agent status can remain idle or stale while a run is queued

Locations:

- [`internal/repository/agent_runs.go:261`](../internal/repository/agent_runs.go#L261)
- [`internal/repository/agent_runs.go:614`](../internal/repository/agent_runs.go#L614)
- [`console/src/api/mutations/agents.ts:50`](../console/src/api/mutations/agents.ts#L50)
- [`cmd/worker/main.go:122`](../cmd/worker/main.go#L122)

`CreateAgentRun` inserts a run with status `queued` but does not refresh the
parent `project_agents.status`. A newly queued run can therefore leave the
agent displayed as `idle` until a worker claims it. The create mutation also
invalidates only run queries, not the parent agent list/detail queries. When
the worker is enabled without provider adapters, queued runs are explicitly
left queued, making the stale display persistent.

### Low/Medium: release smoke validates bootstrap from moving `HEAD`

Location: [`.github/workflows/release.yml:555`](../.github/workflows/release.yml#L555)

The release job checks out the release tag but downloads `scripts/bootstrap.sh`
from the repository's `HEAD`. The smoke test can therefore pass or fail based
on the moving default branch script rather than the script associated with the
release tag. This weakens release validation even though the release checklist
describes tag-pinned validation.

### Low: setup progress checklist does not retain completed steps

Location:
[`console/src/features/auth/browser-setup-view.tsx:1496`](../console/src/features/auth/browser-setup-view.tsx#L1496)

The `done` flag is true only when a checklist label equals the current step.
Earlier steps are rendered as pending again, and the `Complete` phase does not
match any checklist label. This is a browser UX correctness issue rather than
a deployment-state failure.

## Additional hardening and compatibility gaps

- S3 `Commit`, `RemoveRelative`, and `OpenRelative` use
  `context.Background()` in [`internal/storage/s3.go:145`](../internal/storage/s3.go#L145),
  and the `BlobStore` interface has no context parameter. A disconnected HTTP
  request cannot cancel those remote operations.
- Shared setup state permissions are documented as requiring manual migration
  for older `0600` state files before a different non-root host UID can resume
  them. See [`docs/host-side-setup-architecture.md:58`](host-side-setup-architecture.md#L58).

## Frontend and Anti-Slop review

- Design tokens are centralized in
  [`console/src/app/globals.css:23`](../console/src/app/globals.css#L23).
- Shared controls include visible focus treatment and approximately 44px
  minimum interaction heights.
- No confirmed dead navigation route, secret leakage, fake content, or missing
  primary loading/error state was found in the static and runtime checks.
- The realtime/cache findings and setup checklist issue above are the confirmed
  frontend behavior problems.

## Validation

Passed:

- `PATH=/tmp/stealth-go-1.26.0/bin:$PATH go test ./... -count=1`
- `PATH=/tmp/stealth-go-1.26.0/bin:$PATH go vet ./...`
- Focused Go tests for repository, HTTP API, storage, and CLI
- `go mod verify`
- `npm run typecheck`
- `npm run lint`
- `npm run format:check`
- `npm test` - 33 files and 132 tests
- `npm run build`
- Playwright E2E - 33 passed
- `npm audit --omit=dev --audit-level=high`
- `bash -n` for the deployment shell scripts
- `git diff --check`

Not completed locally:

- `go test -race ./...` could not build because the environment has no `gcc`.
- Docker-based image and security scans were not available locally.
- Fresh VPS E2E was not performed. The standalone setup page check could only
  exercise its fallback because no full backend, database, Docker daemon, or
  Quick Tunnel was available in the environment.

GitHub CI and CodeQL checks observed for the audited HEAD were successful.

## Repository/PR status

- Working tree was clean after the audit.
- No application code was changed.
- No commit was created by the audit itself.
- PR #75 remains open: <https://github.com/Stealth-deplover/stealth/pull/75>

**STATUS: AUDIT SELESAI - KODE TIDAK DIUBAH**
