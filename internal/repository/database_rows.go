package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/apikey"
	dbcore "github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const rowProjection = `r.id,r.table_id,r.project_id,r.data,r.read_permissions,r.update_permissions,r.delete_permissions,r.creator_project_user_id,r.created_at,r.updated_at`
const rowProjectionNoAlias = `id,table_id,project_id,data,read_permissions,update_permissions,delete_permissions,creator_project_user_id,created_at,updated_at`

const (
	// DatabaseRowExportDefaultLimit keeps an accidental export from turning
	// into an unbounded database and response-buffer operation.
	DatabaseRowExportDefaultLimit = 1000
	DatabaseRowExportMaxLimit     = 10000
	DatabaseRowBulkImportMaxRows  = 1000
	DatabaseRowTransactionMaxOps  = 100
)

type DatabaseBulkRowInput struct {
	ID                uuid.UUID
	Data              map[string]any
	ReadPermissions   *[]string
	UpdatePermissions *[]string
	DeletePermissions *[]string
}

// DatabaseRowTransactionOperation is one atomic row mutation. Create and
// update return rows in the transaction result; delete returns its id in
// DeletedIDs. The repository evaluates permissions and relationships for every
// operation before committing any of them.
type DatabaseRowTransactionOperation struct {
	Action            string
	ID                uuid.UUID
	Data              map[string]any
	ReadPermissions   *[]string
	UpdatePermissions *[]string
	DeletePermissions *[]string
}

type DatabaseRowTransactionResult struct {
	Rows       []domain.DatabaseRow
	DeletedIDs []string
}

// DatabaseTableSchema loads schema metadata and performs the same project and
// table-read checks used by row reads. When row security is enabled, the
// operation may still narrow results further using each row's grant.
func (r *Repository) DatabaseTableSchema(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor) (DatabaseTableSchema, error) {
	if actor.IsManagement() {
		if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil {
			return DatabaseTableSchema{}, err
		}
	}
	schema, err := loadTableSchema(ctx, r.pool, projectID, databaseID, tableID)
	if err != nil {
		return DatabaseTableSchema{}, err
	}
	if err := authorizeDatabaseRowRead(schema, actor); err != nil {
		return DatabaseTableSchema{}, err
	}
	return schema, nil
}

func loadTableSchema(ctx context.Context, txOrPool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, projectID, databaseID, tableID uuid.UUID) (DatabaseTableSchema, error) {
	item, err := scanTable(txOrPool.QueryRow(ctx, `SELECT `+tableProjection()+` FROM database_tables WHERE id=$1 AND database_id=$2 AND project_id=$3`, tableID, databaseID, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return DatabaseTableSchema{}, ErrNotFound
	}
	if err != nil {
		return DatabaseTableSchema{}, err
	}
	rows, err := txOrPool.Query(ctx, `SELECT `+columnProjection()+` FROM database_columns WHERE table_id=$1 ORDER BY id`, tableID)
	if err != nil {
		return DatabaseTableSchema{}, err
	}
	defer rows.Close()
	columns := make([]DatabaseColumnSchema, 0)
	for rows.Next() {
		column, scanErr := scanColumn(rows)
		if scanErr != nil {
			return DatabaseTableSchema{}, scanErr
		}
		columns = append(columns, schemaFromDomain(column))
	}
	if err := rows.Err(); err != nil {
		return DatabaseTableSchema{}, err
	}
	return DatabaseTableSchema{Table: item, Columns: columns}, nil
}

func authorizeDatabaseRowRead(schema DatabaseTableSchema, actor DatabaseActor) error {
	if actor.IsApplication() && !tablePermission(schema.Table.ReadPermissions, actor) && !schema.Table.RowSecurity {
		return ErrForbidden
	}
	return nil
}

func rowSQLExpression(prefix string, column DatabaseColumnSchema) string {
	literal := quoteSQLLiteral(column.Key)
	switch column.Type {
	case dbcore.TypeVarchar, dbcore.TypeText:
		return `(` + prefix + `.data->>` + literal + `)`
	case dbcore.TypeInteger:
		return `NULLIF(` + prefix + `.data->>` + literal + `,'')::bigint`
	case dbcore.TypeDouble:
		return `NULLIF(` + prefix + `.data->>` + literal + `,'')::double precision`
	case dbcore.TypeBoolean:
		return `NULLIF(` + prefix + `.data->>` + literal + `,'')::boolean`
	case dbcore.TypeDatetime:
		return `NULLIF(` + prefix + `.data->>` + literal + `,'')::timestamptz`
	case dbcore.TypeJSON:
		return `(` + prefix + `.data->` + literal + `)`
	default:
		return `NULL`
	}
}

func fullTextSQLExpression(prefix string, column DatabaseColumnSchema) string {
	return `(to_tsvector('simple', COALESCE(` + prefix + `.data->>` + quoteSQLLiteral(column.Key) + `,'')))`
}

func (r *Repository) indexedColumns(ctx context.Context, tableID uuid.UUID, keys []string) (bool, error) {
	for _, key := range keys {
		var found bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_indexes WHERE table_id=$1 AND column_keys[1]=$2)`, tableID, key).Scan(&found); err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}

func (r *Repository) fullTextIndexedColumn(ctx context.Context, tableID uuid.UUID, key string) (bool, error) {
	var found bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_indexes WHERE table_id=$1 AND index_type='fulltext' AND column_keys[1]=$2)`, tableID, key).Scan(&found); err != nil {
		return false, err
	}
	return found, nil
}

