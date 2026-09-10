package repository

import (
	"context"
	"errors"
	"fmt"

	dbcore "github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const databaseRelationshipProjection = `id,project_id,database_id,source_table_id,source_column_key,target_table_id,relationship_type,on_delete,created_at,updated_at`

const (
	DatabaseRelationshipManyToOne = "many_to_one"
	DatabaseRelationshipRestrict  = "restrict"
)

type DatabaseRelationshipInput struct {
	SourceTableID    uuid.UUID
	SourceColumnKey  string
	TargetTableID    uuid.UUID
	RelationshipType string
	OnDelete         string
}

func scanDatabaseRelationship(row interface{ Scan(...any) error }) (domain.DatabaseRelationship, error) {
	var item domain.DatabaseRelationship
	err := row.Scan(
		&item.ID,
		&item.ProjectID,
		&item.DatabaseID,
		&item.SourceTableID,
		&item.SourceColumnKey,
		&item.TargetTableID,
		&item.RelationshipType,
		&item.OnDelete,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

func (r *Repository) ListDatabaseRelationships(ctx context.Context, projectID, databaseID uuid.UUID, actor DatabaseActor, limit int, cursor *uuid.UUID) ([]domain.DatabaseRelationship, string, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
		return nil, "", err
	}
	if err := r.ensureDatabaseProject(ctx, projectID, databaseID); err != nil {
		return nil, "", err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+databaseRelationshipProjection+` FROM database_relationships WHERE project_id=$1 AND database_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, projectID, databaseID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.DatabaseRelationship, 0, limit)
	for rows.Next() {
		item, scanErr := scanDatabaseRelationship(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func (r *Repository) GetDatabaseRelationship(ctx context.Context, projectID, databaseID, relationshipID uuid.UUID, actor DatabaseActor) (domain.DatabaseRelationship, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	item, err := scanDatabaseRelationship(r.pool.QueryRow(ctx, `SELECT `+databaseRelationshipProjection+` FROM database_relationships WHERE project_id=$1 AND database_id=$2 AND id=$3`, projectID, databaseID, relationshipID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseRelationship{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateDatabaseRelationship(ctx context.Context, id, projectID, databaseID uuid.UUID, actor DatabaseActor, input DatabaseRelationshipInput) (domain.DatabaseRelationship, error) {
	if _, err := dbcore.ValidateIdentifier(input.SourceColumnKey); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	if input.RelationshipType == "" {
		input.RelationshipType = DatabaseRelationshipManyToOne
	}
	if input.OnDelete == "" {
		input.OnDelete = DatabaseRelationshipRestrict
	}
	if input.RelationshipType != DatabaseRelationshipManyToOne {
		return domain.DatabaseRelationship{}, fmt.Errorf("%w: relationship type must be many_to_one", dbcore.ErrInvalidIdentifier)
	}
	if input.OnDelete != DatabaseRelationshipRestrict {
		return domain.DatabaseRelationship{}, fmt.Errorf("%w: on_delete must be restrict", dbcore.ErrInvalidIdentifier)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.DatabaseRelationship{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	if err := ensureDatabaseProjectTx(ctx, tx, projectID, databaseID); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	if err := ensureTableProjectTx(ctx, tx, projectID, databaseID, input.SourceTableID); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	if err := ensureTableProjectTx(ctx, tx, projectID, databaseID, input.TargetTableID); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	var columnType dbcore.ColumnType
	if err := tx.QueryRow(ctx, `SELECT column_type FROM database_columns WHERE table_id=$1 AND key=$2`, input.SourceTableID, input.SourceColumnKey).Scan(&columnType); errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseRelationship{}, ErrNotFound
	} else if err != nil {
		return domain.DatabaseRelationship{}, err
	}
	if columnType != dbcore.TypeText && columnType != dbcore.TypeVarchar {
		return domain.DatabaseRelationship{}, fmt.Errorf("%w: relationship source column must be text or varchar", dbcore.ErrInvalidColumn)
	}
	var invalidRows bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM database_rows source
		WHERE source.table_id=$1
		  AND source.data ? $2
		  AND source.data->>$2 IS NOT NULL
		  AND NOT EXISTS (
			SELECT 1 FROM database_rows target
			WHERE target.table_id=$3 AND target.id::text=source.data->>$2
		  )
	)`, input.SourceTableID, input.SourceColumnKey, input.TargetTableID).Scan(&invalidRows); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	if invalidRows {
		return domain.DatabaseRelationship{}, ErrSchemaConflict
	}
	item, err := scanDatabaseRelationship(tx.QueryRow(ctx, `INSERT INTO database_relationships (id,project_id,database_id,source_table_id,source_column_key,target_table_id,relationship_type,on_delete) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+databaseRelationshipProjection, id, projectID, databaseID, input.SourceTableID, input.SourceColumnKey, input.TargetTableID, input.RelationshipType, input.OnDelete))
	if err != nil {
		return domain.DatabaseRelationship{}, mapError(err)
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_relationship.create", "database_relationship", id, map[string]any{
		"source_table_id":   input.SourceTableID,
		"source_column_key": input.SourceColumnKey,
		"target_table_id":   input.TargetTableID,
	}); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DatabaseRelationship{}, err
	}
	return item, nil
}

func (r *Repository) DeleteDatabaseRelationship(ctx context.Context, projectID, databaseID, relationshipID uuid.UUID, actor DatabaseActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return err
	}
	if err := ensureDatabaseProjectTx(ctx, tx, projectID, databaseID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM database_relationships WHERE project_id=$1 AND database_id=$2 AND id=$3`, projectID, databaseID, relationshipID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_relationship.delete", "database_relationship", relationshipID, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// validateDatabaseRowRelationshipsTx enforces every relationship whose source
// is tableID. A missing or explicit null source value is allowed; any present
// non-null value must be the UUID of a live row in the target table.
func validateDatabaseRowRelationshipsTx(ctx context.Context, tx pgx.Tx, tableID uuid.UUID, data map[string]any) error {
	rows, err := tx.Query(ctx, `SELECT source_column_key,target_table_id FROM database_relationships WHERE source_table_id=$1`, tableID)
	if err != nil {
		return err
	}
	type constraint struct {
		columnKey     string
		targetTableID uuid.UUID
	}
	constraints := make([]constraint, 0)
	for rows.Next() {
		var columnKey string
		var targetTableID uuid.UUID
		if err := rows.Scan(&columnKey, &targetTableID); err != nil {
			rows.Close()
			return err
		}
		constraints = append(constraints, constraint{columnKey: columnKey, targetTableID: targetTableID})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, relationship := range constraints {
		columnKey := relationship.columnKey
		targetTableID := relationship.targetTableID
		value, present := data[columnKey]
		if !present || value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: %w: relationship value must be a row UUID", columnKey, dbcore.ErrInvalidValue)
		}
		targetID, err := uuid.Parse(text)
		if err != nil {
			return fmt.Errorf("%s: %w: relationship value must be a row UUID", columnKey, dbcore.ErrInvalidValue)
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_rows WHERE table_id=$1 AND id=$2)`, targetTableID, targetID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: target row for %s does not exist", ErrReferenceViolation, columnKey)
		}
	}
	return nil
}

func ensureNoDatabaseRowReferencesTx(ctx context.Context, tx pgx.Tx, targetTableID, targetRowID uuid.UUID) error {
	var referenced bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1
		FROM database_relationships relationship
		JOIN database_rows source ON source.table_id=relationship.source_table_id
		WHERE relationship.target_table_id=$1
		  AND source.data ? relationship.source_column_key
		  AND source.data->>relationship.source_column_key=$2
	)`, targetTableID, targetRowID.String()).Scan(&referenced); err != nil {
		return err
	}
	if referenced {
		return ErrReferenceViolation
	}
	return nil
}
