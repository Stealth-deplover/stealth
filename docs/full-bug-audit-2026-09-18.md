# Repository-wide bug audit — 2026-09-18

## Scope and audit basis

This is a read-only audit of the current Stealth repository. The audited
revision is:

| Item | Value |
| --- | --- |
| `origin/main` at audit start | `0ba6dc29ce3d9405bed9242dcea9ec453aec30c0` |
| Audited branch | `feat/admin-observability-foundation` |
| Audited head | `81c7e0ad1d865c192599c8776d9e0e11efe86e93` |
| Original audit context | `010250f5a3c6f1407aeabb6335b189d9b3b55822` |
| Audit date | 2026-09-18 UTC |

The review traced production Go, SQL migrations, Compose/Collector
configuration, Console TypeScript/React, CI/release scripts, and the existing
tests. It used function and data-flow behavior rather than relying on stale
line numbers. No Go, TypeScript, SQL, YAML, Compose, workflow, or test source
was changed for this audit. This document is the only audit artifact added.

Severity is practical impact, not exploitability alone:

- **High** — can expose secrets, broaden host authority, or make a core admin
  operation fail.
- **Medium** — correctness, availability, or observability degradation under a
  realistic path.
- **Low** — compatibility or contract edge case with a bounded impact.

## Executive summary

The current branch has substantial real observability plumbing: authenticated
admin routes, a ClickHouse-backed `TelemetryStore`, a private Collector
pipeline, bounded query APIs, durable control-plane records, and no fabricated
telemetry. The historical six-finding remediation is present on this head;
the status is recorded below rather than copied from the historical report.

The current review found these confirmed issues or gaps. The report was
re-run after the telemetry hardening commits `c3f2a07` and `81c7e0a`; the
Docker metrics socket finding from the earlier revision is explicitly marked
resolved below rather than carried forward as a current defect.

1. Telemetry secrets can be retained in raw ClickHouse rows while only the API
   response is redacted.
2. The main Collector can read the entire host filesystem because Compose
   mounts `/` at `/hostfs`.
3. The Collector healthcheck validates configuration rather than the live
   health endpoint, so dependent services can observe a false-green state.
4. ClickHouse receives histogram/summary metric tables that every current
   admin metric/source/alert query ignores.
5. Deleting an alert rule can fail after it has produced a notification
   delivery.
6. Monitor alert rules are not referentially or semantically validated, so
   orphaned and inert/wrong-kind rules can be stored.
7. Live-tail polling can rescan large ranges and re-emit old rows after its
   de-duplication map is reset.
8. Docker logs and container metrics lose service/container attribution.
9. The monitor SSRF allowlist does not explicitly reject all special-use
   address ranges.
10. Monitor DNS validation can outlive the configured probe timeout.
11. HTTP monitors reject legitimate query-string URLs, and DNS expected-value
    matching is subset-only; both are contract correctness issues.
12. Custom admin time ranges revert to the default when browser storage is
    unavailable.
13. Admin mutations have no instance-level realtime stream, so a second admin
    session is refresh-dependent.
14. The trace duration query accepts `NaN`, which is not a valid bounded
    duration and is later converted to an unsigned integer.
15. The database schema permits `backup_failure`/`job_failure` alert kinds
    that the API and evaluator cannot create or process.

The first two findings are security-boundary issues and should be addressed
before treating the observability stack as hardened. The alert deletion and
live-tail findings are the most direct production correctness/availability
risks. No source fix was applied in this audit.

## Confirmed findings

### AUD-01 — Raw telemetry can retain credentials before API redaction

**Status:** CONFIRMED STATIC + TEST EVIDENCE  
**Severity:** High  
**Area:** telemetry privacy, ClickHouse retention and backups

**Evidence**

- `telemetry/otel-collector.yaml:69-92` accepts Docker log bodies and parses
  them into attributes without a secret-redaction or allowlist processor.
- `internal/telemetry/store.go:313-420` applies `redactText` and
  `redactAttributes` after rows have been read from ClickHouse.
- `internal/telemetry/store_integration_test.go:60-71` inserts
  `password=super-secret` and `token=super-secret` as raw log/span values.
- `internal/telemetry/store_integration_test.go:90-103` expects those values to
  be redacted only in the query result. This proves the intended current
  behavior is storage of the original secret followed by read-time masking.
- `internal/telemetry/store.go:529-551` uses a regex that recognizes
  unquoted `password=...`-style text, but does not reliably match quoted JSON
  keys such as `{"password":"secret"}` and stops at whitespace in a value.
  Even the API-side layer therefore does not provide a complete guarantee for
  structured or multi-word payloads.

**Minimal reproduction**

