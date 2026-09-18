# Stealth telemetry architecture

This document describes the production telemetry boundary shipped with the
Stealth control plane. It is intentionally explicit about what is real: the
pipeline stores and queries OTel logs, traces, and metric points, while
monitors, alert rules, incidents, notifications, and saved dashboards remain
durable PostgreSQL control-plane records. The Console reports unavailable or
empty telemetry explicitly; it does not manufacture samples.

## Data flow

```text
Stealth API ───────┐
Stealth worker ────┤  OTLP/HTTP or OTLP/gRPC (private network)
future workloads ──┘                 │
                                     ▼
                         OTel Collector Contrib
                          ├── hostmetrics
                          ├── Prometheus scrape
                          ├── Docker file logs (host-mounted read-only)
                          └── bounded batch + retry queue
                                     │
                                     ▼
                              ClickHouse (private)
                                     │
                    authenticated instance-admin API
                                     │
                                     ▼
                              Stealth Console
```

The API and worker use the standard OpenTelemetry SDK where instrumentation
exists. The API also keeps the existing low-cardinality Prometheus endpoint
for scraping. The Collector is the canonical transport boundary for new
signals; the Prometheus endpoint is an input to that boundary, not a browser
API.

## Service and privilege boundary

The production Compose topology contains two Collector roles:

- `otel-collector` receives OTLP, scrapes host/API/worker metrics, reads
  Docker JSON log files, and writes ClickHouse. It has no Docker socket. The
  host root bind is read-only for hostmetrics and masks the socket path.
- `telemetry-docker` is an internal metrics-only collector. It is the only
  telemetry service with a read-only Docker socket, has no HTTP/OTLP receiver,
  publishes no host port, and can only send Docker statistics to the main
  Collector over the private Compose network. It cannot execute commands.

The existing trusted worker still owns its Docker socket for the function and
site runner. The API, Console, setup API, ClickHouse, and public proxy do not
receive it. ClickHouse and both Collector listeners are internal Compose
services; no ClickHouse or OTLP port is published by default.

This boundary reduces the authority of web-facing services but does not make
the host, worker, or Collector risk-free. Operators should still restrict the
Compose network, host access, and Docker group membership.

## ClickHouse storage

Telemetry is kept out of PostgreSQL. The pinned Collector Contrib ClickHouse
exporter (`0.161.0`) owns the OTel-compatible signal tables and uses a
persistent sending queue backed by `otelcol_state`. The ClickHouse data volume
is separate from PostgreSQL and is retained in `clickhouse_data`.

The exporter tables are named:

| Signal | Tables |
| --- | --- |
| Logs | `otel_logs` |
| Traces | `otel_traces` |
| Metrics | `otel_metrics_gauge`, `otel_metrics_sum`, `otel_metrics_histogram`, `otel_metrics_summary`, `otel_metrics_exp_histogram` |

`internal/telemetry` records the pinned exporter contract in the
`telemetry_schema_migrations` table. The record is idempotent and is not a
replacement for signal-table DDL. Before changing the Collector image,
review the upstream exporter changelog and SQL templates, add a new migration
step if columns or indexes change, then update the image and
`CollectorSchemaVersion` together. This keeps schema compatibility explicit
while preserving the exporter’s supported `create_schema` behavior for a
fresh installation.

The exporter applies the configured TTL to signal tables. `TELEMETRY_RETENTION`
is bounded by the API configuration loader and is passed to the Collector;
operators should size disk and retention together. A ClickHouse backup is a
separate operational concern from the PostgreSQL control-plane backup. The
current repository provisions the persistent volume and exposes the storage
boundary, but does not claim that a backup provider exists without an
operator-configured backup job.

## Query boundary

The browser never connects to ClickHouse and never submits raw SQL. Admin
requests require an instance owner or instance admin role, are scoped to a
bounded time range and row count, use typed ClickHouse named parameters, set a
server-side execution/resource limit, and inherit HTTP request cancellation.
Returned attributes and status text pass through secret redaction before they
reach the Console. ClickHouse failures become a generic telemetry-unavailable
response so backend details and query fragments are not disclosed.

The current query surface is deliberately domain-shaped:

