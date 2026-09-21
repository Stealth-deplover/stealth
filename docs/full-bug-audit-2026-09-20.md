# Full repository bug audit

## Audit metadata

- Audit date: 2026-09-20 UTC
- Audited repository SHA: `57267dec46895c44a5e87d55581f2567f5423903`
- Audited branch: `main`
- PR #83 merge commit: `57267dec46895c44a5e87d55581f2567f5423903`
- PR #83 pre-merge head: `3ea7f229b8706ba91265e1c7393cb0de91589e53`
- Refreshed PR #84 branch: `audit/full-bug-audit-2026-09-18`
- Previous PR #84 head: `faddd8f74df9243d261afabc3eee03c851020480`
- Previous PR #84 base: `feat/admin-observability-foundation` at `81c7e0ad1d865c192599c8776d9e0e11efe86e93`
- Final PR #84 base: `main` at `57267dec46895c44a5e87d55581f2567f5423903`

PR #83 is merged into `main`; the audited tree is therefore the post-PR-#83
implementation. The stale telemetry implementation commits that were previously
on PR #84 are not part of this refresh.

## Executive summary

The refreshed audit is documentation-only and is based on the current merged
`main`, not on the former PR #84 feature-branch snapshot.

- Current open findings: **14** (`AUD-01`, `AUD-04`, `AUD-05`, `AUD-06`, `AUD-08`, `AUD-09`, `AUD-10`, `AUD-11`, `AUD-12`, `AUD-14`, `AUD-15`, `AUD-16`, `AUD-17`, `AUD-18`)
- Partially resolved findings: **1** (`AUD-07`)
- Resolved findings: **3** (`AUD-02`, `AUD-03`, `AUD-13`)
- Residual security notes: **2**, attached to the resolved privilege-boundary findings (`AUD-02`, `AUD-03`)
- New findings discovered during this refresh: **3** (`AUD-16`, `AUD-17`, `AUD-18`)

PR #83 resolved the former main-Collector filesystem/capability combination,
gave the collectors live health probes, and constrained telemetry Docker API
access behind a read-only proxy. It did not resolve the unrelated application,
query, frontend, lifecycle, and data-retention findings below.

The counts above describe the original post-PR-#83 audit baseline at the
audited SHA. Subsequent focused remediation work is recorded in the individual
finding sections below; those statuses remain pending until their PRs merge.
Unrelated findings are not changed by the remediation work.

## Validation sources

The audit used the current source/configuration tree at the audited SHA, current
migrations and OpenAPI definitions, current tests, current release/install
code, and the following GitHub Actions evidence:

| Evidence | Run | SHA | Result |
| --- | --- | --- | --- |
| CI: Backend checks | `35475418403` / job `105983721600` | `57267dec46895c44a5e87d55581f2567f5423903` | success |
| CI: Installer and release checks | `35475418403` / job `105983721589` | `57267dec46895c44a5e87d55581f2567f5423903` | success |
| CI: Console checks | `35475418403` / job `105983721494` | `57267dec46895c44a5e87d55581f2567f5423903` | success |
| CodeQL Go | `35475418401` / job `105983721466` | `57267dec46895c44a5e87d55581f2567f5423903` | success |
| CodeQL JavaScript/TypeScript | `35475418401` / job `105983721335` | `57267dec46895c44a5e87d55581f2567f5423903` | success |
| Production Compose Smoke (latest final PR #83 run before merge) | `35474665447` / job `105981729364` | `3ea7f229b8706ba91265e1c7393cb0de91589e53` | success |

The successful Backend checks include the current real Collector + ClickHouse
integration, telemetry security regression coverage, Docker proxy tests, and
Docker log parser tests. There is no post-merge `Production Compose Smoke` run
on `57267dec`; the listed smoke run is the final PR #83 validation immediately
before its merge and is identified as such rather than being presented as a
post-merge run.

After this refresh was pushed, the docs-only PR #84 checks also passed on head
`af03b87999ccf030308ebf45139dd59c32712e40`:

| Evidence | Run | SHA | Result |
| --- | --- | --- | --- |
| PR #84 CI | `35476546289` | `af03b87999ccf030308ebf45139dd59c32712e40` | success |
| PR #84 Production Compose Smoke | `35476546310` / job `105986657304` | `af03b87999ccf030308ebf45139dd59c32712e40` | success |

This PR-level smoke run reported one smoke metric, log, and trace row, one
Docker file-log row, 534 container-metric rows, 24 host-metric rows, expected
Admin API rows, ClickHouse restart persistence, and Collector file-storage
persistence. It is validation of the same post-PR-#83 implementation with the
audit document added; it does not change the audited main SHA above.

Fresh VPS E2E — **NOT RUN**.

## Current post-PR-#83 telemetry boundary

The current production topology is materially different from the old audit
snapshot:

- `otel-collector` has no host-root mount, Docker-container-log mount,
  Docker socket, or `DAC_READ_SEARCH`. It runs read-only, drops all
  capabilities, and keeps `no-new-privileges`.
- `telemetry-host` owns the read-only `/:/hostfs` mount for hostmetrics only.
  It has no Docker socket and no `DAC_READ_SEARCH`, and is private to
  `telemetry_ingest`.
- `telemetry-docker-logs` owns only the read-only
  `/var/lib/docker/containers` mount. It has `DAC_READ_SEARCH` because the
  Docker JSON files are commonly root-owned, but it has no host-root mount,
  socket, public port, or Docker API access.
- `telemetry-docker` uses the restricted internal Docker proxy and has no raw
  socket.
- `telemetry-docker-proxy` is the only telemetry service with the raw Docker
  socket. It has no host port, is on the restricted `telemetry_docker` network,
  allows only required read endpoints, and rejects mutation methods.
- The non-telemetry `worker` still has its separate existing Docker authority;
  the proxy is the only raw-socket owner among telemetry services, not among
  every production service.

The pinned Collector is `otel/opentelemetry-collector-contrib:0.161.0`.
`internal/telemetry/schema.go` pins the corresponding
`otel-clickhouse-exporter-0.161.0` schema checkpoint.

### Runtime ClickHouse metric schema

The runtime schema documentation and real integration use the tables created by
the pinned exporter, rather than the old hand-written test tables:

| Table | Common columns | Type-specific columns |
| --- | --- | --- |
| `otel_metrics_gauge` | `TimeUnix DateTime`, `StartTimeUnix DateTime`, `MetricName LowCardinality(String)`, `ServiceName LowCardinality(String)`, `ResourceAttributes Map(LowCardinality(String), String)`, `ScopeAttributes Map(LowCardinality(String), String)`, `Attributes Map(LowCardinality(String), String)` | `Value Float64` |
| `otel_metrics_sum` | same common columns | `Value Float64`, `AggregationTemporality`, `IsMonotonic` |
| `otel_metrics_histogram` | same common columns | exporter histogram count/sum/bucket fields |
| `otel_metrics_summary` | same common columns | exporter summary count/sum/quantile fields |
| `otel_metrics_exp_histogram` | same common columns | exporter exponential-histogram fields |

The exporter uses `TimeUnix`/`StartTimeUnix` with `DateTime` precision,
`MetricName` for the metric name, `ServiceName` for service identity, maps for
resource and datapoint attributes, and `Float64` for gauge/sum values. The
Stealth metric adapter currently converts the gauge and sum tables into its
stable `timestamp`, `name`, `service`, `value`, `attributes`,
`resource_attributes`, and `kind` model. It uses `DateTime` range parameters
with second precision. The typed histogram, summary, and exponential
histogram tables are stored but are not included in the generic Stealth metric
union; that is tracked as AUD-14.

## Current findings

### AUD-01 — Raw telemetry secrets can be persisted before Admin API redaction

- **Status:** OPEN on the audited baseline; remediation complete in PR #89
  pending merge.
- **Severity:** High
- **Area:** Telemetry privacy; ClickHouse persistence; Admin telemetry
- **Remediation evidence:** PR #89 adds pre-export redaction to every main
  Collector logs, metrics, and traces pipeline, retains Store/API redaction as
  defense in depth, and verifies the pinned Collector configuration.
- **Regression coverage:** The real Collector + ClickHouse integration now
  searches raw exporter rows for deterministic fake secrets before checking
  the Admin API; the Admin integration also verifies the authenticated API
  result contains no raw secret. CI runs both suites with the pinned Collector.
- **Residual risk:** This historical document's open-finding count remains
  tied to the audited pre-remediation SHA until PR #89 is merged. Future
  Collector upgrades must revalidate the processor behavior and exporter
  schema.
- **Current-head evidence:** `telemetry/otel-collector.yaml` has no redaction
  processor before the ClickHouse exporter. The Docker file-log pipeline also
  forwards parsed bodies without a secret-removal processor. In
  `internal/telemetry/store.go`, `QueryLogs`, `QueryTraces`, and
  `QueryMetrics` redact after rows have been selected from ClickHouse. The real
  integration test in `internal/telemetry/store_integration_test.go` asserts
  redacted Store/API results but does not assert that raw ClickHouse rows are
  already sanitized.
- **Affected code/config:** `telemetry/otel-collector.yaml`,
  `telemetry/docker-logs.yaml`, `internal/telemetry/store.go`,
  `internal/telemetry/store_integration_test.go`.
- **Minimal proof path:** Emit a test-only OTLP log or trace containing a
  placeholder value under a key such as `password`, `token`, `authorization`,
  `cookie`, or `client_secret`, then inspect the corresponding raw exporter
  row directly in ClickHouse before calling the Stealth API. The current
  pipeline has no stage that removes it before export; the API-only assertion
  is therefore not storage sanitization.
- **Impact:** Anyone with ClickHouse read access, backups, exports, or incident
  access can see secret-bearing telemetry that the Admin API would redact.
- **Recommended remediation:** Sanitize at or before the persistence boundary
  using an explicitly tested allowlist/redaction processor, with instrumentation
  and Store redaction retained as defense in depth. Define handling for bodies,
  span names/status messages, attributes, resource attributes, and Docker log
  fields.
- **Recommended regression test:** Through the real pinned Collector and
  ClickHouse, emit placeholder secret-bearing log and trace fields, assert raw
  exporter rows contain no sensitive value, and separately assert the stable
  Admin API model remains redacted.

### AUD-02 — Main Collector had an unnecessary full-host filesystem mount

- **Status:** RESOLVED BY PR #83
- **Severity:** High (historical)
- **Area:** Collector privilege boundary
- **Original issue:** The main Collector combined `/:/hostfs:ro` with
  `CAP_DAC_READ_SEARCH`, giving a single process broad host read authority.
- **Fix:** PR #83, merged as `57267dec46895c44a5e87d55581f2567f5423903`.
- **Current verification:** `compose.production.yaml` gives the main
  `otel-collector` no host filesystem mount, Docker log mount, socket, or
  `DAC_READ_SEARCH`; its image target is capability-free and its container
  drops all capabilities.
- **Residual security note:** `telemetry-host` still needs a read-only host-root
  mount for hostmetrics, but it has no DAC bypass capability, no Docker socket,
  a non-root identity, and only private telemetry networking. This is a
  deliberately narrower boundary, not the former combined privilege.
- **Recommended regression test:** Keep the explicit service-level mount and
  capability assertions in `scripts/telemetry-security-test.sh` and the
  production smoke health/host-metric checks.

### AUD-03 — Docker daemon authority was too broad

- **Status:** RESOLVED / RESIDUAL SECURITY NOTE
- **Severity:** High (historical)
- **Area:** Docker API authority
- **Original issue:** Telemetry components previously depended on direct Docker
  socket access.
- **Fix:** PR #83 introduced `telemetry-docker-proxy` and moved Docker metrics
  to it.
- **Current verification:** `telemetry-docker-proxy` is the only telemetry
  service mounting `/var/run/docker.sock`. Its handler permits only GET/HEAD
  and a bounded set of ping/version/events/list/inspect/stats paths and query
  parameters. It sanitizes inspect/list/event responses. `telemetry-docker`,
  `otel-collector`, `telemetry-host`, `telemetry-docker-logs`, `setup`, `api`,
  and `console` have no Docker socket. Proxy tests and the successful smoke
  run cover the read path.
- **Residual security note:** The proxy remains a privileged trust boundary,
  and the non-telemetry `worker` retains its existing Docker authority for
  workload operations. No concrete proxy policy bypass was found in this
  refresh.
- **Recommended regression test:** Preserve the proxy method/path/query
  allowlist tests and assert that only the intended proxy service has a raw
  socket among telemetry services.

### AUD-04 — Alert-rule deletion can fail after notification enqueue

- **Status:** OPEN
- **Severity:** High
- **Area:** PostgreSQL alert and notification lifecycle
- **Current-head evidence:** Migration `000043_admin_observability.up.sql`
  defines `admin_alert_events.rule_id` as `NOT NULL REFERENCES
  admin_alert_rules(id) ON DELETE CASCADE` and
  `admin_notification_deliveries.alert_event_id` as `REFERENCES
  admin_alert_events(id) ON DELETE SET NULL`. Migration
  `000046_admin_notification_tests.up.sql` adds a check permitting a detached
  delivery only when `test_message` is non-null. Normal alert deliveries do
  not have that test marker. `EvaluateAdminAlert` inserts the event and
  enqueues deliveries in one transaction; `DeleteAdminAlertRule` deletes the
  rule in a transaction.
- **Minimal proof path:** Create a normal alert rule, fire or resolve it so an
  event and notification delivery exist, then delete the rule. Cascading the
  event attempts to set the delivery FK to NULL, and the delivery source check
  rejects the resulting normal row. The parent deletion rolls back.
- **Impact:** Administrators cannot reliably remove alert rules after they have
  produced operational history, and the intended history policy is implicit
  and contradictory.
- **Recommended remediation:** Choose and document one policy: retain an
  immutable source snapshot/tombstone, intentionally cascade delivery history,
  or use a durable detached-history relation with a valid source snapshot.
- **Recommended regression test:** PostgreSQL integration tests for
  delete-after-fire and delete-after-resolve, asserting both the chosen
  historical behavior and a committed parent deletion.

### AUD-05 — Monitor alert rules can be orphaned or semantically invalid

- **Status:** OPEN on the audited baseline; remediation implemented in PR #89 and pending merge
- **Severity:** Medium
- **Area:** Alert-rule validation and monitor lifecycle
- **Current-head evidence:** The audited baseline only checked that `monitor_id`
  was a UUID, did not load the monitor, did not verify rule/monitor kind
  compatibility, accepted non-positive certificate thresholds, and deleted
  monitors without checking alert-rule references. The PR #89 remediation adds
  transaction-scoped existence/kind validation, positive expiry thresholds, and
  a deterministic `409 monitor_has_alert_rules` delete response while holding
  the monitor row lock.
- **Minimal proof path:** Submit a rule with a nonexistent monitor UUID, a
  heartbeat rule for an HTTP monitor, or a certificate-expiry rule for a
  non-TLS monitor; separately delete a monitor that has a rule. The current
  database/repository path accepts or leaves these relationships without a
  defined reconciliation outcome.
- **Impact:** Rules can never fire, can evaluate against the wrong monitor
  semantics, or can remain misleading orphan records in the control plane.
- **Regression evidence in PR #89:** `TestAdminAlertMonitorReferenceValidationIntegration`,
  `TestDeleteAdminMonitorWithAlertRuleConflictsIntegration`,
  `TestCertificateExpiryAlertRequiresPositiveDays`, and
  `TestAdminMonitorErrorMapsAlertRuleConflict` cover missing/wrong-kind
  references, positive expiry thresholds, conflict semantics, and successful
  deletion after the rule is removed.
- **Residual risk:** The existing condition JSON remains the storage shape;
  direct SQL writes outside the supported repository/API contract are not an
  application path. The evaluator remains intentionally unchanged.

### AUD-06 — Live-tail can replay large historical ranges and duplicate rows

- **Status:** OPEN
- **Severity:** Medium
- **Area:** Admin telemetry log streaming
- **Current-head evidence:** `adminTelemetryLogTail` in
  `internal/httpapi/server_admin.go` initializes its cursor from the selected
  explorer range. Every two seconds it queries from `cursor - 1ns` through now,
  clamped to the maximum range, so a 30-day selection can cause every poll to
  rescan a 30-day range. The deduplication key is timestamp plus trace/span,
  service, and body; it has no durable unique row ID. The cursor only advances
  on a strictly later timestamp, and the `seen` map is reset after a bounded
  size. Same-timestamp rows and rows preceding a reset can therefore be
  emitted again.
- **Minimal proof path:** Select a 30-day range, start live tail with many
  same-timestamp rows, let polling continue until the bounded seen map resets,
  and observe repeated historical rows or large repeated queries.
- **Impact:** High-volume tenants can receive duplicate events, incur repeated
  ClickHouse work, and make live operational output misleading.
- **Recommended remediation:** Use a short live-tail lookback independent of
  the historical range and a stable cursor `(timestamp, unique tie-breaker)`
  supported by the Store query. Bound reconnect replay explicitly.
- **Recommended regression test:** Generate same-timestamp rows and a range
  larger than the live window; assert monotonic cursor delivery, bounded query
  windows, no duplicates after reconnect, and correct behavior after a seen-set
  eviction.

### AUD-07 — Docker attribution is improved for metrics but incomplete for file logs

- **Status:** PARTIALLY RESOLVED
- **Severity:** Medium
- **Area:** Docker telemetry identity
- **Current-head evidence:** `telemetry/docker-stats.yaml` maps Compose project
  and service labels into metric labels and the smoke evidence shows real rows
  with container identity and all six required container metric instruments.
  `telemetry/docker-logs.yaml`, however, uses `include_file_path` and Docker
  JSON parsing but does not derive a stable container ID, container name,
  image, Compose project, or Compose service from the Docker log filename or
  metadata. It inserts the generic `service.name=stealth-host` resource value.
- **Minimal proof path:** Compare a Docker metric row with a Docker file-log
  row from the same container in ClickHouse/Admin Logs. The metric has
  container/Compose identity, while the log pipeline has a file path and
  generic service identity without an equivalent container resource projection.
- **Impact:** Metrics can be attributed to infrastructure entities while their
  associated logs cannot be reliably scoped or correlated by container/service.
- **Recommended remediation:** Parse and validate the Docker log filename or
  add a bounded metadata enrichment step that emits container ID, name, image,
  Compose project, and Compose service as approved resource attributes.
- **Recommended regression test:** Emit Docker metrics and file logs from a
  known Compose service and assert equivalent identity fields through
  ClickHouse and the Admin Sources/Logs APIs.

### AUD-08 — Monitor SSRF policy omits special-use ranges

- **Status:** OPEN on the audited baseline; remediation implemented in PR #89,
  pending merge
- **Severity:** Medium
- **Area:** Monitor destination validation / SSRF defense
- **Original root cause:** `isPublicIP` relied only on the standard library's
  broad private/loopback/link-local checks, and mixed public/private DNS
  answers were reduced to their public subset.
- **Fix evidence in PR #89:** `monitorDeniedPrefixes` provides an explicit
  IPv4/IPv6 special-use policy, normalizes IPv4-mapped IPv6 addresses, and
  rejects any non-public answer rather than filtering it out. Dial-time DNS
  resolution applies the same policy, including redirect targets.
- **Regression coverage:** `TestMonitorPublicAddressPolicyRejectsSpecialUseAndMappedPrivateIPs`
  and `TestResolvePublicHostRejectsMixedDNSAnswers` cover the range matrix,
  mapped forms, and mixed DNS answers.
- **Residual risk:** The deny table must be reviewed if the monitor egress
  policy or relevant IANA special-purpose allocations change.

### AUD-09 — DNS validation uses a detached background context

- **Status:** OPEN on the audited baseline; remediation implemented in PR #89,
  pending merge
- **Severity:** Medium
- **Area:** Monitor and notification URL validation
- **Original root cause:** URL preflight validation detached DNS lookup with
  `context.Background()` even when the monitor or notification worker had a
  deadline.
- **Fix evidence in PR #89:** monitor validation and notification webhook
  validation now accept and pass the caller context through the resolver;
  redirect validation uses the redirected request context as well.
- **Regression coverage:** `TestMonitorURLValidationHonorsResolverCancellationAndDeadline`
  uses a blocking resolver seam and verifies both cancellation and deadline
  termination.
- **Residual risk:** Resolver behavior remains dependent on the host resolver,
  but no application-owned preflight lookup intentionally outlives its caller.

### AUD-10 — Monitor URL query strings are rejected and DNS matching semantics are implicit

- **Status:** OPEN on the audited baseline; remediation implemented in PR #89,
  pending merge
- **Severity:** Low
- **Area:** Monitor URL/API contract
- **Original root cause:** monitor HTTP validation rejected every non-empty
  query while the notification validator allowed queries; DNS matching allowed
  additional records without stating that subset contract.
- **Fix evidence in PR #89:** HTTP monitor targets now allow query components
  while still rejecting credentials and fragments. DNS matching is explicitly
  documented as subset semantics in the OpenAPI schema and Console hint:
  every configured expected value must be present, while additional records
  are allowed.
- **Regression coverage:** `TestMonitorURLValidationAllowsQueriesAndRejectsUnsafeComponents`
  covers query, userinfo, fragment, and scheme cases;
  `TestExpectedDNSValuesUseDocumentedSubsetSemantics` covers present, missing,
  extra, and normalized values.
- **Residual risk:** DNS operators who require exact answer-set semantics must
  configure every expected value; this PR intentionally preserves the existing
  compatible subset behavior rather than silently changing it.

### AUD-11 — Custom admin time range still depends on localStorage for active state

- **Status:** OPEN
- **Severity:** Medium
- **Area:** Console admin time-range state
- **Current-head evidence:** `console/src/features/admin/admin-time-range.tsx`
  catches `localStorage.getItem` errors in `readStoredRange` and returns
  `"1h"`. The hook uses that storage-backed snapshot directly through
  `useSyncExternalStore`. `setItem` failures are caught, but there is no
  in-memory active-range fallback. Thus a storage failure can cause the
  currently selected custom range to be read back as `1h`, not merely lose
  persistence across reloads.
- **Minimal proof path:** Make `localStorage.getItem` and `setItem` throw,
  select a custom range, and trigger the preference snapshot update. The
  active range falls back to the default instead of remaining custom for the
  current session.
- **Impact:** Admin telemetry views silently query a different time window in
  private browsing, restricted storage, or embedded browser environments.
- **Recommended remediation:** Keep the active range in React/in-memory state
  and treat localStorage only as best-effort persistence. Emit a storage event
  only after updating the in-memory value.
- **Recommended regression test:** Mock throwing get/set storage operations
  and assert that the selected custom range remains active during the current
  session while remount persistence is unavailable.

### AUD-12 — Admin control-plane mutations do not propagate cross-session in realtime

- **Status:** OPEN
- **Severity:** Medium
- **Area:** Console admin realtime behavior
- **Current-head evidence:** `console/src/realtime/project-realtime-listener.tsx`
  subscribes to project-scoped realtime events only. Admin monitor, alert,
  notification, incident, dashboard, and status-page mutations in
  `console/src/api/mutations/admin.ts` invalidate local TanStack Query keys;
  they do not publish or subscribe to an authenticated instance-admin event
  stream. Admin query hooks use optional polling intervals, which is not
  realtime delivery.
- **Minimal proof path:** Open the same admin screen as Admin A and Admin B.
  Have A mutate a control-plane object. B receives no instance-admin event and
  sees the change only after polling, manual refresh, navigation, or another
  invalidation.
- **Impact:** Operators can act on stale alert, incident, monitor, dashboard,
  or status-page state during concurrent administration.
- **Recommended remediation:** Add an authenticated instance-admin event
  channel with per-resource invalidation, or explicitly document polling as the
  product contract and expose its freshness guarantees.
- **Recommended regression test:** Two authenticated admin clients with an
  event assertion that a mutation invalidates the second client's relevant
  query without manual refresh.

### AUD-13 — Collector healthchecks validated configuration instead of runtime health

- **Status:** RESOLVED BY PR #83
- **Severity:** Medium (historical)
- **Area:** Collector runtime health
- **Original issue:** Collector health checks could report config validity
  without proving a running health endpoint.
- **Fix:** PR #83 added the live `health_check` extension and the
  `telemetry-collector-healthcheck` probe wrapper.
- **Current verification:** Main, host-metrics, Docker-log, and Docker-stats
  collectors expose the health extension and Compose probes the live endpoint;
  the Docker proxy has its own socket-aware health probe. The successful
  Production Compose Smoke run confirms the services become healthy and the
  end-to-end signal paths work.
- **Important distinction:** A live health endpoint proves process/runtime
  health, not that ClickHouse has accepted every signal. Export queue/retry
  failures still require the end-to-end smoke and exporter diagnostics.
- **Recommended regression test:** Preserve live probe tests and production
  smoke assertions; do not replace them with config-only validation.

### AUD-14 — Non-gauge/sum metrics are stored but ignored by generic Stealth queries

- **Status:** OPEN
- **Severity:** Medium
- **Area:** ClickHouse metric query adapter/API
- **Current-head evidence:** The pinned exporter creates gauge, sum, histogram,
  summary, and exponential-histogram tables. `metricsQuery` in
  `internal/telemetry/store.go`, as well as the current source/infrastructure
  and metric-alert query paths, unions only `otel_metrics_gauge` and
  `otel_metrics_sum`. The typed histogram/summary/exp-hist tables are therefore
  persisted but are absent from the generic `QueryMetrics` result and related
  surfaces.
- **Minimal proof path:** Emit one real OTLP histogram, summary, and
  exponential histogram through the pinned Collector, verify rows in their
  respective ClickHouse tables, and call `/v1/admin/telemetry/metrics` or the
  related source/infrastructure query. The rows are not projected.
- **Impact:** Users see an incomplete metric inventory while the product's
  generic telemetry surfaces appear to support metrics broadly; alerts or
  dashboards built on those surfaces cannot observe those instruments.
- **Recommended remediation:** Decide and document supported projections, such
  as bounded count, sum, bucket, and approved quantiles. Add typed Store/domain
  adapters without exposing arbitrary ClickHouse SQL or exporter-specific
  columns to the frontend.
- **Recommended regression test:** Real Collector integration for all typed
  metric kinds, asserting either the documented projection or an explicit,
  stable unsupported response.

### AUD-15 — Non-finite trace duration values can cross validation

- **Status:** OPEN
- **Severity:** Low
- **Area:** Trace query parameter validation
- **Current-head evidence:** `QueryTraces` checks `MinMs < 0` and an upper bound,
  then converts `query.MinMs * time.Millisecond` to `uint64`. NaN and positive
  or negative infinity do not satisfy the ordinary comparisons, so they can
  reach the conversion and ClickHouse parameter construction. HTTP validation
  must be checked separately; the Store/domain guard is currently not
  sufficient.
- **Minimal proof path:** Call the HTTP endpoint and Store query with
  `min_duration_ms=NaN`, `+Inf`, and `-Inf`, observing whether conversion or a
  ClickHouse error occurs instead of a clean validation error.
- **Impact:** Invalid input can produce implementation-dependent conversion,
  backend errors, or pathological trace filtering rather than a controlled
  client error.
- **Recommended remediation:** Reject `math.IsNaN` and `math.IsInf` at both
  HTTP and domain boundaries before unit conversion.
- **Recommended regression test:** Table-driven HTTP and Store tests for NaN,
  both infinities, negative values, zero, fractional milliseconds, the maximum,
  and range-boundary values.

### AUD-16 — Database alert-kind schema drifts from API and evaluator support

- **Status:** OPEN on the audited baseline; remediation implemented in PR #89 and pending merge
- **Severity:** Medium
- **Area:** Database/API alert contract
- **Current-head evidence:** Migration `000043_admin_observability.up.sql`
  allowed `backup_failure` and `job_failure` in the SQL CHECK constraint while
  `validAdminAlertKind`, the OpenAPI enums, generated Console enums, and both
  evaluator dispatch paths excluded them. PR #89 removes the unreachable
  validation branch and adds forward migration `000049_admin_alert_kind_contract.up.sql`.
- **Minimal proof path:** Insert or migrate a row with either SQL-permitted
  kind, then attempt to represent, update, evaluate, or create it through the
  repository/API. The database accepts a state for which the API/evaluator has
  no coherent end-to-end contract.
- **Impact:** Direct data imports, old rows, or future migrations can create
  alert records that cannot be edited or evaluated consistently.
- **Migration behavior:** Existing unsupported definitions are copied to the
  private `admin_alert_rule_retired` archive, removed from active
  `admin_alert_rules`, and their event history remains intact through the
  PR #87 snapshot/nullable-FK contract. The active SQL CHECK then permits only
  kinds with a current API and evaluator path.
- **Regression evidence in PR #89:**
  `TestAdminAlertKindContractMigrationRetiresUnsupportedKindsIntegration` and
  `TestAdminAlertRuleRejectsRetiredOperationKinds` verify legacy preservation,
  history detachment, active CHECK rejection, and application rejection.
- **Residual risk:** `backup_failure` and `job_failure` remain available only as
  retired historical definitions, not as active alert kinds; implementing them
  would be a separate product feature.

### AUD-17 — Existing repair/update paths can retain the pre-PR-#83 telemetry topology

- **Status:** OPEN
- **Severity:** High
- **Area:** Installer/update lifecycle; security boundary rollout
- **Current-head evidence:** `internal/installengine/engine.go` uses
  `ensureAsset`, which returns immediately when a destination file already
  exists. Existing-install preparation is intentionally non-destructive, and
  `--repair` is documented as reusing an existing installation without
  replacing its configuration. The asset list can add the split files, but an
  existing `compose.production.yaml` and existing telemetry configuration are
  not migrated or replaced. `docs/upgrade.md` likewise describes manual image
  and service recreation rather than a schema-aware telemetry asset migration.
- **Minimal proof path:** Start with an installation created before PR #83,
  retaining its old Compose and Collector assets, then run the current repair
  or update path. Existing files satisfy `ensureAsset`; no transformation
  changes the old main-Collector host mount, file-log pipeline, or capability
  topology. The installation can therefore continue running the superseded
  boundary after the new release is installed.
- **Impact:** The security separation delivered by PR #83 is not reliably
  applied to existing installations, leaving the former broad Collector
  privilege boundary in place after an operator follows the supported lifecycle
  path.
- **Recommended remediation:** Add an explicit, versioned telemetry asset
  migration with backup/confirmation semantics, or fail/warn clearly when an
  existing installation needs a manual security migration. Make Compose,
  configs, images, networks, and state-volume changes a coherent upgrade unit.
- **Recommended regression test:** Fixture an old installation, run repair and
  update, then assert the resulting Compose/config uses the split collectors,
  has no main-Collector DAC capability or host-root mount, and preserves
  operator-owned settings according to the documented policy.

### AUD-18 — Purge validation omits the post-PR-#83 ClickHouse and Collector volumes

- **Status:** OPEN
- **Severity:** Medium
- **Area:** Uninstall/purge lifecycle and data cleanup
- **Current-head evidence:** Current production Compose declares
  `clickhouse_data`, `otelcol_state`, and `otel_docker_logs_state` in addition
  to the older PostgreSQL, storage, and function-runner volumes. The
  `configuredUninstallVolumes` model in `internal/cli/uninstall.go` still
  contains only the older three categories. `validatePurgeScope` compares the
  declared Compose volume set with that model and refuses a purge when
  unexpected persistent resources exist. Current post-PR-#83 Compose therefore
  fails closed with an unexpected-persistent-resource error instead of
  completing the requested purge. Existing uninstall fixtures cover only the
  old volume set.
- **Minimal proof path:** Run `stealth uninstall --purge` against a current
  installation using the post-PR-#83 Compose file. The exact-set validation
  sees the ClickHouse and collector state volumes absent from the configured
  model and refuses the purge. If that validation were bypassed, verification
  would also not cover the omitted volumes.
- **Impact:** Operators cannot complete the documented full cleanup of current
  telemetry data/state through the supported purge command, and the lifecycle
  model does not provide an auditable policy for retaining or deleting those
  volumes.
- **Recommended remediation:** Add all current persistent resources to the
  uninstall model with explicit retention semantics, preserve custom volume
  names, and verify deletion/retention for ClickHouse and both Collector state
  volumes. Keep the fail-closed behavior for genuinely unknown resources.
- **Recommended regression test:** Use a current Compose fixture and assert
  normal uninstall, purge, custom names, and unknown-volume rejection for the
  complete six-volume set.

## Installer, update, release, and rollback audit

Fresh-install configuration and release publishing know about the split
Collector image variables (`OTEL_COLLECTOR_IMAGE`, host collector, Docker
collector, Docker-log collector), the Docker proxy image, and the
`telemetry_ingest` network. The current release workflow publishes the
`telemetry-collector`, `telemetry-docker-logs`, and `telemetry-docker-proxy`
targets. Asset names include the four current telemetry configuration files.

The confirmed lifecycle gaps are AUD-17 and AUD-18. No separate release image
tag mismatch, wrong fresh-install network name, missing fresh-install asset, or
rollback-specific defect was confirmed in this refresh. The update/repair
behavior is intentionally non-destructive, but it lacks a versioned migration
for the security-sensitive telemetry split; that is why the gap is recorded
instead of treating the fresh-install path as sufficient.

## Telemetry failure isolation

The API startup path creates the telemetry store opportunistically and logs a
telemetry schema failure without aborting core API startup. Admin telemetry
handlers return an unavailable/degraded response when the optional telemetry
backend is absent or fails. PostgreSQL-backed authentication, projects,
deployments, storage, ordinary Console navigation, and unrelated worker duties
do not use ClickHouse as a required control-plane startup dependency in the
current code inspected. No new hard dependency introduced by PR #83 was found.

## Historical #75/#76 verification

Previously remediated areas were rechecked against current source and tests and
were not reopened without new evidence:

- Database cursor quote stripping and exact boundary behavior remain covered by
  the current cursor repository/HTTP tests and the `48530fb`/`0765642` fixes.
- Realtime fanout/invalidation, messaging, and stale Agent status recovery are
  covered by current realtime integration/repository tests and the
  `81572f6`, `109f6f8`, and `72be9e3` fixes.
- Release smoke uses the release revision (`7474ac2`) and current release tests.
- Checklist monotonicity remains covered by setup Console tests (`a303da4`).
- Artifact cleanup durability, cancellation, queue/reservation behavior,
  lease/`SKIP LOCKED`, and quota handling remain covered by the current storage
  and cleanup tests (`47dcd21`, `c413c8f`, `8045335`, `50f7777`).

This compact historical verification is not a claim that those areas need no
future review; it records that this refresh found no current regression tied to
the previously fixed issues.

## Production Compose Smoke evidence

The latest successful final PR #83 smoke run (`35474665447`, SHA
`3ea7f229b8706ba91265e1c7393cb0de91589e53`) reported:

- smoke metric: 1 row
- smoke log: 1 row
- smoke trace: 1 row
- Docker file log: 1 row
- container metrics: 534 rows, including the required CPU, memory, network,
  block I/O, state, and health instruments
- host metrics: 24 rows
- authenticated Admin API logs, traces, and metrics: expected rows
- smoke signals and Docker file log after ClickHouse restart: passed
- Collector file-storage persistence probe after restart: passed

The run ended with the Compose telemetry ingestion and ClickHouse persistence
checks passing. It is pre-merge evidence for the exact PR #83 implementation;
no post-merge smoke run on the audited `main` SHA was available at refresh time.

## Scope and final artifact checks

The refreshed PR is intended to contain only this audit document. The final
branch must be checked with:

```text
git diff --check
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD
```

The expected changed-file set is:

```text
docs/full-bug-audit-2026-09-20.md
```

No credential values, access tokens, or private host data are included in this
document. Fresh VPS E2E and a post-merge Production Compose Smoke run were not
performed/available. A direct raw-secret ClickHouse assertion was not run in the
docs-only refresh represented by this document; PR #89 adds that live assertion
and records the remediation status above.

## Remediation backlog

The recommended implementation order is:

1. AUD-01, AUD-04, AUD-08, AUD-09, and AUD-17 for privacy, data integrity,
   SSRF, cancellation, and security-boundary rollout risk.
2. AUD-05, AUD-06, AUD-07, AUD-11, AUD-12, AUD-14, AUD-16, and AUD-18 for
   control-plane correctness, telemetry completeness, realtime behavior, and
   lifecycle correctness.
3. AUD-10 and AUD-15 for low-severity contract and input-validation cleanup.

All remediation should be delivered in separate focused PRs after this audit
has been reviewed.