1. Send an OTLP log or container log containing a password, token, or
   authorization value.
2. Allow the Collector to export it to ClickHouse.
3. Inspect the ClickHouse row as an operator, during backup, or during
   retention/incident handling.
4. The original value is present even though the Stealth API response is
   masked.

**Impact**

Anyone with ClickHouse, volume, backup, or Collector access can obtain values
that the browser does not receive. The regex redactor is useful defense in
depth, but it cannot undo raw retention and cannot guarantee coverage for
arbitrary structured fields or future log formats. Its current JSON and
multi-word blind spots make the read-time guarantee weaker as well.

**Recommended remediation**

Redact or allowlist sensitive fields before export, preferably in the
application/Collector boundary, and retain API-side redaction as a second
layer. Add a runtime integration assertion that the persisted row itself is
sanitized. Avoid logging secret-bearing request headers and bodies entirely.

### AUD-02 — Main Collector has an unnecessary full-host filesystem mount

**Status:** CONFIRMED STATIC SECURITY ISSUE  
**Severity:** High  
**Area:** production Compose privilege boundary

**Evidence**

`compose.production.yaml:138-146` mounts:

```text
/:/hostfs:ro
/var/lib/docker/containers:/hostfs/var/lib/docker/containers:ro
/dev/null:/hostfs/var/run/docker.sock:ro
```

The Collector configuration uses `/hostfs` for host metrics and Docker log
files (`telemetry/otel-collector.yaml:22,69-72`). The container is read-only
and drops capabilities, but a read-only root mount still allows a compromised
Collector process to read unrelated host files, deployment configuration, and
potentially credentials.

**Minimal reproduction**

1. Compromise the Collector process or load a malicious Collector extension.
2. Read `/hostfs/etc`, `/hostfs/opt`, or any other host path visible through
   the root bind.
3. The process can read data outside the hostmetrics and Docker-log inputs it
   actually needs.

**Impact**

A component intended to receive telemetry has broad host confidentiality
authority. Masking the Docker socket prevents this particular container from
using that socket, but it does not prevent host-file disclosure.

**Recommended remediation**

Replace the root bind with the smallest required mounts for the selected
hostmetrics receivers and log paths. Revalidate host filesystem, process,
network, and Docker log collection after narrowing the mounts. Do not add a
second Docker socket to compensate.

### AUD-03 — Docker metrics authority is reduced, but the proxy remains a high-value boundary

**Status:** ALREADY FIXED ON CURRENT HEAD (residual review note)  
**Severity:** High before remediation; no longer counted as an open finding  
**Area:** Docker authority isolation

The previous revision mounted the raw Docker socket into the stats Collector.
That is no longer true on this head:

- `compose.production.yaml:181-220` gives the socket only to
  `telemetry-docker-proxy`; `telemetry-docker` has no socket, capability, or
  Docker group.
- `internal/dockermetricsproxy/proxy.go` permits only GET/HEAD requests for
  `/_ping`, `/version`, `/events`, `/containers/json`, container inspect, and
  container stats, with endpoint-specific query allowlists.
- Inspect and container-list responses are sanitized before forwarding.
- The proxy has no published port and the static check in
  `scripts/telemetry-security-test.sh` rejects public exposure or a socket on
  the metrics Collector.

The raw socket is still a high-value host boundary because the proxy process
can read it, but the current code has an explicit policy boundary rather than
claiming that `:ro` is sufficient. A real Docker-daemon integration test is
still required to prove the allowlist and receiver behavior; it was not
available in this environment.

### AUD-04 — Alert-rule deletion can fail after notification enqueue

**Status:** CONFIRMED BY SQL/TRANSACTION TRACE  
**Severity:** High  
**Area:** admin alert lifecycle and notification durability

**Evidence**

- `internal/repository/admin_alert_evaluator.go:114-118` inserts an
  `admin_alert_events` row and then enqueues an
  `admin_notification_deliveries` row in the same transaction.
- `internal/migrate/migrations/000043_admin_observability.up.sql:119-133`
  defines `admin_notification_deliveries.alert_event_id` with
  `ON DELETE SET NULL`.
- `internal/migrate/migrations/000046_admin_notification_tests.up.sql:7-13`
  requires `alert_event_id IS NOT NULL OR test_message IS NOT NULL`.
- `internal/repository/admin_controls.go:228-250` deletes the alert rule;
  the event has `ON DELETE CASCADE` from the rule.

**Minimal reproduction**

1. Enable a notification channel and an alert rule.
2. Let the rule fire or resolve so a delivery exists with a non-null
   `alert_event_id` and a null `test_message`.
