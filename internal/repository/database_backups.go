package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbcore "github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	DatabaseBackupVersion        = 1
	DatabaseBackupDefaultMaxRows = 10000
	DatabaseBackupMaxRows        = 10000
	DatabaseBackupMaxTables      = 256
	DatabaseBackupMaxRelations   = 1024
	DatabaseBackupMaxBytes       = 50 << 20
)

const databaseBackupProjection = `id,project_id,database_id,storage_path,size_bytes,checksum_sha256,created_at`

// DatabaseBackupSnapshot is a portable logical snapshot. It intentionally
// contains only database metadata and typed rows; storage paths, credentials,
// and internal PostgreSQL names never leave the repository.
type DatabaseBackupSnapshot struct {
	Version       int                           `json:"version"`
	ProjectID     string                        `json:"project_id"`
	DatabaseID    string                        `json:"database_id"`
	DatabaseName  string                        `json:"database_name"`
	Tables        []DatabaseBackupTable         `json:"tables"`
	Relationships []domain.DatabaseRelationship `json:"relationships"`
}

type DatabaseBackupTable struct {
	Table   domain.DatabaseTable    `json:"table"`
	Columns []domain.DatabaseColumn `json:"columns"`
	Indexes []domain.DatabaseIndex  `json:"indexes"`
	Rows    []domain.DatabaseRow    `json:"rows"`
}

type DatabaseBackupRestoreResult struct {
	Tables        int `json:"tables"`
	Columns       int `json:"columns"`
	Indexes       int `json:"indexes"`
	Rows          int `json:"rows"`
	Relationships int `json:"relationships"`
}

func scanDatabaseBackup(row interface{ Scan(...any) error }) (domain.DatabaseBackup, string, error) {
	var item domain.DatabaseBackup
	var path string
	err := row.Scan(&item.ID, &item.ProjectID, &item.DatabaseID, &path, &item.SizeBytes, &item.ChecksumSHA256, &item.CreatedAt)
	return item, path, err
}

