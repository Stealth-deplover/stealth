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
| App build | AppDeployment and a reserved immutable source artifact are recorded before the queued row is committed. | `ClaimNextAppDeployment` uses PostgreSQL `FOR UPDATE SKIP LOCKED`; a dedicated worker verifies and extracts the archive, then invokes `buildctl` against the isolated BuildKit service under a deadline. | BuildKit output must include a valid metadata digest and a verified OCI layout before publication; failures have bounded messages and release artifact/quota reservations. | Stale leases become reclaimable, and every completion is fenced by a unique per-claim worker token. BuildKit unavailability leaves the queue deferred/available and does not stop unrelated worker loops. |
| App runtime, health, route, and logs | App mutations and deployment selection advance desired generation; deleting an App or project writes durable cleanup work before removing rows. Health is separate state tied to generation, deployment, container, and routing-incarnation identity. Fenced runtime completion upserts verified container IDs into PostgreSQL metadata; log bodies remain in ClickHouse. | Runtime and health claims use PostgreSQL transactions and `FOR UPDATE ... SKIP LOCKED`; the worker verifies the persisted OCI archive, inspects the managed container, runs bounded TCP/HTTP probes, and publishes a separate App Traefik snapshot from an authoritative database query. The project-scoped runtime-log API resolves App sources from PostgreSQL and issues one bounded typed ClickHouse query. | Runtime failures use bounded retries. Health uses the WorkloadSpec initial delay, interval, timeout, and failure threshold. Stale leases or identities cannot publish health or log-source mappings. Telemetry query failures affect log reads only. | Startup marks prior observations for inspection and checked health due, expired leases are reclaimable, cleanup survives App/project deletion, and orphan sweeps queue only ownership-validated containers. Verified historical container IDs remain mapped while the App exists. `running` means process liveness; an App route requires enabled state, a ready selected deployment, matching desired/observed generations, current runtime identity, and healthy state. Runtime logs are retained according to Docker local rotation and ClickHouse TTL. |
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
  `/metrics` requires `X-Metrics-Token`; the production Compose worker
  listener binds only to the private Compose network so the OTel Collector can
  scrape it. It has no host port. `/healthz` on the worker listener remains
  the process liveness probe.
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

App builds have a dedicated `apps.build` trace and bounded, low-cardinality
claim, completion, duration, retry, and error metrics. The worker probes the
BuildKit daemon before it claims an App job. BuildKit readiness is reported by
the actual `buildctl debug workers` operation; it is not inferred from the
daemon process existing. `stealth doctor` includes that service as a separate
diagnostic so an App build outage is visible without coupling its recovery to
other queues.

App runtime reconciliation has separate `apps.runtime` tracing and bounded
poll, claim, completion, duration, retry, error, in-flight, cleanup, and orphan
metrics. `APPS_RUNTIME_NETWORK_NAME`, `APPS_RUNTIME_POLL_INTERVAL`,
`APPS_RUNTIME_LEASE_AGE`, `APPS_RUNTIME_ACTION_TIMEOUT`, and
`APPS_RUNTIME_IMAGE_IMPORT_TIMEOUT` set validated runtime bounds. The
recoverable Docker image cache has validated max/target/interval settings:
20 GiB, 16 GiB, and 15 minutes by default. Docker list output remains byte
bounded, while valid lifetime inventory count is not capped. Unique image IDs
are inspected in batches of 128, managed container IDs in batches of 128, and
deployment protection candidates in PostgreSQL batches of 256. Each sweep
removes at most four safe tags in deterministic UUIDv7 order. It protects
selected deployments, verified managed container references, and live runtime
leases; malformed ownership or truncated output fails closed. After successful
removals with pressure remaining, the next sweep is scheduled one minute later;
otherwise the normal interval or bounded failure backoff applies. Cache
maintenance does not change App desired state or fail unrelated Apps.
Docker daemon or bridge ownership errors do not terminate unrelated worker
loops.