3. Delete the alert rule through the admin API.
4. Cascading event deletion sets the delivery's `alert_event_id` to NULL.
5. The source check constraint rejects the resulting delivery row, so the
   delete transaction rolls back.

**Impact**

An alert rule becomes undeletable after it has generated a normal notification
delivery. The UI can only surface a generic mutation failure, while the owner
cannot remove the rule that is causing the problem.

**Recommended remediation**

Choose an explicit history policy: retain an immutable notification source
snapshot/tombstone, cascade/delete delivery records intentionally, or move
delivery history to a relation that does not require a live alert event. Add a
PostgreSQL integration test for delete-after-fire and delete-after-resolve.

### AUD-05 — Monitor alert conditions can be orphaned or semantically invalid

**Status:** CONFIRMED STATIC  
**Severity:** Medium  
**Area:** monitor/alert configuration correctness

**Evidence**

- `internal/repository/admin_controls.go:346-380` validates only that
  `monitor_id` is a UUID for `monitor_failure`, `heartbeat_failure`, and
  `certificate_expiry`; it does not verify that the monitor exists or that its
  kind matches the alert kind.
- `internal/repository/admin_alert_evaluator.go:123-176` selects by JSON
  `monitor_id`. A `certificate_expiry` rule attached to an HTTP, TCP, or
  heartbeat monitor falls through to the generic `!input.Success` trigger for
  failures, rather than certificate-expiry semantics.
- `certificateExpiryTriggered` returns false for `days <= 0` at lines
  193-208, while validation accepts any JSON number at lines 369-371.
- Monitor deletion has no relational foreign key or visible rule cleanup
  path, leaving rules that will never evaluate.

**Minimal reproduction**

1. POST an alert rule with a real UUID that is not a monitor, or with a
   monitor ID of the wrong kind.
2. POST a certificate-expiry rule with `days: 0` or a negative number.
3. The API accepts the rule; evaluation either never triggers or uses the
   wrong monitor-failure behavior.
4. Delete the monitor and observe that its alert rule remains persisted.

**Impact**

The control room can show enabled rules that are inert or fire for the wrong
condition. This is especially dangerous for certificate coverage because a
rule can appear configured while never producing an expiry alert.

**Recommended remediation**

Validate monitor existence and kind in the same transaction as rule creation
and update; reject non-positive expiry thresholds; and disable/delete or
reconcile rules when their monitor is removed. Keep the evaluator's single
canonical monitor-status path.

### AUD-06 — Live-tail can rescan large ranges and duplicate old rows

**Status:** CONFIRMED STATIC  
**Severity:** Medium  
**Area:** ClickHouse load and Console log stream correctness

**Evidence**

`internal/httpapi/server_admin.go:242-317` implements the tail loop by:

- querying from `cursor.Add(-time.Nanosecond)` to `now` every two seconds;
- using a de-duplication key made from timestamp, trace ID, span ID, service,
  and body; and
- resetting `seen` when it exceeds `limit*8`.

The cursor is only advanced when an item timestamp is strictly later. Rows
sharing a timestamp are therefore repeatedly scanned, and resetting `seen`
allows previously emitted rows to be emitted again. A 30-day selected range
causes each two-second poll to rescan that full range even when no new row has
arrived.

**Minimal reproduction**

1. Open `/v1/admin/telemetry/logs/tail` with a 30-day range and a small
   limit.
2. Insert enough log rows to exceed `limit*8`, or create many rows with the
   same timestamp.
3. The server resets its map and emits rows already sent to the browser.
4. With no new rows, inspect ClickHouse query activity: the same historical
   window is queried every poll.

**Impact**

Owners see duplicates in the live stream, and one open tail per admin can
create repeated expensive ClickHouse scans. The frontend's bounded de-dup map
(`console/src/api/queries/observability.ts`) cannot guarantee correctness once
the server has reset its own history.

**Recommended remediation**

Use a stable server cursor containing timestamp plus a unique/tie-breaker key,
query strictly after that cursor, and use a short tail lookback independent of
the explorer's historical range. If a bounded replay window is required,
explicitly mark it as replay and never reset de-duplication in a way that
re-emits old rows.

### AUD-07 — Container telemetry loses service/container identity

**Status:** CONFIRMED DATA-QUALITY GAP  
**Severity:** Medium  
**Area:** infrastructure and source attribution

**Evidence**

- `telemetry/otel-collector.yaml:69-105` parses Docker JSON logs but does not
  extract the container ID/name/service from the file path or Docker metadata;
  the resource processor inserts `service.name=stealth-host`.
- `telemetry/docker-stats.yaml:15-22` upserts the same
  `service.name=stealth-container-metrics` for every Docker stats resource.
- `console/src/features/admin/admin-infrastructure-view.tsx:54-70` groups
  infrastructure samples by service and metric.

