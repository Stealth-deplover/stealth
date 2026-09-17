# Stealth telemetry architecture

This document describes the first production telemetry boundary shipped with
the Stealth control plane. It is intentionally explicit about what is real
today and what remains a later product slice: the current pipeline stores and
queries OTel logs, traces, and metric points; it does not invent an alert,
monitor, incident, or dashboard record when that subsystem is not configured.

## Data flow

```text
Stealth API ───────┐
Stealth worker ────┤  OTLP/HTTP or OTLP/gRPC (private network)
future workloads ──┘                 │
                                     ▼
                         OTel Collector Contrib
                          ├── hostmetrics
                          ├── Prometheus scrape
                          ├── Docker file logs
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

There is no unlimited browser query path. Later alerting and dashboard slices
must use the same bounded `TelemetryStore` contract rather than connecting to
ClickHouse directly.