Worker metrics include `stealth_apps_worker_runtime_image_cache_bytes`,
`stealth_apps_worker_runtime_image_cache_limit_bytes`,
`stealth_apps_worker_runtime_image_cache_inventory_available`,
`stealth_apps_worker_runtime_image_cache_pressure`,
`stealth_apps_worker_runtime_image_gc_total{result=...}`,
`stealth_apps_worker_runtime_image_gc_reclaimed_bytes_total`, and
`stealth_apps_worker_runtime_process_exits_total{reason=...}`. GC results and
process-exit reasons use fixed vocabularies; no tenant or Docker IDs are
Prometheus labels. Structured runtime exit logs may include the App ID and
generation, but do not include container environment values or Docker stderr.
The cache byte and reclaimed-byte values are estimates: Docker reports image
size per image ID and shared layers across different IDs may be counted more
than once. They do not measure exact host filesystem space recovered.

The runtime cache contains imported Docker tags only. Persistent source and OCI
deployment artifacts remain in Stealth storage and the App artifact quota;
runtime image GC never deletes them. Reconciliation verifies and imports an
evicted selected deployment from its persisted OCI archive. Stealth never runs
global Docker prune commands. Cleanup history is bounded by retaining
completed rows for 14 days and terminal failures for 90 days, pruning at most
100 terminal rows per hourly pass. Runtime retries use exponential backoff
capped at one minute; orphan inventory backs off to one hour and runtime-image
GC failures to 24 hours, resetting after successful maintenance. Health probes
follow the WorkloadSpec's bounded 5–300 second cadence. Cleanup stops after 20
attempts and ownership conflicts are terminal.

When an inspected App container is stopped and restarted in place, the worker
fences a durable health reset before `docker start`: the old probe identity,
checked time, failure count, and route address are cleared, then the configured
initial delay starts again. It also rotates the persisted runtime routing
identity and renames the stopped container before starting it. The old Docker
DNS name no longer resolves to the restarted process, so a stale Traefik file
fails closed even before the next route snapshot. The runtime lease and
desired-generation check protect the reset, and only a fresh probe for the
restarted process can restore route eligibility.

## Security assumptions

The BuildKit service is pinned to an exact rootless release and an immutable
image digest. It joins only the dedicated `app_build` network shared with the
worker. The official rootless image requires the Compose `seccomp=unconfined`,
`apparmor=unconfined`, and `systempaths=unconfined` exceptions for nested user
and mount namespaces. On Ubuntu hosts with
`apparmor_restrict_unprivileged_userns=1`, Stealth installs and loads a named
AppArmor profile in the same unconfined mode with only the `userns` permission
added. The service remains non-privileged and runs as UID/GID 1000, with no
host namespaces, Docker socket, backend networks, host ports, or Stealth
storage/staging mounts. A private tmpfs holds rootlesskit's transient state
while the container root filesystem stays read-only. Its only persistent
volume is bounded, disposable BuildKit cache. Dockerfile execution can fetch
ordinary base images and dependencies over outbound Internet, but cannot
resolve backend services by Compose network name.

The TCP control API at `tcp://buildkit:1234` requires mutual TLS. BuildKit
requires a client certificate signed by the installation CA, and the worker
verifies the `buildkit` server SAN using that CA. The daemon uses a dedicated
server identity; the worker and BuildKit healthcheck use separate client-only
identities. `Ready()` and `Build()` both pass all three `buildctl` TLS file
flags and fail closed when any path is missing. The healthcheck also
authenticates; it does not use a plaintext local endpoint.

The host installer generates an installation-local P-256 PKI atomically and
preserves a valid bundle on routine updates. Host private keys are mode `0600`
under mode-`0700` directories. Networkless, one-shot Compose initializers
receive only the source files for one role and copy the identity into a
service-specific read-only volume with a mode-`0400` key. The worker volume
contains the CA certificate and worker identity. The BuildKit volume contains
the CA certificate, server identity, and healthcheck identity. The API and
tenant build steps receive no private key, and the BuildKit daemon never gets
the worker key. CA private material remains host-only; the retained CA key
allows controlled renewal, while leaf certificates renew 30 days before
expiry. Back up `private/buildkit-mtls` with installation control-plane
state. Completed OCI artifacts remain valid if the BuildKit identity is lost.

The worker sends only a validated source context and explicit build request to
BuildKit. It does not forward SSH agents, secrets, arbitrary build arguments,
tenant-selected Dockerfile frontends, or insecure entitlements. The process
uses a minimal environment; platform/database/provider credentials are not
passed to BuildKit. Output becomes authoritative only after BuildKit metadata
and the exported OCI layout are verified and the archive is durably published.
The Dockerfile build is not promised to be bit-for-bit reproducible; the
immutable result is the exact digest and archive captured for that deployment.