**Minimal reproduction**

1. Run two production containers with distinct resource usage or log output.
2. Query admin infrastructure/log sources.
3. Logs are attributed to the host and metrics are attributed to one generic
   container-metrics service instead of the individual Compose service or
   container.

**Impact**

The owner cannot reliably answer which service consumes CPU/RAM or emitted a
log. Service filtering, incident correlation, and source pages lose the most
useful dimension of the data.

**Recommended remediation**

Preserve `container.id`, `container.name`, image, Compose service, and safe
deployment labels from the Docker receiver/path. Do not overwrite an existing
service identity with a global value; use a fallback only when no identity is
available.

### AUD-08 — Monitor SSRF filtering omits special-use address ranges

**Status:** CONFIRMED STATIC SECURITY GAP  
**Severity:** Medium  
**Area:** HTTP/TCP monitor egress boundary

**Evidence**

`internal/monitoring/checker.go:430-455` accepts an address when it is not
loopback, private, link-local, unspecified, or multicast. The check does not
explicitly reject several non-public special-use ranges, including CGNAT
`100.64.0.0/10` and benchmark `198.18.0.0/15`. `net.IP.IsPrivate` is not a
complete “globally routable” policy for this purpose.

**Minimal reproduction**

1. Configure a monitor hostname resolving to a reachable address in one of the
   omitted special-use ranges.
2. Pass the monitor through `resolvePublicHost` and `safeDialContext`.
3. The current predicates can classify the address as public and permit the
   worker's outbound connection.

**Impact**

The worker's owner-configured outbound boundary is weaker than the intended
public-only policy. Reachability depends on the host network, but the allowlist
should not rely on that accident.

**Recommended remediation**

Use an explicit deny policy for IANA special-use/reserved ranges for both IPv4
and IPv6, then add table-driven tests for private, CGNAT, benchmark, link-local,
documentation, multicast, loopback, and global addresses.

### AUD-09 — DNS validation uses an uncancellable background context

**Status:** CONFIRMED STATIC AVAILABILITY GAP  
**Severity:** Medium  
**Area:** monitor worker timeout and notification URL validation

**Evidence**

- `internal/monitoring/checker.go:178-215` creates a bounded `probeContext`
  for an HTTP monitor.
- `validatePublicURL` at lines 354-362 calls
  `resolvePublicHost(context.Background(), ...)` for the initial URL and each
  redirect.
- `ValidatePublicHTTPSURL` at lines 364-375 does the same for notification
  URLs.
- The resolver therefore does not inherit the monitor context or the
  notification request context.

**Minimal reproduction**

1. Configure a monitor or notification endpoint whose resolver is slow or
   unavailable.
2. Set a short monitor timeout.
3. The network request context expires, but DNS validation can remain blocked
   independently of that timeout and hold a worker slot.

**Impact**

Repeated slow DNS validations can reduce worker concurrency and make the
configured timeout misleading. A notification worker can be affected by the
same pattern.

**Recommended remediation**

Pass the active context into URL validation and resolver calls, with a small
independent DNS budget if necessary. Add a test using a blocking resolver or a
context-aware resolver seam.

### AUD-10 — HTTP query-string URLs are rejected and DNS matches only a subset

**Status:** CONFIRMED CONTRACT ISSUES  
**Severity:** Low/Medium  
**Area:** monitoring target semantics

**Evidence**

- `internal/monitoring/checker.go:123-127` and `354-356` reject any
  `RawQuery` for HTTP monitors. Query parameters are normal parts of an HTTP
  endpoint, and `ValidatePublicHTTPSURL` intentionally permits them for
  notification providers at lines 364-374.
- `sameValues` at lines 489-500 verifies that every expected DNS value appears
  in the actual set but never rejects additional actual values.

**Minimal reproduction**

1. Create an HTTP monitor for
   `https://example.test/health?region=eu`; validation rejects it despite a
   valid absolute HTTP(S) target.
2. Configure DNS expected values `["203.0.113.10"]` while the response is
   `["203.0.113.10", "203.0.113.11"]`; `sameValues` returns true.

**Impact**

Legitimate parameterized health endpoints cannot be monitored. DNS monitoring
can report healthy when the response differs from an exact expected set. If
subset semantics are intended, the API contract and UI should say so clearly;
the current name “expected values” is ambiguous.

**Recommended remediation**

Allow query strings while retaining userinfo/fragment and SSRF protections,
with redaction of query secrets in diagnostics. Define DNS exact-vs-subset
semantics explicitly and test both directions.

### AUD-11 — Custom admin time range depends on writable browser storage