func (r *Repository) ListDatabaseRows(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, query RowQuery) ([]domain.DatabaseRow, string, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil && actor.IsManagement() {
		return nil, "", err
	}
	schema, err := loadTableSchema(ctx, r.pool, projectID, databaseID, tableID)
	if err != nil {
		return nil, "", err
	}
	if err := authorizeDatabaseRowRead(schema, actor); err != nil {
		return nil, "", ErrForbidden
	}
	tableReadGranted := tablePermission(schema.Table.ReadPermissions, actor)
	indexKeys := make([]string, 0, len(query.Filters)+1)
	for _, filter := range query.Filters {
		indexKeys = append(indexKeys, filter.Column.Key)
	}
	if query.OrderBy != nil {
		indexKeys = append(indexKeys, query.OrderBy.Key)
	}
	if query.SearchColumn != nil {
		indexed, err := r.fullTextIndexedColumn(ctx, tableID, query.SearchColumn.Key)
		if err != nil {
			return nil, "", err
		}
		if !indexed {
			return nil, "", ErrUnindexedQuery
		}
	}
	if len(indexKeys) > 0 {
		indexed, err := r.indexedColumns(ctx, tableID, indexKeys)
		if err != nil {
			return nil, "", err
		}
		if !indexed {
			return nil, "", ErrUnindexedQuery
		}
	}
	args := []any{projectID, tableID}
	where := []string{"r.project_id=$1", "r.table_id=$2"}
	if actor.IsApplication() && schema.Table.RowSecurity && !tableReadGranted {
		where = append(where, rowPermissionSQL("r.read_permissions", actor, &args))
	}
	for _, filter := range query.Filters {
		args = append(args, filter.Value)
		where = append(where, rowSQLExpression("r", filter.Column)+"=$"+strconv.Itoa(len(args)))
	}
	if query.SearchColumn != nil && strings.TrimSpace(query.Search) != "" {
		args = append(args, strings.TrimSpace(query.Search))
		where = append(where, fullTextSQLExpression("r", *query.SearchColumn)+" @@ plainto_tsquery('simple', $"+strconv.Itoa(len(args))+")")
	}
	orderExpression := "r.id"
	if query.OrderBy != nil {
		orderExpression = rowSQLExpression("r", *query.OrderBy)
	}
	if query.Cursor != nil && query.Cursor.ID != uuid.Nil {
		op := ">"
		if query.Descending {
			op = "<"
		}
		if query.OrderBy == nil {
			args = append(args, query.Cursor.ID)
			where = append(where, "r.id "+op+" $"+strconv.Itoa(len(args)))
		} else {
			if query.Cursor.Value == nil {
				return nil, "", fmt.Errorf("%w: ordered cursor has no value", ErrInvalidQuery)
			}
			args = append(args, query.Cursor.Value)
			valueArg := strconv.Itoa(len(args))
			args = append(args, query.Cursor.ID)
			idArg := strconv.Itoa(len(args))
			where = append(where, "("+orderExpression+" "+op+" $"+valueArg+" OR ("+orderExpression+" = $"+valueArg+" AND r.id "+op+" $"+idArg+"))")
		}
	}
	direction := "ASC"
	if query.Descending {
		direction = "DESC"
	}
	if query.Limit < 1 || query.Limit > 100 {
		return nil, "", fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidQuery)
	}
	args = append(args, query.Limit+1)
	sql := `SELECT ` + rowProjection + ` FROM database_rows r WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + orderExpression + ` ` + direction + ` NULLS LAST, r.id ` + direction + ` LIMIT $` + strconv.Itoa(len(args))
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.DatabaseRow, 0, query.Limit)
	for rows.Next() {
		item, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > query.Limit {
		last := items[query.Limit-1]
		if query.OrderBy == nil {
			next = last.ID
		} else {
			next = EncodeRowCursor(RowCursor{ID: mustParseUUID(last.ID), Value: last.Data[query.OrderBy.Key]})
		}
		items = items[:query.Limit]
	}
	return items, next, nil
}

// StreamDatabaseRows emits rows in stable id order while applying the same
// table and row permission rules as ListDatabaseRows. The callback runs while
// the PostgreSQL cursor is open, so callers can write an export without first
// materialising the entire table in memory.
func (r *Repository) StreamDatabaseRows(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, limit int, emit func(domain.DatabaseRow) error) (int, error) {
	if limit < 1 || limit > DatabaseRowExportMaxLimit {
		return 0, fmt.Errorf("%w: export limit must be between 1 and %d", ErrInvalidQuery, DatabaseRowExportMaxLimit)
	}
	if emit == nil {
		return 0, fmt.Errorf("%w: export callback is required", ErrInvalidQuery)
	}
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil && actor.IsManagement() {
		return 0, err
	}
	schema, err := loadTableSchema(ctx, r.pool, projectID, databaseID, tableID)
	if err != nil {
		return 0, err
	}
	if err := authorizeDatabaseRowRead(schema, actor); err != nil {
		return 0, err
	}
	tableReadGranted := tablePermission(schema.Table.ReadPermissions, actor)
	args := []any{projectID, tableID}
	where := []string{"r.project_id=$1", "r.table_id=$2"}
	if actor.IsApplication() && schema.Table.RowSecurity && !tableReadGranted {
		where = append(where, rowPermissionSQL("r.read_permissions", actor, &args))
	}
	args = append(args, limit)
	query := `SELECT ` + rowProjection + ` FROM database_rows r WHERE ` + strings.Join(where, " AND ") + ` ORDER BY r.id ASC LIMIT $` + strconv.Itoa(len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		item, scanErr := scanRow(rows)
		if scanErr != nil {
			return count, scanErr
		}
		if callbackErr := emit(item); callbackErr != nil {
			return count, callbackErr
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	return count, nil
}

func (r *Repository) GetDatabaseRow(ctx context.Context, projectID, databaseID, tableID, rowID uuid.UUID, actor DatabaseActor) (domain.DatabaseRow, error) {
	if _, err := r.requireDatabaseRead(ctx, projectID, actor); err != nil && actor.IsManagement() {
		return domain.DatabaseRow{}, err
	}
	schema, err := loadTableSchema(ctx, r.pool, projectID, databaseID, tableID)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	if err := authorizeDatabaseRowRead(schema, actor); err != nil {
		return domain.DatabaseRow{}, err
	}
	tableReadGranted := tablePermission(schema.Table.ReadPermissions, actor)
	sql := `SELECT ` + rowProjection + ` FROM database_rows r WHERE r.project_id=$1 AND r.table_id=$2 AND r.id=$3`
	args := []any{projectID, tableID, rowID}
	if actor.IsApplication() && schema.Table.RowSecurity && !tableReadGranted {
		sql += ` AND ` + rowPermissionSQL("r.read_permissions", actor, &args)
	}
	item, err := scanRow(r.pool.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseRow{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) CreateDatabaseRow(ctx context.Context, id, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, input DatabaseRowInput) (domain.DatabaseRow, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return domain.DatabaseRow{}, err
	}
	schema, err := loadTableSchema(ctx, tx, projectID, databaseID, tableID)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	if err := authorizeRowOperationTx(ctx, tx, schema.Table, actor, "create"); err != nil {
		return domain.DatabaseRow{}, err
	}
	item, err := r.createDatabaseRowTx(ctx, tx, projectID, tableID, schema, id, actor, input)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DatabaseRow{}, err
	}
	return item, nil
}

func (r *Repository) CreateDatabaseRows(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, inputs []DatabaseBulkRowInput) ([]domain.DatabaseRow, error) {
	if len(inputs) == 0 || len(inputs) > DatabaseRowBulkImportMaxRows {
		return nil, fmt.Errorf("%w: import rows must contain between 1 and %d items", dbcore.ErrInvalidRow, DatabaseRowBulkImportMaxRows)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return nil, err
	}
	schema, err := loadTableSchema(ctx, tx, projectID, databaseID, tableID)
	if err != nil {
		return nil, err
	}
	if err := authorizeRowOperationTx(ctx, tx, schema.Table, actor, "create"); err != nil {
		return nil, err
	}
	items := make([]domain.DatabaseRow, 0, len(inputs))
	for _, input := range inputs {
		id := input.ID
		if id == uuid.Nil {
			id = uuid.Must(uuid.NewV7())
		}
		item, err := r.createDatabaseRowTx(ctx, tx, projectID, tableID, schema, id, actor, DatabaseRowInput{
			Data: input.Data, ReadPermissions: input.ReadPermissions,
			UpdatePermissions: input.UpdatePermissions, DeletePermissions: input.DeletePermissions,
		})
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// TransactDatabaseRows applies a bounded sequence of creates, updates, and
// deletes under one database transaction. The database namespace lock also
// serializes this batch with relationship creation and target-row deletion.
func (r *Repository) TransactDatabaseRows(ctx context.Context, projectID, databaseID, tableID uuid.UUID, actor DatabaseActor, operations []DatabaseRowTransactionOperation) (DatabaseRowTransactionResult, error) {
	if len(operations) == 0 || len(operations) > DatabaseRowTransactionMaxOps {
		return DatabaseRowTransactionResult{}, fmt.Errorf("%w: transaction operations must contain between 1 and %d items", dbcore.ErrInvalidRow, DatabaseRowTransactionMaxOps)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DatabaseRowTransactionResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return DatabaseRowTransactionResult{}, err
	}
	schema, err := loadTableSchema(ctx, tx, projectID, databaseID, tableID)
	if err != nil {
		return DatabaseRowTransactionResult{}, err
	}
	result := DatabaseRowTransactionResult{
		Rows:       make([]domain.DatabaseRow, 0, len(operations)),
		DeletedIDs: make([]string, 0),
	}
	for _, operation := range operations {
		switch operation.Action {
		case "create":
			if err := authorizeRowOperationTx(ctx, tx, schema.Table, actor, "create"); err != nil {
				return DatabaseRowTransactionResult{}, err
			}
			id := operation.ID
			if id == uuid.Nil {
				id = uuid.Must(uuid.NewV7())
			}
			item, err := r.createDatabaseRowTx(ctx, tx, projectID, tableID, schema, id, actor, DatabaseRowInput{
				Data: operation.Data, ReadPermissions: operation.ReadPermissions,
				UpdatePermissions: operation.UpdatePermissions, DeletePermissions: operation.DeletePermissions,
			})
			if err != nil {
				return DatabaseRowTransactionResult{}, err
			}
			result.Rows = append(result.Rows, item)
		case "update":
			if operation.ID == uuid.Nil {
				return DatabaseRowTransactionResult{}, fmt.Errorf("%w: update operation requires id", dbcore.ErrInvalidRow)
			}
			if err := authorizeRowOperationTx(ctx, tx, schema.Table, actor, "update"); err != nil {
				return DatabaseRowTransactionResult{}, err
			}
			item, err := r.updateDatabaseRowTx(ctx, tx, projectID, tableID, operation.ID, actor, schema, DatabaseRowPatch{
				Data: operation.Data, ReadPermissions: operation.ReadPermissions,
				UpdatePermissions: operation.UpdatePermissions, DeletePermissions: operation.DeletePermissions,
			})
			if err != nil {
				return DatabaseRowTransactionResult{}, err
			}
			result.Rows = append(result.Rows, item)
		case "delete":
			if operation.ID == uuid.Nil {
				return DatabaseRowTransactionResult{}, fmt.Errorf("%w: delete operation requires id", dbcore.ErrInvalidRow)
			}
			if err := authorizeRowOperationTx(ctx, tx, schema.Table, actor, "delete"); err != nil {
				return DatabaseRowTransactionResult{}, err
			}
			if err := r.deleteDatabaseRowTx(ctx, tx, projectID, tableID, operation.ID, actor, schema); err != nil {
				return DatabaseRowTransactionResult{}, err
			}
			result.DeletedIDs = append(result.DeletedIDs, operation.ID.String())
		default:
			return DatabaseRowTransactionResult{}, fmt.Errorf("%w: transaction action must be create, update, or delete", dbcore.ErrInvalidRow)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return DatabaseRowTransactionResult{}, err
	}
	return result, nil
}

func (r *Repository) createDatabaseRowTx(ctx context.Context, tx pgx.Tx, projectID, tableID uuid.UUID, schema DatabaseTableSchema, id uuid.UUID, actor DatabaseActor, input DatabaseRowInput) (domain.DatabaseRow, error) {
	data, err := dbcore.NormalizeCreate(input.Data, columnDefinitions(schema.Columns))
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	if err := validateDatabaseRowRelationshipsTx(ctx, tx, tableID, data); err != nil {
		return domain.DatabaseRow{}, err
	}
	if actor.Kind == DatabaseAnonymousActor && (input.ReadPermissions == nil || input.UpdatePermissions == nil || input.DeletePermissions == nil) {
		return domain.DatabaseRow{}, fmt.Errorf("%w: anonymous rows must specify read, update, and delete permissions", dbcore.ErrInvalidPermissions)
	}
	readPermissions, err := normalizeRowPermissions(input.ReadPermissions, actor, true)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	updatePermissions, err := normalizeRowPermissions(input.UpdatePermissions, actor, true)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	deletePermissions, err := normalizeRowPermissions(input.DeletePermissions, actor, true)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	var creator any
	if actor.Kind == DatabaseApplicationActor {
		creator = actor.ProjectUserID
	}
	item, err := scanRow(tx.QueryRow(ctx, `INSERT INTO database_rows (id,table_id,project_id,data,read_permissions,update_permissions,delete_permissions,creator_project_user_id) VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8) RETURNING `+rowProjectionNoAlias, id, tableID, projectID, dataJSON, readPermissions, updatePermissions, deletePermissions, creator))
	if err != nil {
		return domain.DatabaseRow{}, mapError(err)
	}
	changed := make([]string, 0, len(data))
	for key := range data {
		changed = append(changed, key)
	}
	metadata := buildDatabaseRowEventMetadata(actor, schema.Table, item.ReadPermissions, changed)
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_row.create", "database_row", id, metadata); err != nil {
		return domain.DatabaseRow{}, err
	}
	return item, nil
}

func (r *Repository) UpdateDatabaseRow(ctx context.Context, projectID, databaseID, tableID, rowID uuid.UUID, actor DatabaseActor, input DatabaseRowPatch) (domain.DatabaseRow, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return domain.DatabaseRow{}, err
	}
	schema, err := loadTableSchema(ctx, tx, projectID, databaseID, tableID)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	if err := authorizeRowOperationTx(ctx, tx, schema.Table, actor, "update"); err != nil {
		return domain.DatabaseRow{}, err
	}
	item, err := r.updateDatabaseRowTx(ctx, tx, projectID, tableID, rowID, actor, schema, input)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DatabaseRow{}, err
	}
	return item, nil
}

func (r *Repository) updateDatabaseRowTx(ctx context.Context, tx pgx.Tx, projectID, tableID, rowID uuid.UUID, actor DatabaseActor, schema DatabaseTableSchema, input DatabaseRowPatch) (domain.DatabaseRow, error) {
	selectSQL := `SELECT ` + rowProjection + ` FROM database_rows r WHERE r.project_id=$1 AND r.table_id=$2 AND r.id=$3`
	selectArgs := []any{projectID, tableID, rowID}
	if actor.IsApplication() && schema.Table.RowSecurity && !tablePermission(schema.Table.UpdatePermissions, actor) {
		selectSQL += ` AND ` + rowPermissionSQL("r.update_permissions", actor, &selectArgs)
	}
	selectSQL += ` FOR UPDATE`
	existing, err := scanRow(tx.QueryRow(ctx, selectSQL, selectArgs...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseRow{}, ErrNotFound
	}
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	data, changed, err := dbcore.NormalizeUpdate(existing.Data, input.Data, columnDefinitions(schema.Columns))
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	if err := validateDatabaseRowRelationshipsTx(ctx, tx, tableID, data); err != nil {
		return domain.DatabaseRow{}, err
	}
	readPermissions := existing.ReadPermissions
	updatePermissions := existing.UpdatePermissions
	deletePermissions := existing.DeletePermissions
	if actor.IsApplication() && (input.ReadPermissions != nil || input.UpdatePermissions != nil || input.DeletePermissions != nil) {
		return domain.DatabaseRow{}, ErrForbidden
	}
	if input.ReadPermissions != nil {
		readPermissions, err = normalizeRowPermissions(input.ReadPermissions, actor, false)
		if err != nil {
			return domain.DatabaseRow{}, err
		}
		changed = append(changed, "read_permissions")
	}
	if input.UpdatePermissions != nil {
		updatePermissions, err = normalizeRowPermissions(input.UpdatePermissions, actor, false)
		if err != nil {
			return domain.DatabaseRow{}, err
		}
		changed = append(changed, "update_permissions")
	}
	if input.DeletePermissions != nil {
		deletePermissions, err = normalizeRowPermissions(input.DeletePermissions, actor, false)
		if err != nil {
			return domain.DatabaseRow{}, err
		}
		changed = append(changed, "delete_permissions")
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return domain.DatabaseRow{}, err
	}
	item, err := scanRow(tx.QueryRow(ctx, `UPDATE database_rows SET data=$4::jsonb,read_permissions=$5,update_permissions=$6,delete_permissions=$7,updated_at=now() WHERE project_id=$1 AND table_id=$2 AND id=$3 RETURNING `+rowProjectionNoAlias, projectID, tableID, rowID, dataJSON, readPermissions, updatePermissions, deletePermissions))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DatabaseRow{}, ErrNotFound
	}
	if err != nil {
		return domain.DatabaseRow{}, mapError(err)
	}
	metadata := buildDatabaseRowEventMetadata(actor, schema.Table, item.ReadPermissions, changed)
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_row.update", "database_row", rowID, metadata); err != nil {
		return domain.DatabaseRow{}, err
	}
	return item, nil
}

func columnDefinitions(columns []DatabaseColumnSchema) []dbcore.ColumnDefinition {
	definitions := make([]dbcore.ColumnDefinition, 0, len(columns))
	for _, column := range columns {
		definitions = append(definitions, dbcore.ColumnDefinition{
			Key: column.Key, Type: column.Type, Required: column.Required,
			VarcharSize: column.VarcharSize, Default: column.Default, HasDefault: column.HasDefault,
		})
	}
	return definitions
}

func (r *Repository) DeleteDatabaseRow(ctx context.Context, projectID, databaseID, tableID, rowID uuid.UUID, actor DatabaseActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockDatabaseNamespace(ctx, tx, databaseID); err != nil {
		return err
	}
	schema, err := loadTableSchema(ctx, tx, projectID, databaseID, tableID)
	if err != nil {
		return err
	}
	if err := authorizeRowOperationTx(ctx, tx, schema.Table, actor, "delete"); err != nil {
		return err
	}
	if err := r.deleteDatabaseRowTx(ctx, tx, projectID, tableID, rowID, actor, schema); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) deleteDatabaseRowTx(ctx context.Context, tx pgx.Tx, projectID, tableID, rowID uuid.UUID, actor DatabaseActor, schema DatabaseTableSchema) error {
	selectSQL := `SELECT ` + rowProjection + ` FROM database_rows r WHERE r.project_id=$1 AND r.table_id=$2 AND r.id=$3`
	selectArgs := []any{projectID, tableID, rowID}
	if actor.IsApplication() && schema.Table.RowSecurity && !tablePermission(schema.Table.DeletePermissions, actor) {
		selectSQL += ` AND ` + rowPermissionSQL("r.delete_permissions", actor, &selectArgs)
	}
	item, err := scanRow(tx.QueryRow(ctx, selectSQL+` FOR UPDATE`, selectArgs...))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := ensureNoDatabaseRowReferencesTx(ctx, tx, tableID, rowID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM database_rows WHERE project_id=$1 AND table_id=$2 AND id=$3`, projectID, tableID, rowID); err != nil {
		return err
	}
	metadata := buildDatabaseRowEventMetadata(actor, schema.Table, item.ReadPermissions, nil)
	if err := r.auditDatabase(ctx, tx, projectID, actor, "database_row.delete", "database_row", rowID, metadata); err != nil {
		return err
	}
	return nil
}

