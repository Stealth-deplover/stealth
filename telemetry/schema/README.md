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

The API never accepts database/table identifiers from a browser. Operator
configuration accepts only validated ClickHouse database identifiers, and all
telemetry filters remain typed query parameters.
