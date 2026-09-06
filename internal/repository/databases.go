package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nazxf/stealth-api/internal/apikey"
	dbcore "github.com/nazxf/stealth-api/internal/database"
	"github.com/nazxf/stealth-api/internal/domain"
)

var (
	ErrUnindexedQuery = errors.New("unindexed query")
	ErrSchemaConflict = errors.New("schema change conflicts with existing rows")
	ErrInvalidQuery   = errors.New("invalid query")
	ErrRowHidden      = errors.New("row hidden")
)

type DatabaseActorKind string

const (
	DatabaseConsoleActor     DatabaseActorKind = "console"
	DatabaseAPIKeyActor      DatabaseActorKind = "api_key"
	DatabaseApplicationActor DatabaseActorKind = "application"
	DatabaseAnonymousActor   DatabaseActorKind = "anonymous"
)

// DatabaseActor keeps authentication provenance explicit. In particular, an
// API-key request never fabricates an accounts.id and an application cookie
// never becomes a Console membership.
type DatabaseActor struct {
	Kind          DatabaseActorKind
	AccountID     uuid.UUID
	APIKeyID      uuid.UUID
	APIKeyScopes  []string
	ProjectUserID uuid.UUID
}

func (a DatabaseActor) IsManagement() bool {
	return a.Kind == DatabaseConsoleActor || a.Kind == DatabaseAPIKeyActor
}

func (a DatabaseActor) IsApplication() bool {
	return a.Kind == DatabaseApplicationActor || a.Kind == DatabaseAnonymousActor
}

type DatabaseColumnSchema struct {
	ID          uuid.UUID
	Key         string
	Type        dbcore.ColumnType
	Required    bool
	VarcharSize *int
	Default     any
	HasDefault  bool
}

type DatabaseTableSchema struct {
	Table   domain.DatabaseTable
	Columns []DatabaseColumnSchema
}

type DatabaseTableInput struct {
	Name              string
	RowSecurity       bool
	CreatePermissions []string
	ReadPermissions   []string
	UpdatePermissions []string
	DeletePermissions []string
}

type DatabaseColumnInput struct {
	Key         string
	Type        dbcore.ColumnType
	Required    bool
	VarcharSize *int
	Default     any
	HasDefault  bool
}

type DatabaseIndexInput struct {
	Name       string
	Type       string
	ColumnKeys []string
	Directions []string
}

type DatabaseRowInput struct {
	Data              map[string]any
	ReadPermissions   *[]string
	UpdatePermissions *[]string
	DeletePermissions *[]string
}

type DatabaseRowPatch struct {
	Data              map[string]any
	ReadPermissions   *[]string
	UpdatePermissions *[]string
	DeletePermissions *[]string
}

type RowFilter struct {
	Column DatabaseColumnSchema
	Value  any
}

type RowCursor struct {
	ID    uuid.UUID `json:"id"`
	Value any       `json:"value,omitempty"`
}

type RowQuery struct {
	Limit        int
	Cursor       *RowCursor
	Filters      []RowFilter
	OrderBy      *DatabaseColumnSchema
	Descending   bool
	Search       string
	SearchColumn *DatabaseColumnSchema
}

func EncodeRowCursor(cursor RowCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func DecodeRowCursor(value string) (RowCursor, error) {
	if value == "" {
		return RowCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		// The default id cursor remains a UUID for easy interoperability.
		id, uuidErr := uuid.Parse(value)
		if uuidErr != nil {
			return RowCursor{}, fmt.Errorf("%w: cursor must be a UUID or encoded row cursor", ErrInvalidQuery)
		}
		return RowCursor{ID: id}, nil
	}
	var cursor RowCursor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&cursor); err != nil || cursor.ID == uuid.Nil {
		return RowCursor{}, fmt.Errorf("%w: cursor is invalid", ErrInvalidQuery)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RowCursor{}, fmt.Errorf("%w: cursor is invalid", ErrInvalidQuery)
	}
	return cursor, nil
}

func databaseProjection() string {
	return `id,project_id,name,created_at,updated_at`
}

