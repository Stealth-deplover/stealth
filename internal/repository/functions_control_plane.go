package repository

// Function control-plane persistence owns function metadata and variable
// management. Deployment and execution lifecycle SQL lives in sibling files
// so each persistence module has a smaller locality of reasoning.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) ListFunctions(ctx context.Context, projectID uuid.UUID, actor FunctionActor, limit int, cursor *uuid.UUID) ([]domain.Function, string, bool, error) {
	canManage, err := r.requireFunctionRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+functionProjection+` FROM project_functions WHERE project_id=$1 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $2`, projectID, limit+1, cursor)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.Function, 0, limit)
	for rows.Next() {
		item, scanErr := scanFunction(rows)
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

func (r *Repository) GetFunction(ctx context.Context, projectID, functionID uuid.UUID, actor FunctionActor) (domain.Function, error) {
	if _, err := r.requireFunctionRead(ctx, projectID, actor); err != nil {
		return domain.Function{}, err
	}
	return r.functionByID(ctx, r.pool, projectID, functionID, false)
}

func (r *Repository) CreateFunction(ctx context.Context, id, projectID uuid.UUID, actor FunctionActor, input FunctionInput) (domain.Function, error) {
	permissions, err := database.NormalizePermissions(input.ExecutePermissions)
	if err != nil {
		return domain.Function{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Function{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.Function{}, err
	}
	organizationID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return domain.Function{}, err
	}
	if err := r.enforceOrganizationLimitTx(ctx, tx, organizationID, "functions"); err != nil {
		return domain.Function{}, err
	}
	item, err := scanFunction(tx.QueryRow(ctx, `INSERT INTO project_functions (id,project_id,name,runtime,entrypoint,commands,timeout_seconds,enabled,logging,execute_permissions,description,status,artifact_quota_bytes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING `+functionProjection, id, projectID, input.Name, input.Runtime, input.Entrypoint, input.Commands, input.TimeoutSeconds, input.Enabled, input.Logging, permissions, input.Description, input.Status, input.ArtifactQuotaBytes))
	if err != nil {
		return domain.Function{}, mapError(err)
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function.create", "function", id, map[string]any{"name": input.Name, "runtime": input.Runtime}); err != nil {
		return domain.Function{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Function{}, err
	}
	return item, nil
}

func (r *Repository) UpdateFunction(ctx context.Context, projectID, functionID uuid.UUID, actor FunctionActor, patch FunctionPatch) (domain.Function, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Function{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.Function{}, err
	}
	existing, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		return domain.Function{}, err
	}
	name, runtime, entrypoint, commands, timeoutSeconds, enabled, logging := existing.Name, existing.Runtime, existing.Entrypoint, existing.Commands, existing.TimeoutSeconds, existing.Enabled, existing.Logging
	executePermissions := append([]string(nil), existing.ExecutePermissions...)
	description, status := existing.Description, existing.Status
	quota := existing.ArtifactQuotaBytes
	if patch.Name != nil {
		name = *patch.Name
	}
	if patch.Runtime != nil {
		runtime = *patch.Runtime
	}
	if patch.Entrypoint != nil {
		entrypoint = *patch.Entrypoint
	}
	if patch.Commands != nil {
		commands = *patch.Commands
	}
	if patch.TimeoutSeconds != nil {
		timeoutSeconds = *patch.TimeoutSeconds
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	if patch.Logging != nil {
		logging = *patch.Logging
	}
	if patch.ExecutePermissions != nil {
		executePermissions, err = database.NormalizePermissions(*patch.ExecutePermissions)
		if err != nil {
			return domain.Function{}, err
		}
	}
	if patch.Description != nil {
		description = patch.Description
	}
	if patch.Status != nil {
		status = *patch.Status
	}
	if patch.ArtifactQuotaBytes != nil {
		quota = *patch.ArtifactQuotaBytes
	}
	if patch.Status != nil && patch.Enabled == nil {
		enabled = status == "active"
	}
	if patch.Enabled != nil && patch.Status == nil {
		if enabled {
			status = "active"
		} else {
			status = "disabled"
		}
	}
	if (status == "active") != enabled {
		return domain.Function{}, ErrInvalidFunctionSettings
	}
	if quota <= 0 || quota < existing.ArtifactUsedBytes {
		return domain.Function{}, ErrFunctionQuotaExceeded
	}
	item, err := scanFunction(tx.QueryRow(ctx, `UPDATE project_functions SET name=$3,runtime=$4,entrypoint=$5,commands=$6,timeout_seconds=$7,enabled=$8,logging=$9,execute_permissions=$10,description=$11,status=$12,artifact_quota_bytes=$13,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING `+functionProjection, projectID, functionID, name, runtime, entrypoint, commands, timeoutSeconds, enabled, logging, executePermissions, description, status, quota))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Function{}, ErrNotFound
	}
	if err != nil {
		return domain.Function{}, mapError(err)
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function.update", "function", functionID, map[string]any{"changed_fields": functionChangedFields(patch)}); err != nil {
		return domain.Function{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Function{}, err
	}
	return item, nil
}

func functionChangedFields(patch FunctionPatch) []string {
	fields := make([]string, 0, 12)
	if patch.Name != nil {
		fields = append(fields, "name")
	}
	if patch.Runtime != nil {
		fields = append(fields, "runtime")
	}
	if patch.Entrypoint != nil {
		fields = append(fields, "entrypoint")
	}
	if patch.Commands != nil {
		fields = append(fields, "commands")
	}
	if patch.TimeoutSeconds != nil {
		fields = append(fields, "timeout_seconds")
	}
	if patch.Enabled != nil {
		fields = append(fields, "enabled")
	}
	if patch.Logging != nil {
		fields = append(fields, "logging")
	}
	if patch.ExecutePermissions != nil {
		fields = append(fields, "execute_permissions")
	}
	if patch.Description != nil {
		fields = append(fields, "description")
	}
	if patch.Status != nil {
		fields = append(fields, "status")
	}
	if patch.ArtifactQuotaBytes != nil {
		fields = append(fields, "artifact_quota_bytes")
	}
	sort.Strings(fields)
	return fields
}

// DeleteFunction removes metadata/accounting in one transaction and returns
// only opaque source/build artifact paths for post-commit filesystem cleanup.
func (r *Repository) DeleteFunction(ctx context.Context, projectID, functionID uuid.UUID, actor FunctionActor) ([]string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return nil, err
	}
	if _, err := r.functionByID(ctx, tx, projectID, functionID, true); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT source_path,build_path FROM function_deployments WHERE project_id=$1 AND function_id=$2 FOR UPDATE`, projectID, functionID)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for rows.Next() {
		var sourcePath string
		var buildPath *string
		if err := rows.Scan(&sourcePath, &buildPath); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, sourcePath)
		if buildPath != nil && strings.TrimSpace(*buildPath) != "" {
			paths = append(paths, *buildPath)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if _, err := tx.Exec(ctx, `DELETE FROM project_functions WHERE project_id=$1 AND id=$2`, projectID, functionID); err != nil {
		return nil, err
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function.delete", "function", functionID, map[string]any{"deployment_count": len(paths)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return paths, nil
}

func normalizeFunctionVariableInput(kind string, isSecret *bool) (string, bool, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		if isSecret != nil && *isSecret {
			kind = "secret"
		} else {
			kind = "variable"
		}
	}
	if kind != "variable" && kind != "secret" {
		return "", false, ErrInvalidFunctionVariable
	}
	secret := kind == "secret"
	if isSecret != nil && *isSecret != secret {
		return "", false, ErrInvalidFunctionVariable
	}
	return kind, secret, nil
}

func (r *Repository) ListFunctionVariables(ctx context.Context, projectID, functionID uuid.UUID, actor FunctionActor, limit int, cursor *uuid.UUID) ([]domain.FunctionVariable, string, bool, error) {
	canManage, err := r.requireFunctionRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+functionVariableProjection+` FROM function_variables WHERE project_id=$1 AND function_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, projectID, functionID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.FunctionVariable, 0, limit)
	for rows.Next() {
		item, scanErr := scanFunctionVariable(rows)
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

func (r *Repository) GetFunctionVariable(ctx context.Context, projectID, functionID, variableID uuid.UUID, actor FunctionActor) (domain.FunctionVariable, error) {
	if _, err := r.requireFunctionRead(ctx, projectID, actor); err != nil {
		return domain.FunctionVariable{}, err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return domain.FunctionVariable{}, err
	}
	item, err := scanFunctionVariable(r.pool.QueryRow(ctx, `SELECT `+functionVariableProjection+` FROM function_variables WHERE project_id=$1 AND function_id=$2 AND id=$3`, projectID, functionID, variableID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FunctionVariable{}, ErrNotFound
	}
	return item, err
}

func encryptFunctionVariableValue(value *string, cipher *functionsecret.Cipher) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	if len(*value) == 0 || len(*value) > functionVariableMaxValueBytes {
		return nil, ErrInvalidFunctionVariable
	}
	if cipher == nil {
		return nil, ErrFunctionSecretUnavailable
	}
	ciphertext, err := cipher.Encrypt([]byte(*value))
	if err != nil {
		return nil, fmt.Errorf("encrypt function variable: %w", err)
	}
	return ciphertext, nil
}

func (r *Repository) CreateFunctionVariable(ctx context.Context, id, projectID, functionID uuid.UUID, actor FunctionActor, input FunctionVariableInput) (domain.FunctionVariable, error) {
	if len(input.Key) == 0 || len(input.Key) > 128 || strings.ContainsRune(input.Key, '\x00') {
		return domain.FunctionVariable{}, ErrInvalidFunctionVariable
	}
	if input.Description != nil && (len(*input.Description) > functionVariableMaxDescriptionBytes || strings.ContainsRune(*input.Description, '\x00')) {
		return domain.FunctionVariable{}, ErrInvalidFunctionVariable
	}
	kind, secret, err := normalizeFunctionVariableInput(input.Kind, input.IsSecret)
	if err != nil {
		return domain.FunctionVariable{}, err
	}
	ciphertext, err := encryptFunctionVariableValue(input.Value, input.Cipher)
	if err != nil {
		return domain.FunctionVariable{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionVariable{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.FunctionVariable{}, err
	}
	if _, err := r.functionByID(ctx, tx, projectID, functionID, true); err != nil {
		return domain.FunctionVariable{}, err
	}
	item, err := scanFunctionVariable(tx.QueryRow(ctx, `INSERT INTO function_variables (id,function_id,project_id,key,kind,is_secret,value_ciphertext,description) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+functionVariableProjection, id, functionID, projectID, input.Key, kind, secret, ciphertext, input.Description))
	if err != nil {
		return domain.FunctionVariable{}, mapError(err)
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_variable.create", "function_variable", id, map[string]any{"key": input.Key, "kind": kind, "has_value": input.Value != nil}); err != nil {
		return domain.FunctionVariable{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionVariable{}, err
	}
	return item, nil
}

func (r *Repository) UpdateFunctionVariable(ctx context.Context, projectID, functionID, variableID uuid.UUID, actor FunctionActor, patch FunctionVariablePatch) (domain.FunctionVariable, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionVariable{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.FunctionVariable{}, err
	}
	if _, err := r.functionByID(ctx, tx, projectID, functionID, true); err != nil {
		return domain.FunctionVariable{}, err
	}
	var existing domain.FunctionVariable
	var oldCiphertext []byte
	err = tx.QueryRow(ctx, `SELECT `+functionVariableProjection+`,value_ciphertext FROM function_variables WHERE project_id=$1 AND function_id=$2 AND id=$3 FOR UPDATE`, projectID, functionID, variableID).Scan(&existing.ID, &existing.FunctionID, &existing.ProjectID, &existing.Key, &existing.Kind, &existing.IsSecret, &existing.HasValue, &existing.Description, &existing.CreatedAt, &existing.UpdatedAt, &oldCiphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FunctionVariable{}, ErrNotFound
	}
	if err != nil {
		return domain.FunctionVariable{}, err
	}
	kind := existing.Kind
	secret := existing.IsSecret
	if patch.Kind != nil || patch.IsSecret != nil {
		kindInput := kind
		if patch.Kind != nil {
			kindInput = *patch.Kind
		}
		kind, secret, err = normalizeFunctionVariableInput(kindInput, patch.IsSecret)
		if err != nil {
			return domain.FunctionVariable{}, err
		}
		if existing.IsSecret && !secret {
			return domain.FunctionVariable{}, ErrInvalidFunctionVariable
		}
	}
	key := existing.Key
	if patch.Key != nil {
		key = *patch.Key
	}
	if len(key) == 0 || len(key) > 120 || strings.ContainsRune(key, '\x00') {
		return domain.FunctionVariable{}, ErrInvalidFunctionVariable
	}
	description := existing.Description
	if patch.SetDescription {
		if patch.Description != nil && (len(*patch.Description) > functionVariableMaxDescriptionBytes || strings.ContainsRune(*patch.Description, '\x00')) {
			return domain.FunctionVariable{}, ErrInvalidFunctionVariable
		}
		description = patch.Description
	}
	value := oldCiphertext
	if patch.ClearValue {
		value = nil
	}
	if patch.SetValue {
		value, err = encryptFunctionVariableValue(patch.Value, patch.Cipher)
		if err != nil {
			return domain.FunctionVariable{}, err
		}
	}
	item, err := scanFunctionVariable(tx.QueryRow(ctx, `UPDATE function_variables SET key=$4,kind=$5,is_secret=$6,value_ciphertext=$7,description=$8,updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionVariableProjection, projectID, functionID, variableID, key, kind, secret, value, description))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FunctionVariable{}, ErrNotFound
	}
	if err != nil {
		return domain.FunctionVariable{}, mapError(err)
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_variable.update", "function_variable", variableID, map[string]any{"changed_fields": functionVariableChangedFields(patch), "key": key, "has_value": value != nil}); err != nil {
		return domain.FunctionVariable{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionVariable{}, err
	}
	return item, nil
}

func functionVariableChangedFields(patch FunctionVariablePatch) []string {
	fields := make([]string, 0, 5)
	if patch.Key != nil {
		fields = append(fields, "key")
	}
	if patch.Kind != nil || patch.IsSecret != nil {
		fields = append(fields, "kind")
	}
	if patch.SetValue || patch.ClearValue {
		fields = append(fields, "value")
	}
	if patch.SetDescription {
		fields = append(fields, "description")
	}
	sort.Strings(fields)
	return fields
}

func (r *Repository) DeleteFunctionVariable(ctx context.Context, projectID, functionID, variableID uuid.UUID, actor FunctionActor) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return err
	}
	if _, err := r.functionByID(ctx, tx, projectID, functionID, true); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM function_variables WHERE project_id=$1 AND function_id=$2 AND id=$3`, projectID, functionID, variableID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_variable.delete", "function_variable", variableID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
