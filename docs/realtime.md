# Realtime event infrastructure

Stealth realtime events are notifications, not a second source of truth.
PostgreSQL and the Go API own canonical resource state. A Console subscriber
uses a notification to invalidate a TanStack Query key and then reads the
current state from the normal Go API endpoint.

## Flow

For integrated mutations the flow is:

```text
DB state mutation + webhook_events outbox row (one transaction)
  -> realtime publisher worker claims the row with FOR UPDATE SKIP LOCKED
  -> Redis Pub/Sub project channel (ephemeral fanout)
  -> authorized Go SSE subscriber
  -> Console query invalidation
  -> canonical Go API refetch
```

The existing `webhook_events` table is shared deliberately. It already had
the transaction boundary and seven-day retention needed by project activity
notifications, and webhook delivery rows remain independent from realtime
publication. The outbox adds an event version, organization/correlation
metadata, publication attempts, a lease, retry time, and terminal publication
status. A worker crash leaves the row pending or leased for recovery.

Redis fanout is intentionally allowlisted to lifecycle notifications with a
realtime consumer (runs, executions, deployments, delivery status, selected
resource-list changes, and database-row events). Other audit/webhook rows are
still retained and available from PostgreSQL, but the publisher marks them
handled without sending unnecessary project-channel traffic.

The version-one notification envelope contains `id`, `type`, `version`,
`occurred_at`, `organization_id`, `project_id`, an optional `resource_id`, an
optional `correlation_id`, and small non-secret `payload` metadata. Event IDs
are opaque UUIDs. There is no global ordering or exactly-once guarantee.

## Audited asynchronous paths

The event backbone builds on the existing durable queues and their explicit
claim/lease boundaries. The current paths are documented here so consumers do
not mistake Redis for the job queue:

| Path | Enqueue and claim | Execute and terminal state | Recovery/publication |
| --- | --- | --- | --- |
| Agent Runs | API inserts `queued`; an agent worker claims with `FOR UPDATE SKIP LOCKED` | Provider worker writes a terminal result while fenced by `worker_id` | Expired running leases return to `queued`; lifecycle notification is inserted in the same transaction |
| Function executions | API inserts `accepted`; function worker claims and fences `running` | Isolated runtime uses a context timeout, then writes `succeeded`/`failed` | Expired leases return to `accepted`; accepted, running, and terminal notifications share the DB transaction |
| Function/Site builds | API inserts a queued deployment; the corresponding build worker claims a lease | Build timeout and explicit completion/failure transitions | Expired build leases return to `deferred`; deployment notifications are transactional |
| Webhook deliveries | Event insertion creates delivery rows; webhook worker claims pending deliveries | HTTP delivery classifies success, retryable failure, or terminal failure | Delivery leases are requeued/recovered with bounded retry/backoff; delivery notifications are transactional |
| Messaging deliveries | Provider/topic mutation creates delivery work; messaging worker claims pending rows | Provider call writes delivery outcome | Stale leases and bounded retry recover the row; delivery notifications are transactional |
| Database backups | Backup upload/restore is an API-controlled, bounded storage operation rather than a background queue | The database restore path runs in one transaction and records audit metadata | Backup metadata and audit records remain durable; no separate backup worker or realtime stream is claimed |

Audit/activity events continue to be written with their mutation and remain
available through the canonical activity API. They are not broadcast
automatically unless a resource notification has a concrete Console consumer.
All integrated notification rows use the same outbox lease/retry path, so a
worker crash can delay publication but cannot erase a committed event.

## SSE endpoint and authorization

The endpoint is:

```text
GET /v1/projects/{projectID}/realtime
Accept: text/event-stream
```

Console sessions authenticate through the existing HttpOnly session cookie.
API keys require the existing `realtime.read` scope. Project authorization is
performed by Go on connection and on every database fallback poll; changing a
project ID in the URL cannot cross the tenant boundary. Credentials are never
placed in query parameters.

The endpoint supports `Last-Event-ID` and the explicit `cursor` query
parameter. `Last-Event-ID` takes precedence when both are present; the query
parameter is for clients that cannot set the header. An initial connection
with neither cursor starts at the current project event tail and does not
replay retained history. A reconnect cursor replays retained events strictly
newer than that cursor, then returns to live delivery. If the cursor has
expired or is not a retained event in the authorized project, the server
resets to the current tail; the Console's canonical refetch recovers any
state changes outside the bounded replay window.

Events are retained for approximately seven days for bounded resume/debugging,
not as a permanent event archive. The worker periodically prunes expired
`webhook_events` rows in bounded batches, including rows used only for
realtime notifications. Native `EventSource` reconnects automatically. The
Console keeps bounded polling for active resources as a recovery path when
Redis or SSE is unavailable.

Each connection has a bounded server slot and Redis subscriber buffer. A slow
client is disconnected rather than allowed to block the publisher or grow an
unbounded buffer. Nginx disables buffering only for this endpoint and keeps a
long read timeout.

## Redis and failure behavior

Redis Pub/Sub is only a low-latency notification. It is not durable and a
missed Redis message is reconciled by the SSE connection's PostgreSQL cursor
poll. If Redis is unavailable, the PostgreSQL outbox is not marked published
and the publisher retries with bounded exponential backoff and jitter.
Existing SSE connections continue using the database polling fallback, and
the canonical API does not depend on a Redis delivery acknowledgement.
Duplicate notifications are possible after a publish/worker crash boundary;
Console invalidation and API reads are idempotent.

In the current API composition Redis is also the existing distributed rate
limiter dependency, so `/readyz` retains its established Redis readiness
requirement. Realtime fanout itself remains degradable: an SSE connection can
poll PostgreSQL and the Console keeps its bounded polling fallback.

The publisher exposes bounded metrics for attempts, successes, skipped rows,
failures, and publish duration, plus current non-expired outbox depth. API
metrics include active SSE connections, accepted connections, delivered
notifications, and slow-client disconnects. Structured publisher logs include
the event ID, type, project ID, correlation ID, and attempt without logging the
payload.

## Integrated notifications

The initial integration covers Agent Run claim/accept/terminal transitions,
Function execution accept/claim/terminal transitions, Function/Site deployment
create, claim, build completion/failure, activation, and deletion notifications,
and webhook/messaging delivery transitions. Existing audit/webhook event names
remain compatible; the `*.updated` lifecycle events are additive. Other audit
events remain in the existing outbox but are not necessarily mapped to a
Console query.

The system does not stream full logs or resource snapshots. Agent and build
log viewers continue using their incremental HTTP log APIs. Sensitive values
such as API keys, webhook secrets, cookies, authorization headers, tokens, and
passwords are excluded at the event payload boundary.