**Status:** CONFIRMED FRONTEND EDGE BUG  
**Severity:** Medium  
**Area:** Console admin time-range synchronization

**Evidence**

- `console/src/features/admin/admin-time-range.tsx:44-67` reads the selected
  range from `localStorage` and falls back to `1h` on any storage error.
- `applyCustom` at lines 191-215 catches `localStorage.setItem` failures but
  still dispatches the preference event and calls `onRangeChange("custom")`.
- `useAdminTimeRange` has no in-memory selected-range state; its next store
  read therefore returns `1h` when storage remains unavailable.

**Minimal reproduction**

1. Run the admin Console in a private/restricted context or monkey-patch
   `localStorage.getItem/setItem` to throw `SecurityError`.
2. Enter a valid custom range and click Apply.
3. The hook cannot retain the `custom` key and reverts to the one-hour moving
   range, even though the component comment says the range remains valid for
   the current request lifecycle.

**Impact**

Admin charts, logs, traces, and infrastructure queries can silently use a
different range from the one the owner selected.

**Recommended remediation**

Keep the preference in React state and use storage only for persistence. A
storage failure should not change the current in-memory query; it may only
prevent persistence across reloads.

### AUD-12 — Admin control-room mutations do not have cross-session realtime

**Status:** CONFIRMED CURRENT CAPABILITY GAP  
**Severity:** Medium  
**Area:** alerts, incidents, monitors, status and dashboard admin views

**Evidence**

- `internal/realtime/event.go` publishes to project-scoped channels and its
  event allowlist contains project resource events, not instance-admin alert,
  monitor, incident, or status-page events.
- `console/src/realtime/invalidation.ts` is a project event adapter and has no
  admin event mapping.
- `console/src/api/queries/observability.ts:51-116` opens an SSE only for
  telemetry log tail; admin lists/details use normal TanStack Query fetching.
- Local mutations in `console/src/api/mutations/admin.ts` invalidate the
  current browser's queries, but do not update another owner/admin session.

**Minimal reproduction**

1. Open the same admin alert, incident, or monitor view in two owner sessions.
2. Change the object in session A.
3. Session B remains stale until its configured refetch interval, navigation,
   or manual refresh.

**Impact**

The owner control room is not consistently live across administrators. This is
especially visible during an incident when one operator changes status or
acknowledges an alert while another operator is watching the same page.

**Recommended remediation**

Add an authenticated instance-admin realtime channel with strict instance
authorization and a canonical admin event-to-query mapping. Keep high-volume
telemetry on the existing tail/aggregate endpoints rather than placing every
telemetry row on the application event bus.

### AUD-13 — Collector healthchecks can be green while the running pipeline is unhealthy

**Status:** CONFIRMED STATIC OPERATIONAL GAP  
**Severity:** Medium  
**Area:** Compose startup and dependency health

**Evidence**

- `telemetry/otel-collector.yaml:5-7` enables the Collector
  `health_check` extension on `0.0.0.0:13133`.
- `compose.production.yaml:159-166` instead runs
  `[/otelcol-contrib, validate, --config=...]` as the container healthcheck.
- `telemetry-docker` depends on that status with
  `condition: service_healthy`.

`validate` proves that the configuration can be parsed; it does not probe the
live health endpoint, verify that the exporter is accepting batches, or prove
that the receiver pipelines are running. The API's admin overview separately
uses the health endpoint, so the repository contains two different meanings
of “healthy”.

**Minimal reproduction**

1. Start the Collector with a syntactically valid configuration but an
   unavailable ClickHouse exporter or a receiver that cannot bind/connect.
2. The `validate` command remains successful even though telemetry is not
   being delivered.
3. Compose may start dependent services while the Collector is effectively
   degraded.

**Recommended remediation**

Keep the scratch-compatible exec-form check, but make the health decision
probe the enabled endpoint using a supported image/topology mechanism (or add
a tiny purpose-built probe). Add a runtime Compose test that distinguishes a
valid config from a live, exporting Collector.

### AUD-14 — Histogram, summary, and exponential-histogram telemetry is stored but not queryable

**Status:** CONFIRMED STATIC DATA-COVERAGE GAP  
**Severity:** Medium  
**Area:** ClickHouse telemetry API, infrastructure views, and alerts

**Evidence**

- `telemetry/otel-collector.yaml:130-149` configures the ClickHouse exporter
  to write `summary`, `histogram`, and `exponential_histogram` tables.
- `internal/telemetry/store.go:269-284` reads only
  `otel_metrics_gauge` and `otel_metrics_sum`.
- `internal/telemetry/store.go:286-311` omits the other metric tables from
  Sources, while `internal/telemetry/explore.go:227-290` omits them from
  metric alert queries and `internal/telemetry/explore.go:435-460` omits them
  from infrastructure queries.