- `/v1/admin/telemetry/logs` supports bounded service, level, and text filters.
- `/v1/admin/telemetry/traces` supports service, trace ID, and minimum duration.
- `/v1/admin/telemetry/metrics` returns real OTel gauge/sum points.
- `/v1/admin/telemetry/sources` reports observed services and signal volume.
- `/v1/admin/overview` combines control-plane health with real HTTP span
  aggregates and host metric samples when those signals exist.
- `/v1/admin/telemetry/logs/tail` provides a short-lived authenticated SSE
  stream backed by repeated bounded queries; it is not a ClickHouse socket or
  browser-side polling of an unbounded result.

The monitoring worker executes HTTP, TCP, DNS, TLS, and heartbeat probes from
the trusted worker boundary. Monitor failure, heartbeat, and certificate rules
are evaluated transactionally with monitor results. Metric threshold, HTTP
error-rate, latency percentile, log-match, service-health, and disk-pressure
rules are evaluated by the worker against bounded ClickHouse aggregates. A
ClickHouse outage leaves the last alert state unchanged until a real sample is
available again.

Saved dashboards persist typed panel definitions in PostgreSQL. The Console
currently lets an owner create and edit metric time-series, recent-log, and
monitor-status panels. Each panel uses the same authenticated domain query
API and never accepts raw SQL.

The owner control room also exposes durable operations, audit events, monitor
checks, alert history, notification delivery state, incidents, infrastructure
metric samples, service-map edges, error-group status, and a public status-page
projection. Error groups are derived and fingerprinted in ClickHouse; their
owner-controlled lifecycle state is kept in PostgreSQL so acknowledgement and
resolution survive a restart without copying high-volume occurrences into the
control plane. Notification test sends use the same durable delivery queue as
alert notifications and never return channel secrets to the browser.

The API remains usable when ClickHouse or the Collector is unavailable. Admin
pages report the telemetry backend as unavailable; core authentication,
projects, deployments, and storage do not depend on a successful telemetry
query.

## Component stability decision

The ClickHouse exporter is pinned rather than floating. At the pinned release,
the upstream stability classification is beta for traces/logs and alpha for
metrics. Stealth therefore keeps the exporter behind the `TelemetryStore`
interface, avoids exposing exporter-specific SQL to the browser, and treats a
Collector/ClickHouse outage as degraded observability rather than a control
plane outage. Upgrades require schema and integration validation before the
pin is changed.

## Configuration and sizing

The minimum controls are:

- `CLICKHOUSE_MEMORY_LIMIT` for the ClickHouse container;
- `OTEL_COLLECTOR_MEMORY_LIMIT` and
  `OTEL_DOCKER_COLLECTOR_MEMORY_LIMIT` for Collector processes;
- `TELEMETRY_MAX_QUERY_DURATION`, `TELEMETRY_MAX_QUERY_RANGE`, and
  `TELEMETRY_MAX_QUERY_ROWS` for API reads;
- `TELEMETRY_RETENTION` for signal TTL;
- the Collector memory limiter and persistent sending queue for ingestion.

There is no unlimited browser query path. Any future alerting or dashboard
slice must use the same bounded `TelemetryStore` contract rather than
connecting to ClickHouse directly. The current release intentionally does not
claim the following as implemented: a Playwright synthetic runner,
workload-scoped OTLP
credential issuance, owner-editable retention/sampling/export settings, a
ClickHouse backup provider, automatic alert-to-incident correlation,
maintenance-window scheduling, or a full advanced SQL mode. These are not
represented by placeholder endpoints or fabricated telemetry. The existing
monitoring surface accepts only the probe kinds implemented by the trusted
worker, and unsupported alert kinds are rejected instead of being stored as
inert rules.

## Failure and recovery behavior

Telemetry is an optional dependency for the control plane. If ClickHouse or
the Collector is unavailable, API authentication, projects, deployments,
storage, and the rest of the Console remain available. Query endpoints return
a generic degraded response and the UI renders an explicit unavailable or
empty state. Collector export has a persistent queue, retry, and memory
limits; the worker leaves an alert state unchanged when the required sample
is unavailable, avoiding false recoveries during an outage.

ClickHouse data and the Collector queue are separate from the PostgreSQL
control-plane volume. PostgreSQL backups therefore do not constitute a
telemetry backup. Operators who need telemetry recovery must configure and
verify a ClickHouse backup workflow appropriate for their storage platform.