func BackupChecksum(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// BuildDatabaseBackup reads a bounded management snapshot. The caller stores
// the returned JSON through the configured BlobStore after this method returns.
func (r *Repository) BuildDatabaseBackup(ctx context.Context, projectID, databaseID uuid.UUID, actor DatabaseActor, maxRows int) (DatabaseBackupSnapshot, []byte, error) {
	if maxRows <= 0 {
		maxRows = DatabaseBackupDefaultMaxRows
	}
	if maxRows > DatabaseBackupMaxRows {
		return DatabaseBackupSnapshot{}, nil, fmt.Errorf("%w: maximum row count is %d", ErrBackupTooLarge, DatabaseBackupMaxRows)
	}
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
		return DatabaseBackupSnapshot{}, nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DatabaseBackupSnapshot{}, nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return DatabaseBackupSnapshot{}, nil, err
	}
	database, err := scanDatabase(tx.QueryRow(ctx, `SELECT `+databaseProjection()+` FROM project_databases WHERE id=$1 AND project_id=$2`, databaseID, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return DatabaseBackupSnapshot{}, nil, ErrNotFound
	}
	if err != nil {
		return DatabaseBackupSnapshot{}, nil, err
	}
	snapshot := DatabaseBackupSnapshot{
		Version:       DatabaseBackupVersion,
		ProjectID:     projectID.String(),
		DatabaseID:    databaseID.String(),
		DatabaseName:  database.Name,
		Tables:        make([]DatabaseBackupTable, 0),
		Relationships: make([]domain.DatabaseRelationship, 0),
	}
	tableRows, err := tx.Query(ctx, `SELECT `+tableProjection()+` FROM database_tables WHERE project_id=$1 AND database_id=$2 ORDER BY id`, projectID, databaseID)
	if err != nil {
		return DatabaseBackupSnapshot{}, nil, err
	}
	tables := make([]domain.DatabaseTable, 0)
	for tableRows.Next() {
		if len(tables) >= DatabaseBackupMaxTables {
			tableRows.Close()
			return DatabaseBackupSnapshot{}, nil, ErrBackupTooLarge
		}
		table, scanErr := scanTable(tableRows)
		if scanErr != nil {
			tableRows.Close()
			return DatabaseBackupSnapshot{}, nil, scanErr
		}
		tables = append(tables, table)
	}
	if err := tableRows.Err(); err != nil {
		tableRows.Close()
		return DatabaseBackupSnapshot{}, nil, err
	}
	tableRows.Close()
	rowCount := 0
	for _, table := range tables {
		item := DatabaseBackupTable{Table: table, Columns: make([]domain.DatabaseColumn, 0), Indexes: make([]domain.DatabaseIndex, 0), Rows: make([]domain.DatabaseRow, 0)}
		tableID := mustParseUUID(table.ID)
		columns, err := tx.Query(ctx, `SELECT `+columnProjection()+` FROM database_columns WHERE table_id=$1 ORDER BY id`, tableID)
		if err != nil {
			return DatabaseBackupSnapshot{}, nil, err
		}
		for columns.Next() {
			column, scanErr := scanColumn(columns)
			if scanErr != nil {
				columns.Close()
				return DatabaseBackupSnapshot{}, nil, scanErr
			}
			item.Columns = append(item.Columns, column)
		}
		if err := columns.Err(); err != nil {
			columns.Close()
			return DatabaseBackupSnapshot{}, nil, err
		}
		columns.Close()
		indexes, err := tx.Query(ctx, `SELECT `+indexProjection()+` FROM database_indexes WHERE table_id=$1 ORDER BY id`, tableID)
		if err != nil {
			return DatabaseBackupSnapshot{}, nil, err
		}
		for indexes.Next() {
			index, scanErr := scanIndex(indexes)
			if scanErr != nil {
				indexes.Close()
				return DatabaseBackupSnapshot{}, nil, scanErr
			}
			item.Indexes = append(item.Indexes, index)
		}
		if err := indexes.Err(); err != nil {
			indexes.Close()
			return DatabaseBackupSnapshot{}, nil, err
		}
		indexes.Close()
		remaining := maxRows - rowCount
		if remaining < 0 {
			return DatabaseBackupSnapshot{}, nil, ErrBackupTooLarge
		}
		rows, err := tx.Query(ctx, `SELECT `+rowProjection+` FROM database_rows r WHERE r.project_id=$1 AND r.table_id=$2 ORDER BY r.id LIMIT $3`, projectID, tableID, remaining+1)
		if err != nil {
			return DatabaseBackupSnapshot{}, nil, err
		}
		for rows.Next() {
			if rowCount >= maxRows {
				rows.Close()
				return DatabaseBackupSnapshot{}, nil, ErrBackupTooLarge
			}
			row, scanErr := scanRow(rows)
			if scanErr != nil {
				rows.Close()
				return DatabaseBackupSnapshot{}, nil, scanErr
			}
			item.Rows = append(item.Rows, row)
			rowCount++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return DatabaseBackupSnapshot{}, nil, err
		}
		rows.Close()
		snapshot.Tables = append(snapshot.Tables, item)
	}
	relationships, err := tx.Query(ctx, `SELECT `+databaseRelationshipProjection+` FROM database_relationships WHERE project_id=$1 AND database_id=$2 ORDER BY id`, projectID, databaseID)
	if err != nil {
		return DatabaseBackupSnapshot{}, nil, err
	}
	for relationships.Next() {
		if len(snapshot.Relationships) >= DatabaseBackupMaxRelations {
			return DatabaseBackupSnapshot{}, nil, ErrBackupTooLarge
		}
		item, scanErr := scanDatabaseRelationship(relationships)
		if scanErr != nil {
			return DatabaseBackupSnapshot{}, nil, scanErr
		}
		snapshot.Relationships = append(snapshot.Relationships, item)
	}
	if err := relationships.Err(); err != nil {
		relationships.Close()
		return DatabaseBackupSnapshot{}, nil, err
	}
	relationships.Close()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return DatabaseBackupSnapshot{}, nil, err
	}
	if len(payload) == 0 || len(payload) > DatabaseBackupMaxBytes {
		return DatabaseBackupSnapshot{}, nil, ErrBackupTooLarge
	}
	if err := tx.Commit(ctx); err != nil {
		return DatabaseBackupSnapshot{}, nil, err
	}
	return snapshot, payload, nil
}

