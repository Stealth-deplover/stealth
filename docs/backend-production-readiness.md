# Backend production-readiness contract

This document records the execution paths and operational assumptions of the
Go API and trusted worker process. It describes guarantees that exist in the
repository; it is not a claim of exactly-once execution for arbitrary external
providers.

## Durable work mapping

| Work | Enqueue | Claim and execute | Retry / terminal state | Recovery |
| --- | --- | --- | --- | --- |
| Function deployment build | Deployment row is created with `build_status=queued` in the resource transaction. | `ClaimNextFunctionDeployment` uses a PostgreSQL transaction and `FOR UPDATE OF d SKIP LOCKED`; the worker builds with a deadline and records `succeeded` or `failed`. | Build validation and configuration errors are terminal. | `running` builds older than the configured lease become `deferred`; the worker ID fences completion writes. |
| Function execution | Execution row is created with `status=accepted`. | `ClaimNextFunctionExecution` uses `FOR UPDATE OF e SKIP LOCKED`; the runtime receives a context timeout and terminal writes are fenced by worker ID. | Runtime failures are terminal rather than blindly retried because user code may have side effects. | Stale `running` executions return to `accepted`; a replacement worker can claim them. |
| Site build | Site deployment row is created with `build_status=queued`. | `ClaimNextSiteDeployment` uses `FOR UPDATE OF d SKIP LOCKED`; archive extraction and build have bounded limits and a deadline. | Invalid archives, build errors, and quota failures are terminal. | Stale `running` builds become `deferred`; completion checks the build worker ID. |
| Agent run | Run row is created with `status=queued`. | Provider workers claim with `FOR UPDATE OF r SKIP LOCKED`; provider calls use a context timeout and terminal writes require the claiming worker ID. | Provider failures are terminal. Unknown providers remain queued. | Stale `running` runs return to `queued`; the agent status is refreshed transactionally. |
| Webhook delivery | Mutation transaction writes an outbox event and its delivery rows. | Delivery claim is transactional and `SKIP LOCKED`; outbound HTTP has SSRF checks, a response limit, and a timeout. | HTTP 408/425/429/5xx and network failures retry up to 12 attempts with bounded exponential jitter (or a bounded `Retry-After`); permanent 4xx and expiry are terminal. | Stale leases return to `pending`; expired events are marked failed. `X-Stealth-Delivery` is stable for consumer idempotency. |
| Messaging delivery | Message and delivery rows are created transactionally with message idempotency constraints. | Delivery claim is transactional and `SKIP LOCKED`; provider calls have a timeout and bounded response handling. | Provider adapters classify retryable failures; attempts are capped at 12 and use bounded exponential jitter. | Stale leases return to `pending`; worker ownership fences terminal updates. |
| Database backup | Backup is a synchronous request path with row and payload limits. | The API reads bounded rows and stores an immutable object in one resource transaction. | There is no background retry loop. | The request fails without inventing a backup count; operators must retry the request after correcting the cause. |

All terminal transitions are guarded by the repository state machine or a
worker ownership check. A cancelled or terminal row is not silently moved back
to an active state. The queue claim transactions prevent two workers from
claiming the same eligible row concurrently.

## Operational controls

- `DATABASE_MAX_CONNS`, `DATABASE_MIN_CONNS`,
  `DATABASE_MAX_CONN_LIFETIME`, and `DATABASE_MAX_CONN_IDLE_TIME` bound the
  PostgreSQL pool in both API and worker processes.
- `PROJECT_OPERATION_RATE_LIMIT` and `PROJECT_OPERATION_RATE_WINDOW` apply a
  per-project/per-authenticated-actor safety budget to function and site
  deployments, function executions, Agent runs, message sends, storage
  uploads, database imports/transactions, and backups. Authenticated actor
  buckets do not include client IP, so changing networks cannot reset a
  tenant/account budget. Anonymous function calls use a separate resolved
  project/IP bucket. A limit returns the normal API error envelope with HTTP
  429 and `Retry-After`. This is a platform safety limit, not a billing tier.
- `/healthz` is liveness only. `/readyz` checks PostgreSQL, required storage,
  function/site stores, and the configured rate limiter. Optional provider
  integrations do not make readiness fail.
- Prometheus output is disabled unless `METRICS_TOKEN` is set. When enabled,
  `/metrics` requires `X-Metrics-Token`; the worker listener defaults to
  loopback and should remain on a private network. `/healthz` on the worker
  listener remains the process liveness probe.
- Every API request gets a validated or generated `X-Request-ID`. The value is
  returned in the response and is present in request, panic, internal-error,
  and trace-index logs. Client-IP forwarding is disabled unless the direct
  peer matches `TRUSTED_PROXY_CIDRS`; then bounded `X-Forwarded-For`,
  `Forwarded`, or `X-Real-IP` parsing is used with a direct-peer fallback.
  The ingress must normalize and sanitize forwarded headers as described in
  the deployment README.
- SIGTERM/SIGINT cancels worker loops and active provider/build contexts,
  stops new claims, closes the metrics listener, and closes the database pool.
  If a provider ignores cancellation, the database lease recovery path is the
  safety net after the configured lease age.
- Migrations are ordered embedded files and are serialized with a PostgreSQL
  advisory lock. The API does not serve traffic when migration application
  fails.

Uploads and archive builds enforce request/object/expanded-size and file-count
limits, reject absolute and traversal paths, reject unsafe archive entries, and
clean temporary files. Secrets are kept out of structured logs, audit
metadata, client error messages, and Prometheus labels.

## Security assumptions

The Go API owns sessions and uses HttpOnly cookies with `Secure` and
`SameSite=None` for explicitly configured cross-origin HTTPS console requests;
local HTTP uses `SameSite=Lax`. CORS is an explicit allowlist. Because the
console can be a separate origin, CSRF protection relies on the same-origin
deployment recommendation, explicit CORS/Origin checks, and the cookie
SameSite policy; a deployment that permits arbitrary origins is not supported.

Webhook and Git fetch clients disable proxy use and redirects, resolve targets
before dialing, block loopback/private/link-local/multicast/unspecified
addresses, and bound response bodies. This reduces DNS-rebinding/SSRF risk;
operators must still treat outbound delivery as a network policy boundary.

## Remaining production gaps

These are intentional boundaries, not hidden reliability claims:

- External provider side effects cannot be made exactly-once by PostgreSQL.
  Webhook consumers have a stable delivery ID, and messaging has database
  idempotency/fenced delivery state, but a provider that succeeds immediately
  before the worker crashes can still observe a duplicate after lease recovery.
  Providers should deduplicate their own request IDs where supported.
- Function execution and Agent provider failures are terminal rather than
  automatically retried. Retrying arbitrary user code or an Agent tool call
  could duplicate side effects. Lease expiry is recovery of an interrupted
  claim, not an assertion that the external work did not happen.
- Deployment creation and Agent run creation do not yet expose a client
  idempotency-key contract. Callers that need request-level deduplication must
  avoid resubmitting after an unknown response until that contract is added.
- There is no distributed per-project worker semaphore. Queue claims are safe,
  per-operation API budgets and resource/archive limits provide admission
  control, and each worker loop processes one job at a time; a large tenant
  can still consume available replicas. A future concurrency quota should be
  added only with an explicit product/platform requirement.
- Database backups are user-resource operations, not a platform disaster
  recovery system. Production operators still need PostgreSQL backups,
  object-storage versioning/backup, and periodic restore verification.