**Minimal reproduction**

1. Emit an OTLP histogram or summary metric.
2. Confirm the Collector exporter writes the corresponding ClickHouse table.
3. Query `/v1/admin/telemetry/metrics`, Sources, infrastructure, or a metric
   alert for that metric.
4. The data is absent even though ingestion succeeded.

**Impact**

Latency and duration instruments are commonly histograms. The control room
can therefore report an empty or incomplete view while the backend contains
the signal, and alert rules can evaluate “no samples” instead of the actual
distribution.

**Recommended remediation**

Define bounded domain projections for the supported histogram/summary kinds
(for example count/sum and approved quantiles), or explicitly reject and
document unsupported kinds at ingestion. Cover the chosen semantics with a
ClickHouse integration test through the Admin API.

### AUD-15 — `NaN` is accepted as a trace duration filter

**Status:** CONFIRMED STATIC INPUT-VALIDATION BUG  
**Severity:** Low  
**Area:** Admin trace query boundary

**Evidence**

`internal/httpapi/server_admin.go:642-651` accepts a parsed float when it is
not below the minimum and not above the maximum. `strconv.ParseFloat("NaN",
64)` returns a float without an error, and both comparisons with `NaN` are
false. `internal/telemetry/store.go:356-361` then converts the value to
`uint64` for a ClickHouse `UInt64` parameter.

**Minimal reproduction**

Request `/v1/admin/telemetry/traces?min_duration_ms=NaN`. The HTTP boundary
does not return the documented validation error; the subsequent numeric
conversion is not a meaningful duration and can produce a driver error or an
implementation-dependent unsigned value.

**Recommended remediation**

Require `math.IsNaN(value) == false` and `math.IsInf(value, 0) == false` at
the HTTP and store boundaries, then add tests for both non-finite values.

### Related contract inconsistency — unsupported alert kinds are still allowed by SQL

`internal/migrate/migrations/000043_admin_observability.up.sql:79` allows
`backup_failure` and `job_failure`, while `validAdminAlertKind` in
`internal/repository/admin_controls.go:383-390`, the OpenAPI enum, and the
telemetry evaluator do not support them. The validator contains dead
`backup_failure`/`job_failure` branches (`admin_controls.go:373-376`). Current
HTTP creation rejects these kinds, so this is not an immediate public API
acceptance bug, but old/manual rows in those valid database states cannot be
evaluated consistently. Align the database constraint, API contract, and
worker implementation in one follow-up.

## Verified controls with no current finding

The following paths were inspected and did not reproduce the earlier defect
or a new correctness failure at this head:

- Instance-admin routes use `requireInstanceAdmin`; the owner/member/anonymous
  authorization integration test covers the boundary.
- The host installer observer now handles installer errors explicitly, reloads
  state, stops after a non-terminal failure, and has regression tests for
  persisted failure, run-ID mismatch, lock conflict, and single invocation.
- Docker JSON timestamp parsing, stdout/stderr severity defaults, structured
  application levels, and malformed-line preservation have static and pinned
  Collector smoke coverage. The live smoke was environment-blocked as noted
  below.
- The setup service and public production services do not mount the Docker
  socket; the raw socket is isolated behind the current internal metrics
  proxy.
- Cursor quoted-boundary handling, durable artifact cleanup, project realtime
  mappings, queued Agent status, release bootstrap pinning, and monotonic
  browser setup checklist state are covered in the historical table below.

## Historical six-finding re-verification

The previous audit was not blindly reapplied. Each item was traced against the
current implementation:

| Historical finding | Current status | Current evidence |
| --- | --- | --- |
| Cursor pagination stripped quoted text boundaries | **ALREADY FIXED** | `internal/repository/databases.go:118-160` uses a typed JSON/base64 cursor with `UseNumber`; `internal/repository/database_rows.go:211-265` uses value-plus-ID tuple boundaries. `internal/httpapi/server_databases_cursor_test.go` and `internal/repository/databases_cursor_test.go` cover quoted, escaped, Unicode, empty, numeric, and scalar cases. |
| Metadata deletion could orphan physical artifacts | **FIXED for deletion; publication residual not reproduced on current head** | Delete paths enqueue durable cleanup in `internal/repository/artifact_cleanup.go` and the worker retries/reclaims leases. Current upload/build paths use `ReserveArtifactPublishCleanup` and consume the reservation transactionally (`server_*` upload handlers, function/site workers, and repository finalize paths). |
| Realtime fanout/subscriptions/cache invalidation incomplete | **FIXED for the previously audited project domains** | `internal/realtime/event.go` includes row, storage, messaging, webhook, Agent, schema, and deployment domains; `console/src/realtime/invalidation.ts` and `console/src/api/cache-coherence.ts` map them to scoped keys. Current admin cross-session realtime remains the separate AUD-12 gap. |
| Queued Agent runs left parent status stale | **FIXED** | `CreateAgentRun` refreshes parent status in the same transaction; `refreshAgentStatusTx` derives running → queued/active → idle. The recovery path also emits `agent.run.queued` (`internal/repository/agent_runs.go:573-623`). |
| Release smoke fetched `bootstrap.sh` from `HEAD` | **FIXED** | `.github/workflows/release.yml:68-71` checks out the release revision; `scripts/release_workflow_test.sh` rejects `/HEAD/` and requires the release version. |
| Browser setup checklist lost completed steps | **FIXED** | `installationStepState` uses `setupSteps` ordering and lifecycle aliases in `console/src/features/auth/browser-setup-model.ts:65-122`; tests cover forward progression, completion/handoff, failure, fresh request, and cleanup. |