func (r *Repository) CreateDatabaseBackup(ctx context.Context, id, projectID, databaseID uuid.UUID, actor DatabaseActor, storagePath string, sizeBytes int64, checksum string) (domain.DatabaseBackup, error) {
	if strings.TrimSpace(storagePath) == "" || strings.Contains(storagePath, "..") || sizeBytes < 1 || sizeBytes > DatabaseBackupMaxBytes || len(checksum) != 64 || checksum != strings.ToLower(checksum) {
		return domain.DatabaseBackup{}, ErrInvalidBackup
	}
	if _, err := hex.DecodeString(checksum); err != nil {
		return domain.DatabaseBackup{}, ErrInvalidBackup
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.DatabaseBackup{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return domain.DatabaseBackup{}, err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return domain.DatabaseBackup{}, err
	}
	if err := ensureDatabaseProjectTx(ctx, tx, projectID, databaseID); err != nil {
		return domain.DatabaseBackup{}, err
	}
	item, _, err := scanDatabaseBackup(tx.QueryRow(ctx, `INSERT INTO database_backups (id,project_id,database_id,storage_path,size_bytes,checksum_sha256) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+databaseBackupProjection, id, projectID, databaseID, storagePath, sizeBytes, checksum))
	if err != nil {
		return domain.DatabaseBackup{}, mapError(err)
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_backup.create", "database_backup", id, map[string]any{"size_bytes": sizeBytes, "checksum_sha256": checksum}); err != nil {
		return domain.DatabaseBackup{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DatabaseBackup{}, err
	}
	return item, nil
}

func (r *Repository) ListDatabaseBackups(ctx context.Context, projectID, databaseID uuid.UUID, actor DatabaseActor, limit int, cursor *uuid.UUID) ([]domain.DatabaseBackup, string, bool, error) {
	canManage, err := r.requireDatabaseRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if err := r.ensureDatabaseProject(ctx, projectID, databaseID); err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+databaseBackupProjection+` FROM database_backups WHERE project_id=$1 AND database_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, projectID, databaseID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.DatabaseBackup, 0, limit)
	for rows.Next() {
		item, _, scanErr := scanDatabaseBackup(rows)
		if scanErr != nil {
			return nil, "", false, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, canManage, nil
}

func (r *Repository) GetDatabaseBackup(ctx context.Context, projectID, databaseID, backupID uuid.UUID, actor DatabaseActor) (domain.DatabaseBackup, string, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
		return domain.DatabaseBackup{}, "", err
	}
	item, path, err := scanDatabaseBackup(r.pool.QueryRow(ctx, `SELECT `+databaseBackupProjection+` FROM database_backups WHERE project_id=$1 AND database_id=$2 AND id=$3`, projectID, databaseID, backupID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseBackup{}, "", ErrNotFound
	}
	return item, path, err
}

func (r *Repository) DeleteDatabaseBackup(ctx context.Context, projectID, databaseID, backupID uuid.UUID, actor DatabaseActor) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return "", err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return "", err
	}
	if err := ensureDatabaseProjectTx(ctx, tx, projectID, databaseID); err != nil {
		return "", err
	}
	var path string
	if err := tx.QueryRow(ctx, `SELECT storage_path FROM database_backups WHERE project_id=$1 AND database_id=$2 AND id=$3 FOR UPDATE`, projectID, databaseID, backupID).Scan(&path); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM database_backups WHERE project_id=$1 AND database_id=$2 AND id=$3`, projectID, databaseID, backupID); err != nil {
		return "", err
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_backup.delete", "database_backup", backupID, map[string]any{}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return path, nil
}

func validateBackupUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.Version() != uuid.Version(7) {
		return uuid.Nil, ErrInvalidBackup
	}
	return id, nil
}

func backupTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}

// RestoreDatabaseBackup replaces every table in the target database in one
// transaction. The caller must have an explicit databases.write grant; a
// failed schema, row, relationship, or index aborts the complete restore.
func (r *Repository) RestoreDatabaseBackup(ctx context.Context, projectID, databaseID uuid.UUID, actor DatabaseActor, snapshot DatabaseBackupSnapshot) (DatabaseBackupRestoreResult, error) {
	if snapshot.Version != DatabaseBackupVersion || snapshot.ProjectID != projectID.String() || snapshot.DatabaseID != databaseID.String() || len(snapshot.Tables) > DatabaseBackupMaxTables || len(snapshot.Relationships) > DatabaseBackupMaxRelations {
		return DatabaseBackupRestoreResult{}, ErrInvalidBackup
	}
	rowCount := 0
	for _, table := range snapshot.Tables {
		rowCount += len(table.Rows)
	}
	if rowCount > DatabaseBackupMaxRows {
		return DatabaseBackupRestoreResult{}, ErrBackupTooLarge
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DatabaseBackupRestoreResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return DatabaseBackupRestoreResult{}, err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return DatabaseBackupRestoreResult{}, err
	}
	if err := ensureDatabaseProjectTx(ctx, tx, projectID, databaseID); err != nil {
		return DatabaseBackupRestoreResult{}, err
	}
	if err := dropIndexesForDatabase(ctx, tx, databaseID); err != nil {
		return DatabaseBackupRestoreResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM database_tables WHERE project_id=$1 AND database_id=$2`, projectID, databaseID); err != nil {
		return DatabaseBackupRestoreResult{}, err
	}
	tableSchemas := make(map[uuid.UUID]DatabaseTableSchema, len(snapshot.Tables))
	result := DatabaseBackupRestoreResult{}
	for _, backupTable := range snapshot.Tables {
		tableID, err := validateBackupUUID(backupTable.Table.ID)
		if err != nil || backupTable.Table.DatabaseID != databaseID.String() || backupTable.Table.ProjectID != projectID.String() {
			return DatabaseBackupRestoreResult{}, ErrInvalidBackup
		}
		name, err := dbcore.ValidateName(backupTable.Table.Name)
		if err != nil {
			return DatabaseBackupRestoreResult{}, ErrInvalidBackup
		}
		permissions, err := normalizeTablePermissions(DatabaseTableInput{CreatePermissions: backupTable.Table.CreatePermissions, ReadPermissions: backupTable.Table.ReadPermissions, UpdatePermissions: backupTable.Table.UpdatePermissions, DeletePermissions: backupTable.Table.DeletePermissions})
		if err != nil {
			return DatabaseBackupRestoreResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO database_tables (id,database_id,project_id,name,row_security,create_permissions,read_permissions,update_permissions,delete_permissions,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tableID, databaseID, projectID, name, backupTable.Table.RowSecurity, permissions[0], permissions[1], permissions[2], permissions[3], backupTime(backupTable.Table.CreatedAt), backupTime(backupTable.Table.UpdatedAt)); err != nil {
			return DatabaseBackupRestoreResult{}, mapError(err)
		}
		schema := DatabaseTableSchema{Table: backupTable.Table, Columns: make([]DatabaseColumnSchema, 0, len(backupTable.Columns))}
		for _, backupColumn := range backupTable.Columns {
			columnID, err := validateBackupUUID(backupColumn.ID)
			if err != nil || backupColumn.TableID != tableID.String() {
				return DatabaseBackupRestoreResult{}, ErrInvalidBackup
			}
			var defaultValue any
			hasDefault := len(backupColumn.Default) > 0 && string(backupColumn.Default) != "null"
			if hasDefault {
				decoder := json.NewDecoder(strings.NewReader(string(backupColumn.Default)))
				decoder.UseNumber()
				if err := decoder.Decode(&defaultValue); err != nil {
					return DatabaseBackupRestoreResult{}, ErrInvalidBackup
				}
			}
			column := DatabaseColumnInput{Key: backupColumn.Key, Type: dbcore.ColumnType(backupColumn.Type), Required: backupColumn.Required, VarcharSize: backupColumn.VarcharSize, Default: defaultValue, HasDefault: hasDefault}
			if err := dbcore.ValidateColumn(dbcore.ColumnDefinition{Key: column.Key, Type: column.Type, Required: column.Required, VarcharSize: column.VarcharSize, Default: column.Default, HasDefault: column.HasDefault}); err != nil {
				return DatabaseBackupRestoreResult{}, ErrInvalidBackup
			}
			defaultJSON := []byte(nil)
			if hasDefault {
				defaultJSON = backupColumn.Default
			}
			if _, err := tx.Exec(ctx, `INSERT INTO database_columns (id,table_id,key,column_type,required,varchar_size,default_value,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9)`, columnID, tableID, column.Key, column.Type, column.Required, column.VarcharSize, defaultJSON, backupTime(backupColumn.CreatedAt), backupTime(backupColumn.UpdatedAt)); err != nil {
				return DatabaseBackupRestoreResult{}, mapError(err)
			}
			schema.Columns = append(schema.Columns, DatabaseColumnSchema{ID: columnID, Key: column.Key, Type: column.Type, Required: column.Required, VarcharSize: column.VarcharSize, Default: defaultValue, HasDefault: hasDefault})
			result.Columns++
		}
		tableSchemas[tableID] = schema
		result.Tables++
	}
	for _, backupTable := range snapshot.Tables {
		tableID := mustParseUUID(backupTable.Table.ID)
		schema := tableSchemas[tableID]
		for _, backupRow := range backupTable.Rows {
			rowID, err := validateBackupUUID(backupRow.ID)
			if err != nil || backupRow.TableID != tableID.String() || backupRow.ProjectID != projectID.String() {
				return DatabaseBackupRestoreResult{}, ErrInvalidBackup
			}
			data, err := dbcore.NormalizeCreate(backupRow.Data, columnDefinitions(schema.Columns))
			if err != nil {
				return DatabaseBackupRestoreResult{}, ErrInvalidBackup
			}
			readPermissions, err := dbcore.NormalizePermissions(backupRow.ReadPermissions)
			if err != nil {
				return DatabaseBackupRestoreResult{}, err
			}
			updatePermissions, err := dbcore.NormalizePermissions(backupRow.UpdatePermissions)
			if err != nil {
				return DatabaseBackupRestoreResult{}, err
			}
			deletePermissions, err := dbcore.NormalizePermissions(backupRow.DeletePermissions)
			if err != nil {
				return DatabaseBackupRestoreResult{}, err
			}
			dataJSON, err := json.Marshal(data)
			if err != nil {
				return DatabaseBackupRestoreResult{}, err
			}
			var creator any
			if backupRow.CreatorProjectUserID != nil {
				creatorID, parseErr := uuid.Parse(*backupRow.CreatorProjectUserID)
				if parseErr != nil || creatorID == uuid.Nil {
					return DatabaseBackupRestoreResult{}, ErrInvalidBackup
				}
				creator = creatorID
			}
			if _, err := tx.Exec(ctx, `INSERT INTO database_rows (id,table_id,project_id,data,read_permissions,update_permissions,delete_permissions,creator_project_user_id,created_at,updated_at) VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9,$10)`, rowID, tableID, projectID, dataJSON, readPermissions, updatePermissions, deletePermissions, creator, backupTime(backupRow.CreatedAt), backupTime(backupRow.UpdatedAt)); err != nil {
				return DatabaseBackupRestoreResult{}, mapError(err)
			}
			result.Rows++
		}
	}
	for _, relationship := range snapshot.Relationships {
		relationshipID, err := validateBackupUUID(relationship.ID)
		if err != nil || relationship.ProjectID != projectID.String() || relationship.DatabaseID != databaseID.String() {
			return DatabaseBackupRestoreResult{}, ErrInvalidBackup
		}
		sourceTableUUID, sourceErr := validateBackupUUID(relationship.SourceTableID)
		targetTableUUID, targetErr := validateBackupUUID(relationship.TargetTableID)
		if sourceErr != nil || targetErr != nil {
			return DatabaseBackupResultInvalid()
		}
		sourceTable, sourceOK := tableSchemas[sourceTableUUID]
		_, targetOK := tableSchemas[targetTableUUID]
		if !sourceOK || !targetOK || relationship.RelationshipType != DatabaseRelationshipManyToOne || relationship.OnDelete != DatabaseRelationshipRestrict {
			return DatabaseBackupResultInvalid()
		}
		var sourceColumn *DatabaseColumnSchema
		for i := range sourceTable.Columns {
			if sourceTable.Columns[i].Key == relationship.SourceColumnKey {
				sourceColumn = &sourceTable.Columns[i]
				break
			}
		}
		if sourceColumn == nil || (sourceColumn.Type != dbcore.TypeText && sourceColumn.Type != dbcore.TypeVarchar) {
			return DatabaseBackupResultInvalid()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO database_relationships (id,project_id,database_id,source_table_id,source_column_key,target_table_id,relationship_type,on_delete) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, relationshipID, projectID, databaseID, sourceTableUUID, relationship.SourceColumnKey, targetTableUUID, relationship.RelationshipType, relationship.OnDelete); err != nil {
			return DatabaseBackupResultInvalidWithError(err)
		}
		result.Relationships++
	}
	for _, relationship := range snapshot.Relationships {
		sourceTableID, err := validateBackupUUID(relationship.SourceTableID)
		if err != nil {
			return DatabaseBackupResultInvalid()
		}
		if err := validateDatabaseRowRelationshipsForTableTx(ctx, tx, sourceTableID); err != nil {
			return DatabaseBackupResultInvalidWithError(err)
		}
	}
	for _, backupTable := range snapshot.Tables {
		tableID := mustParseUUID(backupTable.Table.ID)
		schema := tableSchemas[tableID]
		byKey := make(map[string]DatabaseColumnSchema, len(schema.Columns))
		for _, column := range schema.Columns {
			byKey[column.Key] = column
		}
		for _, backupIndex := range backupTable.Indexes {
			indexID, err := validateBackupUUID(backupIndex.ID)
			if err != nil || backupIndex.TableID != tableID.String() {
				return DatabaseBackupResultInvalid()
			}
			input := DatabaseIndexInput{Name: backupIndex.Name, Type: backupIndex.Type, ColumnKeys: backupIndex.ColumnKeys, Directions: backupIndex.Directions}
			if len(input.Directions) == 0 && len(input.ColumnKeys) > 0 {
				input.Directions = make([]string, len(input.ColumnKeys))
				for i := range input.Directions {
					input.Directions[i] = "asc"
				}
			}
			if err := validateBackupIndex(input, byKey); err != nil {
				return DatabaseBackupResultInvalidWithError(err)
			}
			ddl, err := buildIndexDDL(internalIndexName(indexID), tableID, input, byKey)
			if err != nil {
				return DatabaseBackupResultInvalidWithError(err)
			}
			if _, err := tx.Exec(ctx, ddl); err != nil {
				return DatabaseBackupResultInvalidWithError(mapError(err))
			}
			if _, err := tx.Exec(ctx, `INSERT INTO database_indexes (id,table_id,name,index_type,column_keys,directions,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, indexID, tableID, input.Name, input.Type, input.ColumnKeys, input.Directions, backupTime(backupIndex.CreatedAt), backupTime(backupIndex.UpdatedAt)); err != nil {
				return DatabaseBackupResultInvalidWithError(mapError(err))
			}
			result.Indexes++
		}
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_backup.restore", "project_database", databaseID, map[string]any{"tables": result.Tables, "rows": result.Rows, "relationships": result.Relationships}); err != nil {
		return DatabaseBackupRestoreResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DatabaseBackupRestoreResult{}, err
	}
	return result, nil
}

// These small helpers keep restore validation errors mapped through the same
// resource error path while preserving the original PostgreSQL conflict when
// one is available.
func DatabaseBackupResultInvalid() (DatabaseBackupRestoreResult, error) {
	return DatabaseBackupRestoreResult{}, ErrInvalidBackup
}

func DatabaseBackupResultInvalidWithError(err error) (DatabaseBackupRestoreResult, error) {
	if err == nil {
		return DatabaseBackupRestoreResult{}, ErrInvalidBackup
	}
	return DatabaseBackupRestoreResult{}, err
}

func validateBackupIndex(input DatabaseIndexInput, columns map[string]DatabaseColumnSchema) error {
	if _, err := dbcore.ValidateName(input.Name); err != nil {
		return err
	}
	if input.Type != "key" && input.Type != "unique" && input.Type != "fulltext" {
		return ErrInvalidBackup
	}
	if len(input.ColumnKeys) == 0 || len(input.ColumnKeys) > 16 || len(input.ColumnKeys) != len(input.Directions) {
		return ErrInvalidBackup
	}
	if input.Type == "fulltext" && len(input.ColumnKeys) != 1 {
		return ErrInvalidBackup
	}
	seen := make(map[string]struct{}, len(input.ColumnKeys))
	for i, key := range input.ColumnKeys {
		if _, err := dbcore.ValidateIdentifier(key); err != nil {
			return err
		}
		if _, exists := seen[key]; exists {
			return ErrInvalidBackup
		}
		seen[key] = struct{}{}
		column, exists := columns[key]
		if !exists {
			return ErrInvalidBackup
		}
		direction := strings.ToLower(input.Directions[i])
		if direction != "asc" && direction != "desc" || input.Type == "fulltext" && direction != "asc" {
			return ErrInvalidBackup
		}
		if input.Type == "fulltext" && column.Type != dbcore.TypeText && column.Type != dbcore.TypeVarchar {
			return ErrInvalidBackup
		}
	}
	return nil
}

func validateDatabaseRowRelationshipsForTableTx(ctx context.Context, tx pgx.Tx, tableID uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT data FROM database_rows WHERE table_id=$1`, tableID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var data map[string]any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&data); err != nil {
			return err
		}
		if err := validateDatabaseRowRelationshipsTx(ctx, tx, tableID, data); err != nil {
			return err
		}
	}
	return rows.Err()
}