func authorizeRowOperationTx(ctx context.Context, tx pgx.Tx, table domain.DatabaseTable, actor DatabaseActor, operation string) error {
	switch actor.Kind {
	case DatabaseConsoleActor:
		if operation != "read" {
			if actor.AccountID == uuid.Nil {
				return ErrForbidden
			}
			return requireProjectRoleTx(ctx, tx, mustParseUUID(table.ProjectID), actor.AccountID, "owner", "admin")
		}
		return requireProjectAccessTx(ctx, tx, mustParseUUID(table.ProjectID), actor.AccountID)
	case DatabaseAPIKeyActor:
		scope := "databases.read"
		if operation != "read" {
			scope = "databases.write"
		}
		if !apikey.HasScope(actor.APIKeyScopes, scope) {
			return ErrForbidden
		}
		return requireActiveProjectAPIKeyTx(ctx, tx, mustParseUUID(table.ProjectID), actor.APIKeyID, scope)
	case DatabaseApplicationActor, DatabaseAnonymousActor:
		var permissions []string
		switch operation {
		case "create":
			permissions = table.CreatePermissions
		case "read":
			permissions = table.ReadPermissions
		case "update":
			permissions = table.UpdatePermissions
		case "delete":
			permissions = table.DeletePermissions
		default:
			return ErrForbidden
		}
		if tablePermission(permissions, actor) {
			return nil
		}
		// With row security enabled a row grant may authorize update/delete
		// (and read) even when the table grant is absent. Creation is always
		// gated by the table's create permissions.
		if operation == "create" || !table.RowSecurity {
			return ErrForbidden
		}
		return nil
	default:
		return ErrForbidden
	}
}

func requireProjectAccessTx(ctx context.Context, tx pgx.Tx, projectID, accountID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects p JOIN organization_memberships m ON m.organization_id=p.organization_id WHERE p.id=$1 AND m.account_id=$2)`, projectID, accountID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}