Persistent App containers are created only by the trusted worker through Moby.
The API, Console, BuildKit, and Traefik have no Docker socket. The owned App
bridge is joined dynamically by only the validated worker and hardened Traefik
peers; API, Console, BuildKit, databases, Redis, ClickHouse, and telemetry
services remain unattached. App containers publish no host ports and attach
only to that bridge. This is a shared user-defined bridge: Apps can use Docker
DNS/container names and connect to ports on other App containers. The worker
and Traefik container names are also bridge peers. Traefik listens on 8080 and
health ping on 8081; its public Host-routed API/Console services are reachable
indirectly through Traefik. The worker's port 9091 exposes `/healthz` and
`/version`, while `/metrics` requires the configured metrics token. The bridge
is `Internal=false`, so outbound App traffic follows Docker bridge NAT and the
host firewall. This east-west/egress limitation is documented and deferred to
Phase C1; there is no per-App network isolation. A container uses a read-only
root, drops all Linux capabilities, enables
`no-new-privileges`, and receives bounded CPU, memory/swap, PID, tmpfs, log,
and ulimit settings. No Stealth backend or provider environment variables,
storage mounts, Docker socket, build staging, BuildKit credentials, Cloudflare
import state, or Traefik state are passed into the App container. The selected
OCI image's own user, environment, entrypoint, and default command remain part
of its immutable configuration; WorkloadSpec command and working-directory
overrides are applied separately.

The runtime verifies the persisted archive size and checksum, OCI manifest,
platform, config digest, root filesystem diff IDs, and unsupported volume
declarations before Moby import. Container ownership uses only Stealth-added
Docker labels plus the persisted incarnation-specific name; OCI image labels
are not trusted.
`stealth doctor` only inspects network identity and does not create a network
or start an App. Runtime `running` confirms the current expected process;
`healthy` confirms the configured TCP or HTTP probe converged for the current
generation, selected deployment, and container. Only an enabled App with a
ready selected deployment, matching desired/observed generations, current
runtime identity, and healthy probes is eligible for its platform route.
Runtime stdout/stderr logs are available through the project-scoped API and
follow telemetry retention. App environment values are write-only through the
API and Console and stored as ciphertext in PostgreSQL with the dedicated
`APPS_SECRET_KEY`. The trusted worker decrypts them only for container creation
and does not pass them to BuildKit. Docker retains the effective environment in
container configuration while the container exists; a host operator with
Docker access can inspect it. gVisor and per-App network isolation remain
unimplemented.
App container outbound access follows Docker's bridge and host firewall policy.

The Go API owns sessions and uses HttpOnly cookies with `Secure` and
`SameSite=None` for explicitly configured cross-origin HTTPS console requests;
local HTTP uses `SameSite=Lax`. CORS is an explicit allowlist. Because the
console can be a separate origin, CSRF protection relies on the same-origin
deployment recommendation, explicit CORS/Origin checks, and the cookie
SameSite policy; a deployment that permits arbitrary origins is not supported.

Webhook and monitor/Git fetch clients disable proxy use and redirects, resolve
targets before dialing, and apply an explicit public-destination policy that
blocks private, shared, loopback, link-local, multicast, unspecified,
documentation, benchmarking, reserved, and other special-use ranges. Every
resolved DNS answer must pass that policy, and the dial-time resolution uses
the caller's context so a DNS deadline or cancellation is honored. Response
bodies remain bounded; operators must still treat outbound delivery as a
network policy boundary.

## Remaining production gaps

These are intentional boundaries, not hidden reliability claims:

- App health convergence, health-gated public routing, runtime stdout/stderr
  log viewing through the existing Collector/ClickHouse pipeline, and bounded
  Stealth-owned runtime image-cache maintenance are implemented. App
  environment values use dedicated-key authenticated encryption at rest and
  are injected by the runtime worker; gVisor isolation and per-App network
  isolation remain deferred to Phase C1. The current Moby `running`
  status confirms process liveness only; `healthy` confirms the configured
  probe, and route eligibility additionally requires the current enabled
  desired generation and inspected runtime identity. Values are limited to
  65,536 bytes each and 512 KiB total per App. NUL and CR/LF are rejected
  because Docker `--env-file` is line-based; multiline values are unsupported.

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
