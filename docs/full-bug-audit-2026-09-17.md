# Full repository bug audit — 2026-09-17

## Scope and revision

This is a read-only audit of the current repository state. No production source,
configuration, workflow, or test code was changed for this audit; this document
is the only intended worktree change.

| Item | Value |
| --- | --- |
| Latest `origin/main` SHA | `157b1ba0903f2f1dc0e7be96e6a7b3d0a387360e` |
| Audited HEAD SHA | `fc27da73e9fcd2489993a8da50f12a06e6a9cecc` |
| Branch | `fix/audit-2026-09-remediation` |
| Historical audit SHA | `010250f5a3c6f1407aeabb6335b189d9b3b55822` |
| Existing remediation PR | [PR #76](https://github.com/Stealth-deplover/stealth/pull/76), open |

The review covered tracked Go backend/CLI/repository/worker code, Console
source and tests, HTTP/API routing, storage backends, realtime transport and
cache mapping, migrations, Docker/Compose files, release and security scripts,
workflows, and the relevant architecture documentation. Generated output and
dependency trees were not treated as application source. The conclusions below
are based on current function-level tracing and minimal reproductions. A live
PostgreSQL/Redis/S3/Docker/VPS environment was not available locally, so the
storage and transport findings that need those services are reported as static
or sequence reproductions rather than claimed live integration successes.

Classification uses `CONFIRMED`, `PARTIALLY VALID + RESIDUAL CONFIRMED`,
`ALREADY FIXED ON CURRENT HEAD`, and `UNCONFIRMED / POLICY GAP`.

## Executive summary

The six historical findings from the original audit were re-verified against
the current HEAD. Findings 1, 5, and 6 are fixed. Finding 4's original
create-queued-run case is fixed. Findings 2 and 3 are only partially resolved:
the original paths were hardened, but related current paths still have
correctness gaps.

Four current residual issues were confirmed:

1. Physical artifacts can still become unreachable when publication succeeds
   and the metadata transaction never completes.
2. Messaging subscriber/message events, and webhook secret-rotation events,
   are generated but dropped before Console realtime invalidation.
3. Stale Agent runs are requeued and the parent Agent status is refreshed in
   the database, but that recovery mutation emits no realtime notification.
4. S3 operations use `context.Background()` through a context-free storage
   interface, so request cancellation cannot stop remote work.

The first three are correctness/consistency issues. The fourth is a resource
and reliability hardening gap; no direct data exposure was found.

## Historical finding verification

| Finding | Current status | Current evidence |
| --- | --- | --- |
| 1. Cursor pagination corrupts quoted boundaries | **ALREADY FIXED ON CURRENT HEAD** | `canonicalCursorValue` in `internal/httpapi/server_databases.go` keeps textual values as strings and uses typed parsing; `internal/repository/databases.go` decodes typed cursors. Existing cursor tests cover quoted text, scalar normalization, ordering boundaries, and null rejection. |
| 2. Metadata deletion can orphan physical artifacts | **PARTIALLY VALID + RESIDUAL CONFIRMED** | Delete paths now use the durable cleanup queue, but post-commit upload/build paths still rely on best-effort rollback and have no durable record if the process dies before metadata creation. See Finding A. |
| 3. Realtime fanout/subscriptions/cache invalidation are incomplete | **PARTIALLY VALID + RESIDUAL CONFIRMED** | Database row, storage file/bucket, deployment, and Agent run mappings are present and tested. Messaging subscriber/message and webhook secret-rotation events are still not in the backend/frontend active event set. See Finding B. |
| 4. Queued Agent runs leave parent Agent status stale | **ALREADY FIXED for the original case; related recovery gap CONFIRMED** | `CreateAgentRun` inserts the queued run and calls `refreshAgentStatusTx` in the same transaction. A stale running run is also requeued and status-refreshed, but `RequeueStaleAgentRuns` emits no realtime event. See Finding C. |
| 5. Release smoke test downloads `bootstrap.sh` from `HEAD` | **ALREADY FIXED ON CURRENT HEAD** | `.github/workflows/release.yml` resolves `RELEASE_VERSION` and downloads `scripts/bootstrap.sh` from that revision. `scripts/release_workflow_test.sh` rejects `/HEAD/` and requires the release revision. |
| 6. Browser setup checklist loses completed steps | **ALREADY FIXED ON CURRENT HEAD** | `installationStepState` in `console/src/features/auth/browser-setup-model.ts` uses the canonical ordered step list and marks the completed prefix monotonically. Tests cover forward progress, complete/handoff, failure, fresh request, and cleanup. |

## Confirmed current findings

### Finding A — Upload/build publication has a crash window for orphaned artifacts

**Status:** `CONFIRMED` (related residual to historical finding 2)
**Severity:** Medium
**Area:** storage, function/site builds, database backups

#### Current implementation

The durable deletion mechanism is correctly present for metadata deletion:

- `internal/repository/artifact_cleanup.go` stores cleanup work durably.
- `internal/artifactcleanup/worker.go` retries failures, recovers stale leases,
  treats already-missing artifacts as successful, and validates relative paths.
- `internal/repository/storage.go` (`DeleteStorageBucket` and
  `DeleteStorageFile`) queues physical cleanup instead of making metadata
  deletion depend on a remote delete.
- Similar cleanup-aware paths exist in `functions_deployments.go`,
  `sites_artifacts.go`, `database_backups.go`, and `projects.go`.

The opposite direction is not durable. The following paths publish bytes first,
then create metadata:

- `internal/httpapi/server_storage.go`, `uploadStorageFile`: `Commit` is
  followed by `CreateStorageFile`; the deferred rollback calls
  `RemoveRelative` on a best-effort basis.
- `internal/httpapi/server_functions.go`, `uploadFunctionDeployment`:
  `Commit` is followed by `CreateFunctionDeployment`; error cleanup is best
  effort.
- `internal/httpapi/server_sites.go`: archive, extracted-directory, and Git
  upload paths call `Commit`/`CommitDirectory` before metadata insertion and
  use best-effort removal on failure.
- `internal/httpapi/server_database_backups.go`: `Commit` precedes
  `CreateDatabaseBackup` and failure cleanup is best effort.
- `internal/functionrunner/build.go` and `internal/functionrunner/site_worker.go`:
  build output is committed before the completion metadata transaction, with
  best-effort removal if completion fails.

#### Minimal reproduction

1. Start an upload or build output and let physical `Commit` succeed.
2. Terminate the process, or make the following metadata transaction fail,
   before metadata creation/completion commits.
3. The physical object remains under the validated storage root/key.
4. No metadata row and no artifact-cleanup job identify that object.

The same outcome occurs if best-effort `RemoveRelative` fails after a metadata
error. The current cleanup worker cannot repair this sequence because it only
processes known cleanup jobs; no physical orphan scan or durable upload-intent
reconciler was found in the repository.

This is distinct from the historical metadata-delete ordering bug, which the
durable deletion queue resolves.

#### Impact and safety review

The object is not user-visible through normal metadata queries, but it consumes
storage indefinitely and can be billed or exhaust a storage quota. The path
validation itself is not the issue: `internal/storage/store.go` and
`internal/sitestore/store.go` keep local cleanup rooted, and S3 key validation
is bounded in `internal/storage/s3.go`.

#### Recommended focused remediation

Add a durable upload-intent/finalization record (or equivalent pending metadata
state/outbox) before physical publication. Finalization should make the
metadata and physical object relationship recoverable after a crash. A
reconciler can retry/remediate stale intents and must keep cleanup idempotent and
root-constrained. Do not replace the current durable delete queue with an
in-memory queue.

#### Existing and missing regression coverage

Existing protection covers the delete side:

- `internal/artifactcleanup/worker_test.go` covers successful cleanup, missing
  local artifacts, retry scheduling, stale lease recovery, and path escape.
- `internal/repository/artifact_cleanup_integration_test.go` covers retry and
  lease recovery when integration configuration is available.

Missing coverage is a crash/failure between successful physical commit and
metadata finalization, including process restart/reconciliation.

### Finding B — Messaging realtime events are not end-to-end connected

**Status:** `CONFIRMED` (related residual to historical finding 3)
**Severity:** Medium
**Area:** repository outbox, realtime fanout, Console cache coherence

#### Current implementation

The repository creates durable audit/outbox events for mutations:

- `internal/repository/messaging.go`: `CreateMessagingSubscriber` emits
  `messaging.subscriber.create`; `DeleteMessagingSubscriber` emits
  `messaging.subscriber.delete`.
- `internal/repository/messaging_delivery.go`: message creation emits
  `messaging.message.create`; cancellation emits
  `messaging.message.cancel`.
- `internal/repository/webhooks.go`: secret rotation emits
  `webhook.secret_rotate`.

However, `internal/realtime/event.go:ShouldFanout` includes messaging provider,
topic, and delivery events but not the subscriber/message events above, and it
does not include `webhook.secret_rotate`. The events therefore remain durable
audit/webhook records but do not reach the Redis/websocket/SSE notification
path.

The Console allowlist in `console/src/realtime/invalidation.ts` has the same
gap. Its generic messaging mapping invalidates providers, topics, and messages
through `console/src/api/cache-coherence.ts`, but it can only run if the event
first reaches the stream. The topic projection includes `subscriber_count` in
`internal/repository/messaging.go`, while the Console displays that count in
`console/src/features/messaging/messaging-view.tsx`.

#### Minimal reproduction

1. Open the Messaging page in one Console session.
2. Create, enable/disable, or delete a subscriber for an existing topic, or
   create/cancel a message from another session/API client.
3. The database mutation and durable event are committed.
4. `ShouldFanout` returns false for the subscriber/message event, so no live
   notification is delivered.
5. The first Console session keeps its topic subscriber count or message list
   stale until a navigation/manual refetch.

The same sequence applies to another Console session after a webhook secret
rotation: the mutation is durable, but the missing fanout event leaves the
other session's webhook cache stale.

This is not a claim that every audit event should be broadcast. It is a gap for
events with active Console data consumers.

#### Recommended focused remediation

Define one canonical event-to-consumer mapping and add the missing event types
to both sides of the pipeline. Subscriber changes should invalidate the topic
projection (and subscriber queries if/when exposed); message changes should
invalidate message/topic projections as appropriate; secret rotation should
invalidate the affected webhook detail/list scope. Keep project/resource
filtering and scoped invalidation; do not invalidate the entire Query cache.

#### Existing and missing regression coverage

Existing tests cover database rows, storage file/bucket separation, Agent run
mapping, unknown events, and the currently supported fanout allowlist in
`console/src/realtime/invalidation.test.ts` and
`internal/realtime/event_test.go`.

Missing coverage is an end-to-end assertion for:

- `messaging.subscriber.create/delete`;
- `messaging.message.create/cancel`;
- `webhook.secret_rotate`;
- unrelated-project event rejection at the transport/invalidation boundary.

### Finding C — Stale Agent-run recovery updates SQL but does not notify Console

**Status:** `CONFIRMED` (related residual to historical findings 3 and 4)
**Severity:** Medium
**Area:** worker recovery and Agent realtime projection

#### Current implementation

The original queued-run defect is fixed. `internal/repository/agent_runs.go`
`CreateAgentRun` inserts the queued run, calls `refreshAgentStatusTx`, and
commits both changes in one transaction. The status derivation is centralized:

- a running run produces Agent status `running`;
- otherwise a queued run produces the existing `active` status;
- otherwise the Agent is `idle`.

The recovery path `RequeueStaleAgentRuns` updates stale running rows to
`queued`, calls the same `refreshAgentStatusTx`, and commits the transaction.
It does not write a corresponding `agent.run.*` or `agent.update` outbox event.

`console/src/api/queries/agents.ts` does not periodically refetch the parent
Agent list/detail. The active-run query polls, but that does not update a
separate cached Agent query without an invalidation event.

#### Minimal reproduction

1. Let an Agent run be `running` with an expired worker claim.
2. Run the worker recovery path so `RequeueStaleAgentRuns` changes it to
   `queued` and refreshes the parent Agent to `active`.
3. Keep an already-open Agent list/detail page in another Console session.
4. No outbox/realtime notification is produced by the recovery transaction.
5. The parent Agent cache can continue to display `running` until navigation,
   manual invalidation, or another event updates it.

The database state is internally consistent; the defect is the stale external
projection.

#### Recommended focused remediation

Emit a safe, project-scoped recovery event in the same transaction, preferably
only when the derived parent status changes. Reuse the existing canonical
status derivation and event/cache mapping rather than adding independent status
assignments in worker code.

#### Existing and missing regression coverage

The existing Agent run realtime test covers a worker lifecycle event and parent
Agent invalidation. `CreateAgentRun` behavior is covered by the repository test
suite and current PR checks.

Missing coverage is a stale-claim recovery test that asserts both the committed
status and the realtime event/cache invalidation for a second Console session.

### Finding D — S3 storage operations ignore request cancellation

**Status:** `CONFIRMED STATIC RELIABILITY GAP`
**Severity:** Low/Medium
**Area:** storage abstraction and remote object operations

#### Current implementation

`internal/storage/store.go` defines `BlobStore` methods such as `Commit`,
`RemoveRelative`, `RemoveProject`, and `OpenRelative` without a context
parameter. `internal/storage/s3.go` consequently calls MinIO using
`context.Background()` for destination checks, object uploads, cleanup, project
listing, and downloads.

HTTP request handlers invoke these operations while handling client-driven
uploads/downloads. If the client disconnects or the request deadline expires,
the S3 call has no request context to observe and may continue until the remote
client/network timeout. Cleanup workers also need a separate bounded background
context, so simply replacing every call with a request context would be
incorrect.

#### Minimal reproduction

1. Start a large S3-backed upload or download.
2. Cancel the HTTP request or close the client connection.
3. The handler's request context is canceled, but the S3 operation was started
   with `context.Background()` and continues independently.

No live S3 daemon was available locally to measure the duration, so this is a
direct static control-flow finding rather than a timing claim.

#### Recommended focused remediation

Make request-facing blob operations context-aware and pass the HTTP context
through. Keep explicit bounded contexts for worker/reconciliation operations.
Add cancellation tests with a blocking mock client and ensure cleanup remains
idempotent. This is separate from the artifact publication durability issue;
both affect storage reliability but require different fixes.

## Reviewed paths with no confirmed defect

The following areas were traced and did not reproduce the historical issue or a
new correctness defect in the current revision:

- Cursor typed decoding and quoted/unicode/empty scalar boundary handling:
  `internal/httpapi/server_databases.go`,
  `internal/repository/databases.go`, and cursor tests.
- Host-side installer ownership, request-only setup install endpoint, run ID
  checks, lock/error propagation, and setup Docker authority removal.
- Database row and storage file/bucket realtime mapping, including separate
  file and bucket cache keys.
- Agent creation status refresh and transaction atomicity.
- Release revision pinning and the `/HEAD/` regression guard.
- Browser setup checklist progression, refresh/resume, failed current step,
  handoff, and complete state.
- Setup authentication, project-scoped realtime filtering, secret-redaction
  checks, and the setup Docker security regression script.

These are not claims that the product has no other bugs. They are the paths
covered by this audit where the expected defect was not present at the audited
revision.

## Additional risks requiring a product decision

### Host preflight projection on failed checks — `UNCONFIRMED / POLICY GAP`

`internal/cli/setup.go:runWebBootstrap` exits before starting the setup UI when
required host checks fail. `internal/cli/preflight_projection.go` persists
host results after setup orchestration starts, while the setup API can display a
“host CLI has not published system checks” state when no projection exists.
This is safe fail-fast behavior, but it means a user may not reach the browser
System Check page to see a failed Docker/CPU/RAM/disk result. Confirm whether
the intended product contract is CLI fail-fast or browser-visible host
preflight; do not let the setup container run substitute Docker checks.

### Shared setup-state identity — `UNCONFIRMED / DOCUMENTED COMPATIBILITY RISK`

`internal/setupstate/store.go:PrepareShared` creates setgid/group-private
permissions but does not choose or repair a group. The architecture document
states that the operator must own or belong to the directory group and that
older `0600` state files require migration before a different non-root host UID
can resume. The current tests verify modes, not a real distinct host UID/GID
resume. The product should either test/enforce supported non-root identity or
explicitly make installation root-only; the current documentation is the only
guard.

## Validation performed

| Check | Result |
| --- | --- |
| `gofmt -l $(git ls-files '*.go')` | PASS; no files reported |
| `go test ./... -count=1` | PASS; PostgreSQL/Redis-backed tests were skipped because no `TEST_DATABASE_URL` was configured |
| `go vet ./...` | PASS |
| `go mod verify` | PASS |
| `git diff --check` | PASS |
| Console `npm run format:check` | PASS |
| Console `npm run typecheck` | PASS |
| Console `npm run lint` | PASS |
| Console `npm run test` | PASS; 33 files and 144 tests |
| Console `npm run build` | PASS; production Next build completed |
| Console `npm audit --omit=dev --audit-level=high` | PASS; 0 vulnerabilities |
| `scripts/release_workflow_test.sh` | PASS |
| `scripts/bootstrap_test.sh` | PASS |
| `scripts/setup-security-test.sh` | PASS |
| `bash -n` on bootstrap, smoke, release, and security scripts | PASS |
| `gh pr checks 76` | PASS; current GitHub checks including Go, JS/TS, Console, Installer/release, and CodeQL were green at audit time |
| `go test -race ./...` | NOT AVAILABLE; race mode requires CGO and the environment has no `gcc` |
| Real Docker/Compose setup or image validation | NOT RUN; Docker is unavailable in the environment |
| Real PostgreSQL/Redis integration run | NOT RUN locally; required test environment variables/services were unavailable |
| Real S3 integration | NOT RUN; no S3-compatible service was available |
| Playwright `npm run test:e2e` | **29 passed, 4 failed**; see below |
| CodeQL/security scanners locally | NOT RUN; local scanner binaries are unavailable; hosted CodeQL check was green |
| Fresh VPS E2E | NOT PERFORMED; no VPS was available |

### Playwright failures requiring follow-up

The run was not clean and should not be reported as a full E2E pass:

- `console/tests/e2e/agents.spec.ts:286`: the fixture advances from queued to
  running by detail-read count while `useAgentRun` polls active runs every
  second; the assertion can miss the transient queued state.
- `console/tests/e2e/critical-flow.spec.ts:582`: the unscoped accessible name
  `Next` matches the product pagination button and a Next.js dev-tools button
  in the test environment.
- `console/tests/e2e/critical-flow.spec.ts:759`: the fixture expects the
  transient `Accepted` execution state while the page polls and can advance
  before the assertion.
- `console/tests/e2e/critical-flow.spec.ts:805`: the fixture expects the
  transient `Queued` site-build state while the page polls and can advance
  before the assertion.

These failures are test/fixture timing or selector findings from this run, not
classified as confirmed production defects without a stable backend-backed
reproduction. They do require fixing before calling the browser E2E suite
green.

## Recommended remediation order

1. Add durable upload-intent/finalization recovery for all publish-before-
   metadata paths.
2. Complete the canonical realtime event map for messaging, webhook secret
   rotation, and stale Agent-run recovery; add transport-to-cache tests.
3. Introduce context-aware request-facing blob operations with bounded worker
   contexts.
4. Resolve the host preflight UX contract and exercise shared-state recovery
   under the supported UID/GID model.
5. Stabilize the four Playwright fixtures/selectors and rerun the complete
   validation matrix.

## Audit conclusion

The historical audit fixes on the current branch are materially present, but
the repository is not free of related correctness risks. The residual storage
publication window and realtime gaps should be treated as remediation work
before declaring the audit fully closed. This audit itself made no code or
configuration changes.

**STATUS: AUDIT REPORT COMPLETE — CODE NOT CHANGED AT AUDIT TIME**

## Follow-up remediation status

The historical audit above was read-only at the time it was written. The four
residual issues described in Findings A–D are now implemented in the current
working tree on `fix/audit-2026-09-remediation`:

- durable pre-publication reservations use the existing PostgreSQL artifact
  cleanup worker for crash/retry recovery, including ambiguous metadata
  outcomes where direct physical rollback could otherwise remove a referenced
  artifact;
- messaging and webhook-secret events are included in backend fanout and the
  Console scoped invalidation map;
- stale Agent-run recovery emits `agent.run.queued` after refreshing the
  parent Agent in the same transaction; and
- S3 stat/put/get/list/remove paths and storage wrappers accept request
  contexts.

Regression tests cover these paths. The follow-up is committed and pushed in
[PR #76](https://github.com/Stealth-deplover/stealth/pull/76); CI status is
tracked there. A fresh VPS validation has not been performed.
