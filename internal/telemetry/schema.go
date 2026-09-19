package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// CollectorVersion is coupled to the pinned Collector Contrib image in
// compose.production.yaml. Keep this value aligned with the image tag and
// update the schema adapter and compatibility integration test when upgrading.
const CollectorVersion = "0.161.0"

// CollectorSchemaVersion is the durable checkpoint for the ClickHouse schema
// owned by the pinned Collector ClickHouse exporter.
const CollectorSchemaVersion = "otel-clickhouse-exporter-" + CollectorVersion

const schemaMigrationsTable = "telemetry_schema_migrations"

// Migrate records the exporter schema contract in ClickHouse. The signal
// tables themselves are created by the pinned ClickHouse exporter with
// create_schema enabled. Keeping the registry in the same database means an
// upgrade can add an explicit migration before changing the pinned exporter;
// it also avoids racing multiple API/collector processes over custom DDL.
//
// This operation is deliberately best-effort at process startup: telemetry is
// an optional dependency and a ClickHouse outage must not prevent the core
// Stealth control plane from serving. The one-shot production migration
// command may call this method and fail its own deployment check separately.
func (s *ClickHouseStore) Migrate(ctx context.Context) error {
	if s == nil || s.conn == nil {
		return ErrDisabled
	}
	if !isIdentifier(s.database) {
		return fmt.Errorf("%w: telemetry database must be a valid identifier", ErrInvalidQuery)
	}
	database := quoteIdentifier(s.database)
	queryContext, cancel := context.WithTimeout(ctx, s.maxQueryDuration)
	defer cancel()
	queryContext = clickhouse.Context(queryContext, clickhouse.WithSettings(clickhouse.Settings{
		"max_execution_time": uint64(s.maxQueryDuration / time.Second),
	}))
	if err := s.conn.Exec(queryContext, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (version String, applied_at DateTime64(3)) ENGINE = ReplacingMergeTree(applied_at) ORDER BY version", database, quoteIdentifier(schemaMigrationsTable))); err != nil {
		return fmt.Errorf("create telemetry schema registry: %w", err)
	}
	if err := s.conn.Exec(queryContext, fmt.Sprintf("INSERT INTO %s.%s (version, applied_at) SELECT {version:String}, now64(3) WHERE NOT EXISTS (SELECT 1 FROM %s.%s WHERE version = {version:String})", database, quoteIdentifier(schemaMigrationsTable), database, quoteIdentifier(schemaMigrationsTable)), clickhouse.Named("version", CollectorSchemaVersion)); err != nil {
		return fmt.Errorf("record telemetry schema version: %w", err)
	}
	return nil
}

func isIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func quoteIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}