The older `docs/full-bug-audit-2026-09-17.md` still contains intermediate
residual wording in its historical sections. The current code and the later
remediation notes were rechecked above; the historical text should not be read
as the status of this head.

## Material scope gaps (not represented as fake completion)

These are not counted as newly discovered defects because the repository's
current architecture document explicitly says they are not implemented. They
must remain visible before calling the larger observability specification
complete:

| Capability | Current evidence | Risk/impact |
| --- | --- | --- |
| Synthetic browser runner | `internal/monitoring/checker.go` returns `synthetic runner is not enabled`. | Synthetic checks cannot run; no browser-based availability signal exists. |
| Workload-scoped OTLP credentials | `docs/telemetry-architecture.md` lists workload credential issuance as not implemented. | External workloads cannot receive an authoritative project/deployment telemetry identity. |
| Owner-editable retention, sampling and external OTLP export | Explicitly listed as not implemented in `docs/telemetry-architecture.md`. | Operators cannot configure those controls through the admin product. |
| ClickHouse backup provider/verification | Persistent storage exists, but the same document says backup remains operator-configured. | Telemetry recovery is not proven by the control-plane backup. |
| Automatic alert-to-incident correlation and maintenance windows | Explicitly listed as not implemented. | Incident workflows remain partly manual. |
| Full advanced SQL mode | Deliberately not exposed; normal users get domain queries. | This is a product decision, not a security bug, but it does not satisfy an advanced-query requirement. |

These gaps are preferable to placeholder endpoints or fabricated charts. They
should be tracked as separate implementation work rather than silently
reported as complete.

## Preliminary validation record from the earlier audit pass

The section below records the initial pass against the preceding branch head
and is retained for traceability. It is not the authoritative current-head
result. The current-head rerun is at the end of this document.

### Passed locally

- `PATH=/tmp/stealth-go/go/bin:$PATH go test ./... -count=1` — PASS. Tests
  requiring external `TEST_DATABASE_URL` were skipped by their existing
  guards.
- `PATH=/tmp/stealth-go/go/bin:$PATH go vet ./...` — PASS.
- Repository Go formatting check — PASS.
- `git diff --check` — PASS.
- `go mod verify` — PASS.
- Console `npm test -- --run` — PASS, 35 files / 153 tests.
- Console `npm run typecheck` — PASS.
- Console `npm run lint` — PASS.
- Console `npm run format:check` — PASS.
- Console `npm run build` — PASS, including generated route/type checks and
  production page generation.
- `npm audit --omit=dev --audit-level=high` — PASS, zero high/critical
  production dependency findings.
- `./scripts/release_workflow_test.sh` — PASS.
- `./scripts/bootstrap_test.sh` — PASS.
- `./scripts/telemetry-security-test.sh` — PASS for the static checks it
  contains.
- `./scripts/setup-security-test.sh` — PASS for the static checks it
  contains.
- `bash -n scripts/*.sh` — PASS.

### Failed or unavailable checks

- `timeout 180s npm run test:e2e` — 29 passed, 4 failed. The failures were:
  `agents.spec.ts` did not observe the transient queued-worker message;
  `critical-flow.spec.ts` used a non-exact `Next` locator that also matched
  Next.js dev tools; the function execution test did not observe transient
  `Accepted`; and the site deployment test did not observe transient `Queued`.
  These are real validation failures, but the latter three are intermediate
  state/locator timing assumptions rather than proof that the durable final
  state is wrong. They should be made deterministic before relying on this
  suite as a release gate.
- `go test -race ./...` — NOT RUN TO COMPLETION. The environment reported
  `-race requires cgo; enable cgo by setting CGO_ENABLED=1`; no compiler was
  available.