func tableProjection() string {
	return `id,database_id,project_id,name,row_security,create_permissions,read_permissions,update_permissions,delete_permissions,created_at,updated_at`
}

func columnProjection() string {
	return `id,table_id,key,column_type,required,varchar_size,default_value,created_at,updated_at`
}

func indexProjection() string {
	return `id,table_id,name,index_type,column_keys,directions,created_at,updated_at`
}

func scanDatabase(row interface{ Scan(...any) error }) (domain.ProjectDatabase, error) {
	var item domain.ProjectDatabase
	err := row.Scan(&item.ID, &item.ProjectID, &item.Name, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanTable(row interface{ Scan(...any) error }) (domain.DatabaseTable, error) {
	var item domain.DatabaseTable
	err := row.Scan(&item.ID, &item.DatabaseID, &item.ProjectID, &item.Name, &item.RowSecurity, &item.CreatePermissions, &item.ReadPermissions, &item.UpdatePermissions, &item.DeletePermissions, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanColumn(row interface{ Scan(...any) error }) (domain.DatabaseColumn, error) {
	var item domain.DatabaseColumn
	var raw []byte
	err := row.Scan(&item.ID, &item.TableID, &item.Key, &item.Type, &item.Required, &item.VarcharSize, &raw, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	if len(raw) > 0 {
		item.Default = append(item.Default[:0], raw...)
	}
	return item, nil
}

func scanIndex(row interface{ Scan(...any) error }) (domain.DatabaseIndex, error) {
	var item domain.DatabaseIndex
	err := row.Scan(&item.ID, &item.TableID, &item.Name, &item.Type, &item.ColumnKeys, &item.Directions, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanRow(row interface{ Scan(...any) error }) (domain.DatabaseRow, error) {
	var item domain.DatabaseRow
	var raw []byte
	var creator *uuid.UUID
	err := row.Scan(&item.ID, &item.TableID, &item.ProjectID, &raw, &item.ReadPermissions, &item.UpdatePermissions, &item.DeletePermissions, &creator, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	item.Data = make(map[string]any)
	if len(raw) > 0 {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&item.Data); err != nil {
			return item, err
		}
	}
	if creator != nil {
		value := creator.String()
		item.CreatorProjectUserID = &value
	}
	return item, nil
}

func schemaFromDomain(column domain.DatabaseColumn) DatabaseColumnSchema {
	var defaultValue any
	hasDefault := len(column.Default) > 0 && string(column.Default) != "null"
	if hasDefault {
		decoder := json.NewDecoder(strings.NewReader(string(column.Default)))
		decoder.UseNumber()
		if err := decoder.Decode(&defaultValue); err != nil {
			hasDefault = false
		}
	}
	return DatabaseColumnSchema{ID: mustParseUUID(column.ID), Key: column.Key, Type: dbcore.ColumnType(column.Type), Required: column.Required, VarcharSize: column.VarcharSize, Default: defaultValue, HasDefault: hasDefault}
}

func mustParseUUID(value string) uuid.UUID {
	parsed, _ := uuid.Parse(value)
	return parsed
}

func (r *Repository) ListProjectDatabases(ctx context.Context, projectID uuid.UUID, actor DatabaseActor, limit int, cursor *uuid.UUID) ([]domain.ProjectDatabase, string, bool, error) {
	canManage, err := r.requireDatabaseRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+databaseProjection()+` FROM project_databases WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.ProjectDatabase, 0, limit)
	for rows.Next() {
		item, scanErr := scanDatabase(rows)
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

func (r *Repository) GetProjectDatabase(ctx context.Context, projectID, databaseID uuid.UUID, actor DatabaseActor) (domain.ProjectDatabase, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
		return domain.ProjectDatabase{}, err
	}
	item, err := scanDatabase(r.pool.QueryRow(ctx, `SELECT `+databaseProjection()+` FROM project_databases WHERE project_id=$1 AND id=$2`, projectID, databaseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectDatabase{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateProjectDatabase(ctx context.Context, id, projectID uuid.UUID, actor DatabaseActor, name string) (domain.ProjectDatabase, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ProjectDatabase{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return domain.ProjectDatabase{}, err
	}
	organizationID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.ProjectDatabase{}, err
	}
	if err := r.enforceOrganizationLimitTx(ctx, tx, organizationID, "databases"); err != nil {
		return domain.ProjectDatabase{}, err
	}
	if err := lockDatabaseNamespace(ctx, tx, projectID); err != nil {
		return domain.ProjectDatabase{}, err
	}
	item, err := scanDatabase(tx.QueryRow(ctx, `INSERT INTO project_databases (id,project_id,name) VALUES ($1,$2,$3) RETURNING `+databaseProjection(), id, projectID, name))
	if err != nil {
		return domain.ProjectDatabase{}, mapError(err)
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database.create", "project_database", id, map[string]any{}); err != nil {
		return domain.ProjectDatabase{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProjectDatabase{}, err
	}
	return item, nil
}

func (r *Repository) DeleteProjectDatabase(ctx context.Context, projectID, databaseID uuid.UUID, actor DatabaseActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return err
	}
	if err := lockDatabaseNamespace(ctx, tx, projectID); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_databases WHERE id=$1 AND project_id=$2)`, databaseID, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err := dropIndexesForDatabase(ctx, tx, databaseID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_databases WHERE id=$1 AND project_id=$2`, databaseID, projectID); err != nil {
		return err
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database.delete", "project_database", databaseID, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListDatabaseTables(ctx context.Context, projectID, databaseID uuid.UUID, actor DatabaseActor, limit int, cursor *uuid.UUID) ([]domain.DatabaseTable, string, bool, error) {
	canManage, err := r.requireDatabaseRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if err := r.ensureDatabaseProject(ctx, projectID, databaseID); err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+tableProjection()+` FROM database_tables WHERE project_id=$1 AND database_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, projectID, databaseID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.DatabaseTable, 0, limit)
	for rows.Next() {
		item, scanErr := scanTable(rows)
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

func (r *Repository) GetDatabaseTable(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor) (domain.DatabaseTable, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
		return domain.DatabaseTable{}, err
	}
	item, err := scanTable(r.pool.QueryRow(ctx, `SELECT `+tableProjection()+` FROM database_tables WHERE project_id=$1 AND database_id=$2 AND id=$3`, projectID, databaseID, tableID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseTable{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateDatabaseTable(ctx context.Context, id, projectID, databaseID uuid.UUID, actor DatabaseActor, input DatabaseTableInput) (domain.DatabaseTable, error) {
	permissions, err := normalizeTablePermissions(input)
	if err != nil {
		return domain.DatabaseTable{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.DatabaseTable{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return domain.DatabaseTable{}, err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return domain.DatabaseTable{}, err
	}
	if err := ensureDatabaseProjectTx(ctx, tx, projectID, databaseID); err != nil {
		return domain.DatabaseTable{}, err
	}
	item, err := scanTable(tx.QueryRow(ctx, `INSERT INTO database_tables (id,database_id,project_id,name,row_security,create_permissions,read_permissions,update_permissions,delete_permissions) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+tableProjection(), id, databaseID, projectID, input.Name, input.RowSecurity, permissions[0], permissions[1], permissions[2], permissions[3]))
	if err != nil {
		return domain.DatabaseTable{}, mapError(err)
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_table.create", "database_table", id, map[string]any{}); err != nil {
		return domain.DatabaseTable{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DatabaseTable{}, err
	}
	return item, nil
}

func (r *Repository) UpdateDatabaseTable(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, input DatabaseTableInput) (domain.DatabaseTable, error) {
	permissions, err := normalizeTablePermissions(input)
	if err != nil {
		return domain.DatabaseTable{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.DatabaseTable{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return domain.DatabaseTable{}, err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return domain.DatabaseTable{}, err
	}
	item, err := scanTable(tx.QueryRow(ctx, `UPDATE database_tables SET row_security=$4,create_permissions=$5,read_permissions=$6,update_permissions=$7,delete_permissions=$8,updated_at=now() WHERE project_id=$1 AND database_id=$2 AND id=$3 RETURNING `+tableProjection(), projectID, databaseID, tableID, input.RowSecurity, permissions[0], permissions[1], permissions[2], permissions[3]))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseTable{}, ErrNotFound
	}
	if err != nil {
		return domain.DatabaseTable{}, mapError(err)
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_table.update", "database_table", tableID, map[string]any{"changed_fields": []string{"row_security", "permissions"}}); err != nil {
		return domain.DatabaseTable{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DatabaseTable{}, err
	}
	return item, nil
}

func (r *Repository) DeleteDatabaseTable(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor) error {
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
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_tables WHERE id=$1 AND database_id=$2 AND project_id=$3)`, tableID, databaseID, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err := dropIndexesForTable(ctx, tx, tableID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM database_tables WHERE id=$1 AND database_id=$2 AND project_id=$3`, tableID, databaseID, projectID); err != nil {
		return err
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_table.delete", "database_table", tableID, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListDatabaseColumns(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, limit int, cursor *uuid.UUID) ([]domain.DatabaseColumn, string, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
		return nil, "", err
	}
	if err := r.ensureTableProject(ctx, projectID, databaseID, tableID); err != nil {
		return nil, "", err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+columnProjection()+` FROM database_columns WHERE table_id=$1 AND ($2::uuid IS NULL OR id>$2) ORDER BY id LIMIT $3`, tableID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.DatabaseColumn, 0, limit)
	for rows.Next() {
		item, scanErr := scanColumn(rows)
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

func (r *Repository) CreateDatabaseColumn(ctx context.Context, id, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, input DatabaseColumnInput) (domain.DatabaseColumn, error) {
	if err := dbcore.ValidateColumn(dbcore.ColumnDefinition{Key: input.Key, Type: input.Type, Required: input.Required, VarcharSize: input.VarcharSize, Default: input.Default, HasDefault: input.HasDefault}); err != nil {
		return domain.DatabaseColumn{}, err
	}
	defaultJSON, err := json.Marshal(input.Default)
	if !input.HasDefault {
		defaultJSON = nil
	}
	if err != nil {
		return domain.DatabaseColumn{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.DatabaseColumn{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return domain.DatabaseColumn{}, err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return domain.DatabaseColumn{}, err
	}
	if err := ensureTableProjectTx(ctx, tx, projectID, databaseID, tableID); err != nil {
		return domain.DatabaseColumn{}, err
	}
	var rowCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM database_rows WHERE table_id=$1`, tableID).Scan(&rowCount); err != nil {
		return domain.DatabaseColumn{}, err
	}
	if rowCount > 0 && input.Required && !input.HasDefault {
		return domain.DatabaseColumn{}, ErrSchemaConflict
	}
	item, err := scanColumn(tx.QueryRow(ctx, `INSERT INTO database_columns (id,table_id,key,column_type,required,varchar_size,default_value) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+columnProjection(), id, tableID, input.Key, input.Type, input.Required, input.VarcharSize, defaultJSON))
	if err != nil {
		return domain.DatabaseColumn{}, mapError(err)
	}
	if input.HasDefault {
		if _, err := tx.Exec(ctx, `UPDATE database_rows SET data=data || jsonb_build_object($2,$3::jsonb),updated_at=now() WHERE table_id=$1 AND NOT (data ? $2)`, tableID, input.Key, defaultJSON); err != nil {
			return domain.DatabaseColumn{}, err
		}
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_column.create", "database_column", id, map[string]any{}); err != nil {
		return domain.DatabaseColumn{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DatabaseColumn{}, err
	}
	return item, nil
}

func (r *Repository) DeleteDatabaseColumn(ctx context.Context, projectID, databaseID, tableID, columnID uuid.UUID, actor DatabaseActor) error {
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
	if err := ensureTableProjectTx(ctx, tx, projectID, databaseID, tableID); err != nil {
		return err
	}
	var dependentIndex bool
	var key string
	if err := tx.QueryRow(ctx, `SELECT key FROM database_columns WHERE id=$1 AND table_id=$2 FOR UPDATE`, columnID, tableID).Scan(&key); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_indexes WHERE table_id=$1 AND $2=ANY(column_keys))`, tableID, key).Scan(&dependentIndex); err != nil {
		return err
	}
	if dependentIndex {
		return ErrSchemaConflict
	}
	var dependentRelationship bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_relationships WHERE source_table_id=$1 AND source_column_key=$2)`, tableID, key).Scan(&dependentRelationship); err != nil {
		return err
	}
	if dependentRelationship {
		return ErrSchemaConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM database_columns WHERE id=$1 AND table_id=$2`, columnID, tableID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE database_rows SET data=data-$2,updated_at=now() WHERE table_id=$1`, tableID, key); err != nil {
		return err
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_column.delete", "database_column", columnID, map[string]any{"changed_fields": []string{"key"}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListDatabaseIndexes(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, limit int, cursor *uuid.UUID) ([]domain.DatabaseIndex, string, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
		return nil, "", err
	}
	if err := r.ensureTableProject(ctx, projectID, databaseID, tableID); err != nil {
		return nil, "", err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+indexProjection()+` FROM database_indexes WHERE table_id=$1 AND ($2::uuid IS NULL OR id>$2) ORDER BY id LIMIT $3`, tableID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.DatabaseIndex, 0, limit)
	for rows.Next() {
		item, scanErr := scanIndex(rows)
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

func (r *Repository) CreateDatabaseIndex(ctx context.Context, id, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, input DatabaseIndexInput) (domain.DatabaseIndex, error) {
	if _, err := dbcore.ValidateName(input.Name); err != nil {
		return domain.DatabaseIndex{}, err
	}
	if input.Type != "key" && input.Type != "unique" && input.Type != "fulltext" {
		return domain.DatabaseIndex{}, fmt.Errorf("%w: index type must be key, unique, or fulltext", dbcore.ErrInvalidIdentifier)
	}
	if len(input.Directions) == 0 && len(input.ColumnKeys) > 0 {
		input.Directions = make([]string, len(input.ColumnKeys))
		for i := range input.Directions {
			input.Directions[i] = "asc"
		}
	}
	if len(input.ColumnKeys) == 0 || len(input.ColumnKeys) > 16 || len(input.ColumnKeys) != len(input.Directions) {
		return domain.DatabaseIndex{}, fmt.Errorf("%w: index columns and directions must contain 1 to 16 matching entries", dbcore.ErrInvalidIdentifier)
	}
	if input.Type == "fulltext" && len(input.ColumnKeys) != 1 {
		return domain.DatabaseIndex{}, fmt.Errorf("%w: fulltext indexes must contain exactly one text column", dbcore.ErrInvalidIdentifier)
	}
	seen := make(map[string]struct{}, len(input.ColumnKeys))
	for i, key := range input.ColumnKeys {
		if _, err := dbcore.ValidateIdentifier(key); err != nil {
			return domain.DatabaseIndex{}, err
		}
		if _, ok := seen[key]; ok {
			return domain.DatabaseIndex{}, fmt.Errorf("%w: duplicate index column", dbcore.ErrInvalidIdentifier)
		}
		seen[key] = struct{}{}
		input.Directions[i] = strings.ToLower(input.Directions[i])
		if input.Directions[i] != "asc" && input.Directions[i] != "desc" {
			return domain.DatabaseIndex{}, fmt.Errorf("%w: index direction must be asc or desc", dbcore.ErrInvalidIdentifier)
		}
		if input.Type == "fulltext" && input.Directions[i] != "asc" {
			return domain.DatabaseIndex{}, fmt.Errorf("%w: fulltext index direction must be asc", dbcore.ErrInvalidIdentifier)
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.DatabaseIndex{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireDatabaseWriteTx(ctx, tx, projectID, actor, "databases.write"); err != nil {
		return domain.DatabaseIndex{}, err
	}
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return domain.DatabaseIndex{}, err
	}
	if err := ensureTableProjectTx(ctx, tx, projectID, databaseID, tableID); err != nil {
		return domain.DatabaseIndex{}, err
	}
	columns, err := columnsForTableTx(ctx, tx, tableID)
	if err != nil {
		return domain.DatabaseIndex{}, err
	}
	byKey := make(map[string]DatabaseColumnSchema, len(columns))
	for _, column := range columns {
		byKey[column.Key] = column
	}
	for _, key := range input.ColumnKeys {
		if _, ok := byKey[key]; !ok {
			return domain.DatabaseIndex{}, ErrNotFound
		}
	}
	internalName := internalIndexName(id)
	ddl, err := buildIndexDDL(internalName, tableID, input, byKey)
	if err != nil {
		return domain.DatabaseIndex{}, err
	}
	if _, err := tx.Exec(ctx, ddl); err != nil {
		return domain.DatabaseIndex{}, mapError(err)
	}
	item, err := scanIndex(tx.QueryRow(ctx, `INSERT INTO database_indexes (id,table_id,name,index_type,column_keys,directions) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+indexProjection(), id, tableID, input.Name, input.Type, input.ColumnKeys, input.Directions))
	if err != nil {
		// The DDL is transactional in PostgreSQL, so rollback removes the
		// physical index together with failed metadata insertion.
		return domain.DatabaseIndex{}, mapError(err)
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_index.create", "database_index", id, map[string]any{"columns": input.ColumnKeys}); err != nil {
		return domain.DatabaseIndex{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DatabaseIndex{}, err
	}
	return item, nil
}

func (r *Repository) DeleteDatabaseIndex(ctx context.Context, projectID, databaseID, tableID, indexID uuid.UUID, actor DatabaseActor) error {
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
	if err := ensureTableProjectTx(ctx, tx, projectID, databaseID, tableID); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_indexes WHERE id=$1 AND table_id=$2)`, indexID, tableID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DROP INDEX IF EXISTS "`+internalIndexName(indexID)+`"`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM database_indexes WHERE id=$1 AND table_id=$2`, indexID, tableID); err != nil {
		return err
	}
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_index.delete", "database_index", indexID, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func normalizeTablePermissions(input DatabaseTableInput) ([4][]string, error) {
	var result [4][]string
	values := [][]string{input.CreatePermissions, input.ReadPermissions, input.UpdatePermissions, input.DeletePermissions}
	for i, raw := range values {
		permissions, err := dbcore.NormalizePermissions(raw)
		if err != nil {
			return result, err
		}
		result[i] = permissions
	}
	return result, nil
}

func (r *Repository) requireDatabaseRead(ctx context.Context, projectID uuid.UUID, actor DatabaseActor) (bool, error) {
	switch actor.Kind {
	case DatabaseConsoleActor:
		role, err := r.projectRole(ctx, projectID, actor.AccountID)
		if err != nil {
			return false, err
		}
		return role == "owner" || role == "admin", nil
	case DatabaseAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "databases.read") {
			return false, ErrForbidden
		}
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, projectID).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			return false, ErrNotFound
		}
		return apikey.HasScope(actor.APIKeyScopes, "databases.write"), nil
	default:
		return false, ErrForbidden
	}
}

func (r *Repository) requireDatabaseWriteTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor DatabaseActor, scope string) error {
	switch actor.Kind {
	case DatabaseConsoleActor:
		return requireProjectRoleTx(ctx, tx, projectID, actor.AccountID, "owner", "admin")
	case DatabaseAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, scope) {
			return ErrForbidden
		}
		return requireActiveProjectAPIKeyTx(ctx, tx, projectID, actor.APIKeyID, scope)
	default:
		return ErrForbidden
	}
}

func (r *Repository) ensureDatabaseProject(ctx context.Context, projectID, databaseID uuid.UUID) error {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_databases WHERE id=$1 AND project_id=$2)`, databaseID, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func ensureDatabaseProjectTx(ctx context.Context, tx pgx.Tx, projectID, databaseID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_databases WHERE id=$1 AND project_id=$2)`, databaseID, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ensureTableProject(ctx context.Context, projectID, databaseID, tableID uuid.UUID) error {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_tables WHERE id=$1 AND database_id=$2 AND project_id=$3)`, tableID, databaseID, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func ensureTableProjectTx(ctx context.Context, tx pgx.Tx, projectID, databaseID, tableID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_tables WHERE id=$1 AND database_id=$2 AND project_id=$3)`, tableID, databaseID, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func lockDatabaseNamespace(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id.String())
	return err
}

func columnsForTableTx(ctx context.Context, tx pgx.Tx, tableID uuid.UUID) ([]DatabaseColumnSchema, error) {
	rows, err := tx.Query(ctx, `SELECT `+columnProjection()+` FROM database_columns WHERE table_id=$1 ORDER BY id`, tableID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make([]DatabaseColumnSchema, 0)
	for rows.Next() {
		item, err := scanColumn(rows)
		if err != nil {
			return nil, err
		}
		columns = append(columns, schemaFromDomain(item))
	}
	return columns, rows.Err()
}

func dropIndexesForTable(ctx context.Context, tx pgx.Tx, tableID uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT id FROM database_indexes WHERE table_id=$1 FOR UPDATE`, tableID)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `DROP INDEX IF EXISTS "`+internalIndexName(id)+`"`); err != nil {
			return err
		}
	}
	return nil
}

func dropIndexesForDatabase(ctx context.Context, tx pgx.Tx, databaseID uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT i.id FROM database_indexes i JOIN database_tables t ON t.id=i.table_id WHERE t.database_id=$1 FOR UPDATE`, databaseID)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `DROP INDEX IF EXISTS "`+internalIndexName(id)+`"`); err != nil {
			return err
		}
	}
	return nil
}

func internalIndexName(id uuid.UUID) string {
	return "stealth_db_row_idx_" + strings.ReplaceAll(id.String(), "-", "")
}

func quoteSQLLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func buildIndexDDL(internalName string, tableID uuid.UUID, input DatabaseIndexInput, columns map[string]DatabaseColumnSchema) (string, error) {
	if input.Type == "fulltext" {
		column := columns[input.ColumnKeys[0]]
		if column.Type != dbcore.TypeVarchar && column.Type != dbcore.TypeText {
			return "", fmt.Errorf("%w: fulltext indexes require a varchar or text column", dbcore.ErrInvalidIdentifier)
		}
		keyLiteral := quoteSQLLiteral(column.Key)
		return `CREATE INDEX "` + internalName + `" ON database_rows USING GIN (to_tsvector('simple', COALESCE(data->>` + keyLiteral + `,''))) WHERE table_id = ` + quoteSQLLiteral(tableID.String()), nil
	}
	parts := make([]string, 0, len(input.ColumnKeys))
	for i, key := range input.ColumnKeys {
		column := columns[key]
		keyLiteral := quoteSQLLiteral(key)
		var expression string
		switch column.Type {
		case dbcore.TypeVarchar, dbcore.TypeText:
			expression = `(data->>` + keyLiteral + `)`
		case dbcore.TypeInteger:
			expression = `NULLIF(data->>` + keyLiteral + `,'')::bigint`
		case dbcore.TypeDouble:
			expression = `NULLIF(data->>` + keyLiteral + `,'')::double precision`
		case dbcore.TypeBoolean:
			expression = `NULLIF(data->>` + keyLiteral + `,'')::boolean`
		case dbcore.TypeDatetime:
			expression = `NULLIF(data->>` + keyLiteral + `,'')::timestamptz`
		case dbcore.TypeJSON:
			expression = `(data->` + keyLiteral + `)`
		default:
			return "", fmt.Errorf("%w: unsupported index column type", ErrInvalidQuery)
		}
		parts = append(parts, "("+expression+") "+strings.ToUpper(input.Directions[i]))
	}
	return `CREATE ` + map[bool]string{true: "UNIQUE ", false: ""}[input.Type == "unique"] + `INDEX "` + internalName + `" ON database_rows (` + strings.Join(parts, ",") + `) WHERE table_id = ` + quoteSQLLiteral(tableID.String()), nil
}

func (r *Repository) auditDatabase(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor DatabaseActor, action, targetType string, target uuid.UUID, metadata map[string]any) error {
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["project_id"] = projectID.String()
	switch actor.Kind {
	case DatabaseAPIKeyActor:
		metadata["actor"] = "api_key"
		metadata["api_key_id"] = actor.APIKeyID.String()
	case DatabaseApplicationActor:
		metadata["actor"] = "project_user"
		metadata["project_user_id"] = actor.ProjectUserID.String()
		metadata["source"] = "application"
	case DatabaseAnonymousActor:
		metadata["actor"] = "anonymous"
		metadata["source"] = "application"
	}
	actorID := uuid.Nil
	if actor.Kind == DatabaseConsoleActor {
		actorID = actor.AccountID
	}
	if err := writeAuditMetadata(ctx, tx, orgID, actorID, action, targetType, target, metadata); err != nil {
		return err
	}
	return r.enqueueWebhookEventTx(ctx, tx, projectID, action, targetType, target, metadata)
}

func rowPermissionSQL(column string, actor DatabaseActor, args *[]any) string {
	if actor.Kind == DatabaseConsoleActor || actor.Kind == DatabaseAPIKeyActor {
		return "TRUE"
	}
	if actor.Kind == DatabaseAnonymousActor {
		return column + " @> ARRAY['any']::text[]"
	}
	userPermission := "user:" + actor.ProjectUserID.String()
	*args = append(*args, userPermission)
	return "(" + column + " @> ARRAY['any']::text[] OR " + column + " @> ARRAY['users']::text[] OR $" + strconv.Itoa(len(*args)) + " = ANY(" + column + "))"
}

func tablePermission(perms []string, actor DatabaseActor) bool {
	if actor.Kind == DatabaseConsoleActor || actor.Kind == DatabaseAPIKeyActor {
		return true
	}
	return dbcore.Grants(perms, dbcore.Actor{Authenticated: actor.Kind == DatabaseApplicationActor, UserID: actor.ProjectUserID})
}

func normalizeRowPermissions(raw *[]string, actor DatabaseActor, defaultForUser bool) ([]string, error) {
	if raw == nil {
		if defaultForUser && actor.Kind == DatabaseApplicationActor {
			return []string{"user:" + actor.ProjectUserID.String()}, nil
		}
		return []string{}, nil
	}
	permissions, err := dbcore.NormalizePermissions(*raw)
	if err != nil {
		return nil, err
	}
	if actor.Kind == DatabaseAnonymousActor {
		for _, permission := range permissions {
			if permission == "users" {
				return nil, fmt.Errorf("%w: anonymous rows cannot grant users", dbcore.ErrInvalidPermissions)
			}
		}
	}
	return permissions, nil
}

func buildRowSourceMetadata(actor DatabaseActor, changed []string) map[string]any {
	copyChanged := append([]string(nil), changed...)
	sort.Strings(copyChanged)
	return map[string]any{"changed_fields": copyChanged}
}

// buildDatabaseRowEventMetadata adds the minimum permission snapshot needed
// for a Realtime application subscriber to decide whether a row event is
// visible. The actual row data is intentionally never copied into the audit or
// integration payload; consumers can fetch it through the normal permissioned
// row API after receiving an event.
func buildDatabaseRowEventMetadata(actor DatabaseActor, table domain.DatabaseTable, rowReadPermissions, changed []string) map[string]any {
	metadata := buildRowSourceMetadata(actor, changed)
	metadata["realtime"] = map[string]any{
		"database_id":            table.DatabaseID,
		"table_id":               table.ID,
		"row_security":           table.RowSecurity,
		"table_read_permissions": append([]string(nil), table.ReadPermissions...),
		"row_read_permissions":   append([]string(nil), rowReadPermissions...),
	}
	return metadata
}

func mapDatabaseConstraintError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

// Keep these references in this file so future schema adapters cannot forget
// that database writes are checked against the same API-key implementation.
var _ = mapDatabaseConstraintError
var _ = math.MaxInt64
var _ = time.Second
