# Telemetry schema contract

The production Collector is built from the pinned
`otel/opentelemetry-collector-contrib:0.161.0` base image. Its ClickHouse
exporter creates the OTel signal tables (`otel_logs`,
`otel_traces`, and the typed metric tables) on a fresh database. The exporter
SQL templates and insert columns are part of that pinned dependency; do not
copy an unpinned table definition into this repository.

`internal/telemetry.CollectorSchemaVersion` is the durable compatibility
checkpoint. The API records it in `telemetry_schema_migrations` using an
idempotent ClickHouse statement. Before changing the Collector pin:

1. compare the new release's ClickHouse exporter SQL templates and changelog;
2. add a new migration step for every incompatible or newly required column;
3. validate the existing tables against the new insert contract on a throwaway
   ClickHouse instance;
4. update this document, `CollectorSchemaVersion`, and the Compose image tag
   in the same change;
5. run the ClickHouse integration suite before upgrading a production volume.

The runtime schema observed from the pinned `0.161.0` exporter is:

- `otel_metrics_gauge`, `otel_metrics_sum`, `otel_metrics_histogram`,
  `otel_metrics_summary`, and `otel_metrics_exp_histogram`;
- metric timestamps are `TimeUnix DateTime` and start timestamps are
  `StartTimeUnix DateTime`;
- `MetricName` and `ServiceName` are `LowCardinality(String)`;
- `ResourceAttributes`, `ScopeAttributes`, and `Attributes` are
  `Map(LowCardinality(String), String)`;
- gauge and sum values are `Value Float64`; sum rows additionally carry
  `AggregationTemporality` and `IsMonotonic`;
- histogram, summary, and exponential-histogram tables retain their typed
  exporter-specific aggregate columns and still share the common identity and
  attribute columns above.

The production compatibility tests start the pinned Collector, emit OTLP logs,
traces, and gauge, sum, histogram, summary, and exponential-histogram metrics,
and read them through `ClickHouseStore`.
The hand-written tables in the Admin HTTP integration fixture are only an
isolated route/query fixture; they are not evidence of exporter compatibility.

The API never accepts database/table identifiers from a browser. Operator
configuration accepts only validated ClickHouse database identifiers, and all
telemetry filters remain typed query parameters.