- Real PostgreSQL/Redis integration paths — NOT RUN locally because the test
  database environment was not supplied.
- Real ClickHouse/OTel integration — NOT RUN locally; Docker and ClickHouse
  are unavailable in this environment.
- Compose config/image builds — NOT RUN locally for the same Docker
  unavailability. Static security scripts passed.
- CodeQL/security scanners — NOT run as local binaries. Current GitHub CI
  checks associated with the branch were green, but that does not replace a
  local runtime test of the findings above.
- Fresh VPS E2E — NOT PERFORMED. No VPS was available, so no claim is made
  about production ClickHouse, Collector, monitor, or incident behavior.

### CI observed

The GitHub Actions runs observed for the audited head were successful:

- [PR checks run 35300895540](https://github.com/Stealth-deplover/stealth/actions/runs/35300895540)
- [push checks run 35300891150](https://github.com/Stealth-deplover/stealth/actions/runs/35300891150)

CI success does not invalidate static findings that are not covered by the
current tests, especially secret-at-rest redaction, least-privilege mounts,
alert deletion after delivery, and cross-session admin realtime behavior.

## Recommended priority

1. Remove raw secret retention at ingestion and narrow the Collector host
   filesystem authority (AUD-01 and AUD-02); retain the current restricted
   Docker proxy as a separately tested security boundary.
2. Fix alert deletion transaction semantics and monitor rule referential
   validation (AUD-04 and AUD-05).
3. Replace live-tail timestamp/de-dup polling with a stable cursor and bounded
   tail window (AUD-06).
4. Preserve container identity and harden monitor egress/DNS cancellation
   (AUD-07 through AUD-09).
5. Resolve the frontend time-range storage edge case and decide whether the
   instance-admin realtime channel is in scope for the next observability PR
   (AUD-11 and AUD-12).

No code was changed as part of this audit, no commit was created, and no merge
or PR update was performed.

**PRELIMINARY STATUS: AUDIT REPORT COMPLETE, CODE NOT CHANGED**

## Current-head revalidation record

This is the authoritative validation record for `81c7e0ad1d865c192599c8776d9e0e11efe86e93`.

### Passed

- `PATH=/tmp/stealth-go/go/bin:$PATH go vet ./...` — PASS.
- `PATH=/tmp/stealth-go/go/bin:$PATH go test ./internal/monitoring ./internal/telemetry ./internal/repository ./internal/realtime ./internal/observability ./internal/setupstate ./internal/setupinstall -count=1` — PASS.
- `gofmt -l $(git ls-files '*.go')` — no files reported.
- `git diff --check` — PASS.
- `npm run typecheck` in `console` — PASS.
- `npm run lint` in `console` — PASS.
- `npm run format:check` in `console` — PASS.
- `./scripts/release_workflow_test.sh` — PASS.
- `./scripts/telemetry-security-test.sh` — PASS for its static assertions.

### Failed or unavailable

- `PATH=/tmp/stealth-go/go/bin:$PATH go test ./... -count=1` — NOT GREEN in
  this sandbox. The affected tests require sockets or external DNS: several
  `httptest`/Unix-socket tests fail with `operation not permitted`, and the
  notification retry test cannot resolve `example.com`. The unaffected
  targeted packages above pass; this is not evidence that the whole suite is
  green.
- `npm test -- --run` — could not start under the installed Node `18.19.1`:
  the installed `rolldown` package imports `node:util.styleText`, which this
  Node version does not provide.
- `npm run build` — blocked by the repository's Next.js requirement for Node
  `>=20.9.0`; the environment has Node `18.19.1`.
- `go test -race ./...` — not completed; the environment lacks the cgo/GCC
  toolchain required by the race detector.
- `OTEL_COLLECTOR_BINARY=... ./scripts/collector-log-parser-smoke.sh` — the
  pinned 0.161.0 binary reached startup but the sandbox denied binding its
  default internal metrics listener (`operation not permitted`). This is an
  environment limitation, not a parser assertion result.
- Docker/Compose is not installed in this environment, so
  `docker compose config`, image builds, clean-volume startup, ClickHouse
  persistence/restart, the restricted Docker proxy integration, and the
  actual Collector-to-ClickHouse metric/log/trace path were not run.
- Real PostgreSQL/Redis/ClickHouse integration, CodeQL/security scanners, and
  a fresh VPS E2E were not run.

The worktree contains only the untracked report file
`docs/full-bug-audit-2026-09-18.md`; no application, test, Compose, workflow,
or configuration source was modified. No commit, push, merge, or PR update was
performed.

**CURRENT STATUS: AUDIT COMPLETE, FINDINGS REPORTED, CODE NOT CHANGED**
