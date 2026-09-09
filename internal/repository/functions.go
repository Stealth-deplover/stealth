package repository

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nazxf/stealth-api/internal/apikey"
	"github.com/nazxf/stealth-api/internal/database"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/functionsecret"
)

var (
	ErrFunctionQuotaExceeded     = errors.New("function artifact quota exceeded")
	ErrFunctionArtifactTooLarge  = errors.New("function source artifact is too large")
	ErrFunctionSecretUnavailable = errors.New("function secret encryption is unavailable")
	ErrInvalidFunctionVariable   = errors.New("invalid function variable")
	ErrInvalidFunctionTransition = errors.New("invalid function deployment transition")
	ErrFunctionDisabled          = errors.New("function is disabled")
	ErrDeploymentActive          = errors.New("active deployment cannot be deleted")
	ErrExecutionNotAvailable     = errors.New("function execution is not available")
	ErrNoExecutionJob            = errors.New("no function execution job available")
	ErrNoDeploymentJob           = errors.New("no function deployment build job available")
	ErrInvalidFunctionSettings   = errors.New("invalid function settings")
)

// Functions have the same management actor boundary as Database and Storage:
// Console sessions identify an accounts row, while API keys remain scoped to
// one project and never fabricate a Console account.
type FunctionActor = DatabaseActor

const (
	FunctionConsoleActor = DatabaseConsoleActor
	FunctionAPIKeyActor  = DatabaseAPIKeyActor
)

type FunctionInput struct {
	Name               string
	Runtime            string
	Entrypoint         string
	Commands           string
	TimeoutSeconds     int
	Enabled            bool
	Logging            bool
	ExecutePermissions []string
	Description        *string
	Status             string
	ArtifactQuotaBytes int64
}

type FunctionPatch struct {
	Name               *string
	Runtime            *string
	Entrypoint         *string
	Commands           *string
	TimeoutSeconds     *int
	Enabled            *bool
	Logging            *bool
	ExecutePermissions *[]string
	Description        *string
	Status             *string
	ArtifactQuotaBytes *int64
}

type FunctionVariableInput struct {
	Key         string
	Kind        string
	IsSecret    *bool
	Value       *string
	Description *string
	Cipher      *functionsecret.Cipher
}

type FunctionVariablePatch struct {
	Key            *string
	Kind           *string
	IsSecret       *bool
	Value          *string
	ClearValue     bool
	SetValue       bool
	Description    *string
	SetDescription bool
	Cipher         *functionsecret.Cipher
}

type FunctionDeploymentInput struct {
	Source             string
	SourceName         *string
	SizeBytes          int64
	ChecksumSHA256     string
	SourcePath         string
	CreatedByAccountID *uuid.UUID
	Activate           bool
}

// FunctionExecutionJob is the worker-only view of an execution. SourcePath
// is deliberately kept out of domain.FunctionDeployment: it is an internal
// storage locator and must never cross an HTTP/SDK boundary.
type FunctionExecutionJob struct {
	Execution           domain.FunctionExecution
	Function            domain.Function
	Deployment          domain.FunctionDeployment
	SourcePath          string
	BuildPath           string
	BuildChecksumSHA256 string
}

// FunctionBuildJob is the worker-only view of a deployment waiting for its
// immutable runtime artifact. SourcePath never crosses an HTTP or SDK
// boundary.
type FunctionBuildJob struct {
	Function   domain.Function
	Deployment domain.FunctionDeployment
	SourcePath string
}

type functionDeploymentVariableSnapshot struct {
	Key        string
	Kind       string
	IsSecret   bool
	Ciphertext []byte
}

// FunctionRuntimeVariable is materialized only inside the trusted worker.
// The plaintext value must never be returned by an HTTP handler or logged.
type FunctionRuntimeVariable struct {
	Key      string
	Value    string
	IsSecret bool
}

const functionProjection = `id,project_id,name,runtime,entrypoint,commands,timeout_seconds,enabled,logging,execute_permissions,description,status,artifact_quota_bytes,artifact_used_bytes,active_deployment_id,created_at,updated_at`
const functionVariableProjection = `id,function_id,project_id,key,kind,is_secret,(value_ciphertext IS NOT NULL),description,created_at,updated_at`
const functionDeploymentProjection = `id,function_id,project_id,version,source,source_name,size_bytes,checksum_sha256,status,build_status,error_message,created_by_account_id,queued_at,build_started_at,built_at,activated_at,finished_at,created_at,updated_at`
const functionBuildLogProjection = `id,deployment_id,function_id,project_id,sequence,level,message,created_at`
const functionExecutionProjection = `id,deployment_id,function_id,project_id,status,trigger,input_json,response_status,output_json,output_content_type,error_message,started_at,finished_at,created_at,updated_at`
const functionExecutionLogProjection = `id,execution_id,function_id,project_id,sequence,level,message,created_at`

const (
	functionVariableMaxValueBytes       = 64 * 1024
	functionVariableMaxDescriptionBytes = 2000
)

type functionScanner interface{ Scan(...any) error }

type functionVariableRows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

func scanFunction(row functionScanner) (domain.Function, error) {
	var item domain.Function
	var active *uuid.UUID
	err := row.Scan(&item.ID, &item.ProjectID, &item.Name, &item.Runtime, &item.Entrypoint, &item.Commands, &item.TimeoutSeconds, &item.Enabled, &item.Logging, &item.ExecutePermissions, &item.Description, &item.Status, &item.ArtifactQuotaBytes, &item.ArtifactUsedBytes, &active, &item.CreatedAt, &item.UpdatedAt)
	if err == nil && active != nil {
		value := active.String()
		item.ActiveDeploymentID = &value
	}
	return item, err
}

func scanFunctionVariable(row functionScanner) (domain.FunctionVariable, error) {
	var item domain.FunctionVariable
	return item, row.Scan(&item.ID, &item.FunctionID, &item.ProjectID, &item.Key, &item.Kind, &item.IsSecret, &item.HasValue, &item.Description, &item.CreatedAt, &item.UpdatedAt)
}

func scanFunctionDeployment(row functionScanner) (domain.FunctionDeployment, string, error) {
	var item domain.FunctionDeployment
	var sourcePath string
	var createdBy *uuid.UUID
	err := row.Scan(&item.ID, &item.FunctionID, &item.ProjectID, &item.Version, &item.Source, &item.SourceName, &item.SizeBytes, &item.ChecksumSHA256, &item.Status, &item.BuildStatus, &item.ErrorMessage, &createdBy, &item.QueuedAt, &item.BuildStartedAt, &item.BuiltAt, &item.ActivatedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt, &sourcePath)
	if err == nil && createdBy != nil {
		value := createdBy.String()
		item.CreatedByAccountID = &value
	}
	return item, sourcePath, err
}

// scanFunctionDeploymentRow expects the private source_path to be selected
// after the public projection. It is kept separate so callers cannot
// accidentally serialize source paths by scanning directly into a DTO.
func scanFunctionDeploymentPublic(row functionScanner) (domain.FunctionDeployment, error) {
	var item domain.FunctionDeployment
	var createdBy *uuid.UUID
	err := row.Scan(&item.ID, &item.FunctionID, &item.ProjectID, &item.Version, &item.Source, &item.SourceName, &item.SizeBytes, &item.ChecksumSHA256, &item.Status, &item.BuildStatus, &item.ErrorMessage, &createdBy, &item.QueuedAt, &item.BuildStartedAt, &item.BuiltAt, &item.ActivatedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt)
	if err == nil && createdBy != nil {
		value := createdBy.String()
		item.CreatedByAccountID = &value
	}
	return item, err
}

func scanFunctionBuildLog(row functionScanner) (domain.FunctionBuildLog, error) {
	var item domain.FunctionBuildLog
	return item, row.Scan(&item.ID, &item.DeploymentID, &item.FunctionID, &item.ProjectID, &item.Sequence, &item.Level, &item.Message, &item.CreatedAt)
}

func scanFunctionExecution(row functionScanner) (domain.FunctionExecution, error) {
	var item domain.FunctionExecution
	var input, output []byte
	err := row.Scan(&item.ID, &item.DeploymentID, &item.FunctionID, &item.ProjectID, &item.Status, &item.Trigger, &input, &item.ResponseStatus, &output, &item.OutputContentType, &item.ErrorMessage, &item.StartedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		item.InputJSON = append(item.InputJSON[:0], input...)
		item.OutputJSON = append(item.OutputJSON[:0], output...)
	}
	return item, err
}

func scanFunctionExecutionLog(row functionScanner) (domain.FunctionExecutionLog, error) {
	var item domain.FunctionExecutionLog
	return item, row.Scan(&item.ID, &item.ExecutionID, &item.FunctionID, &item.ProjectID, &item.Sequence, &item.Level, &item.Message, &item.CreatedAt)
}

func functionActorIsConsole(actor FunctionActor) bool { return actor.Kind == FunctionConsoleActor }
func functionActorIsAPIKey(actor FunctionActor) bool  { return actor.Kind == FunctionAPIKeyActor }

// requireFunctionRead returns canManage for the response capability field.
// Every branch verifies the project boundary before exposing metadata.
func (r *Repository) requireFunctionRead(ctx context.Context, projectID uuid.UUID, actor FunctionActor) (bool, error) {
	switch actor.Kind {
	case FunctionConsoleActor:
		role, err := r.projectRole(ctx, projectID, actor.AccountID)
		if err != nil {
			return false, err
		}
		return role == "owner" || role == "admin", nil
	case FunctionAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "functions.read") {
			return false, ErrForbidden
		}
		var active bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM project_api_keys
			WHERE id=$1 AND project_id=$2
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at>now())
			  AND 'functions.read' = ANY(scopes)
		)`, actor.APIKeyID, projectID).Scan(&active); err != nil {
			return false, err
		}
		if !active {
			return false, ErrNotFound
		}
		return apikey.HasScope(actor.APIKeyScopes, "functions.write"), nil
	default:
		return false, ErrForbidden
	}
}

func (r *Repository) requireFunctionWriteTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor FunctionActor) error {
	switch actor.Kind {
	case FunctionConsoleActor:
		return requireProjectRoleTx(ctx, tx, projectID, actor.AccountID, "owner", "admin")
	case FunctionAPIKeyActor:
		if !apikey.HasScope(actor.APIKeyScopes, "functions.write") {
			return ErrForbidden
		}
		return requireActiveProjectAPIKeyTx(ctx, tx, projectID, actor.APIKeyID, "functions.write")
	default:
		return ErrForbidden
	}
}

func (r *Repository) functionByID(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, functionID uuid.UUID, lock bool) (domain.Function, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	item, err := scanFunction(query.QueryRow(ctx, `SELECT `+functionProjection+` FROM project_functions WHERE project_id=$1 AND id=$2`+suffix, projectID, functionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Function{}, ErrNotFound
	}
	return item, err
}

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

func (r *Repository) functionDeploymentByID(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, projectID, functionID, deploymentID uuid.UUID, lock bool, includePath bool) (domain.FunctionDeployment, string, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	projection := functionDeploymentProjection
	if includePath {
		projection += `,source_path`
	}
	var item domain.FunctionDeployment
	var sourcePath string
	var createdBy *uuid.UUID
	args := []any{projectID, functionID, deploymentID}
	var err error
	if includePath {
		err = query.QueryRow(ctx, `SELECT `+projection+` FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND id=$3`+suffix, args...).Scan(&item.ID, &item.FunctionID, &item.ProjectID, &item.Version, &item.Source, &item.SourceName, &item.SizeBytes, &item.ChecksumSHA256, &item.Status, &item.BuildStatus, &item.ErrorMessage, &createdBy, &item.QueuedAt, &item.BuildStartedAt, &item.BuiltAt, &item.ActivatedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt, &sourcePath)
	} else {
		err = query.QueryRow(ctx, `SELECT `+projection+` FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND id=$3`+suffix, args...).Scan(&item.ID, &item.FunctionID, &item.ProjectID, &item.Version, &item.Source, &item.SourceName, &item.SizeBytes, &item.ChecksumSHA256, &item.Status, &item.BuildStatus, &item.ErrorMessage, &createdBy, &item.QueuedAt, &item.BuildStartedAt, &item.BuiltAt, &item.ActivatedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FunctionDeployment{}, "", ErrNotFound
	}
	if err != nil {
		return domain.FunctionDeployment{}, "", err
	}
	if createdBy != nil {
		value := createdBy.String()
		item.CreatedByAccountID = &value
	}
	return item, sourcePath, nil
}

func (r *Repository) ListFunctionDeployments(ctx context.Context, projectID, functionID uuid.UUID, actor FunctionActor, limit int, cursor *uuid.UUID) ([]domain.FunctionDeployment, string, bool, error) {
	canManage, err := r.requireFunctionRead(ctx, projectID, actor)
	if err != nil {
		return nil, "", false, err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return nil, "", false, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+functionDeploymentProjection+` FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, projectID, functionID, cursor, limit+1)
	if err != nil {
		return nil, "", false, err
	}
	defer rows.Close()
	items := make([]domain.FunctionDeployment, 0, limit)
	for rows.Next() {
		item, scanErr := scanFunctionDeploymentPublic(rows)
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

func (r *Repository) GetFunctionDeployment(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, actor FunctionActor) (domain.FunctionDeployment, error) {
	if _, err := r.requireFunctionRead(ctx, projectID, actor); err != nil {
		return domain.FunctionDeployment{}, err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return domain.FunctionDeployment{}, err
	}
	item, _, err := r.functionDeploymentByID(ctx, r.pool, projectID, functionID, deploymentID, false, false)
	return item, err
}

// CreateFunctionDeployment reserves per-function quota and assigns a
// monotonically increasing version while holding the function row lock. The
// upload is already atomically published by the caller. `Activate` is handled
// in the same transaction so the new active pointer cannot be observed before
// its metadata and quota reservation commit.
func (r *Repository) CreateFunctionDeployment(ctx context.Context, id, projectID, functionID uuid.UUID, actor FunctionActor, input FunctionDeploymentInput) (domain.FunctionDeployment, error) {
	if input.SizeBytes < 0 {
		return domain.FunctionDeployment{}, ErrFunctionArtifactTooLarge
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.FunctionDeployment{}, err
	}
	function, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if input.SizeBytes > function.ArtifactQuotaBytes-function.ArtifactUsedBytes {
		return domain.FunctionDeployment{}, ErrFunctionQuotaExceeded
	}
	var version int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM function_deployments WHERE project_id=$1 AND function_id=$2`, projectID, functionID).Scan(&version); err != nil {
		return domain.FunctionDeployment{}, err
	}
	item, err := scanFunctionDeploymentPublic(tx.QueryRow(ctx, `INSERT INTO function_deployments (id,function_id,project_id,version,source,source_name,size_bytes,checksum_sha256,source_path,status,build_status,created_by_account_id,runtime_snapshot,entrypoint_snapshot,commands_snapshot,timeout_seconds_snapshot,logging_snapshot) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'ready','queued',$10,$11,$12,$13,$14,$15) RETURNING `+functionDeploymentProjection, id, functionID, projectID, version, input.Source, input.SourceName, input.SizeBytes, input.ChecksumSHA256, input.SourcePath, input.CreatedByAccountID, function.Runtime, function.Entrypoint, function.Commands, function.TimeoutSeconds, function.Logging))
	if err != nil {
		return domain.FunctionDeployment{}, mapError(err)
	}
	variableRows, err := tx.Query(ctx, `SELECT key,kind,is_secret,value_ciphertext FROM function_variables WHERE project_id=$1 AND function_id=$2 ORDER BY key`, projectID, functionID)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	snapshots := make([]functionDeploymentVariableSnapshot, 0)
	for variableRows.Next() {
		var snapshot functionDeploymentVariableSnapshot
		if err := variableRows.Scan(&snapshot.Key, &snapshot.Kind, &snapshot.IsSecret, &snapshot.Ciphertext); err != nil {
			variableRows.Close()
			return domain.FunctionDeployment{}, err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := variableRows.Err(); err != nil {
		variableRows.Close()
		return domain.FunctionDeployment{}, err
	}
	variableRows.Close()
	for _, snapshot := range snapshots {
		snapshotID := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `INSERT INTO function_deployment_variables (id,deployment_id,function_id,project_id,key,kind,is_secret,value_ciphertext) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, snapshotID, id, functionID, projectID, snapshot.Key, snapshot.Kind, snapshot.IsSecret, snapshot.Ciphertext); err != nil {
			return domain.FunctionDeployment{}, mapError(err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE project_functions SET artifact_used_bytes=artifact_used_bytes+$3,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, functionID, input.SizeBytes); err != nil {
		return domain.FunctionDeployment{}, err
	}
	if input.Activate {
		if function.Status != "active" {
			return domain.FunctionDeployment{}, ErrFunctionDisabled
		}
		item, err = activateFunctionDeploymentTx(ctx, tx, projectID, functionID, id, actor, item)
		if err != nil {
			return domain.FunctionDeployment{}, err
		}
	}
	metadata := map[string]any{"version": item.Version, "source": input.Source, "size_bytes": input.SizeBytes, "checksum_sha256": input.ChecksumSHA256, "activated": input.Activate}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_deployment.create", "function_deployment", id, metadata); err != nil {
		return domain.FunctionDeployment{}, err
	}
	if input.Activate {
		if err := r.auditFunction(ctx, tx, projectID, actor, "function_deployment.activate", "function_deployment", id, map[string]any{"version": item.Version}); err != nil {
			return domain.FunctionDeployment{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionDeployment{}, err
	}
	return item, nil
}

func activateFunctionDeploymentTx(ctx context.Context, tx pgx.Tx, projectID, functionID, deploymentID uuid.UUID, actor FunctionActor, item domain.FunctionDeployment) (domain.FunctionDeployment, error) {
	var current *uuid.UUID
	var functionStatus string
	if err := tx.QueryRow(ctx, `SELECT active_deployment_id,status FROM project_functions WHERE project_id=$1 AND id=$2 FOR UPDATE`, projectID, functionID).Scan(&current, &functionStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.FunctionDeployment{}, ErrNotFound
		}
		return domain.FunctionDeployment{}, err
	}
	if functionStatus != "active" {
		return domain.FunctionDeployment{}, ErrFunctionDisabled
	}
	locked, _, err := (&Repository{pool: nil}).functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if locked.Status == "active" && current != nil && *current == deploymentID {
		return locked, nil
	}
	if locked.Status != "ready" {
		return domain.FunctionDeployment{}, ErrInvalidFunctionTransition
	}
	if current != nil && *current != deploymentID {
		if _, err := tx.Exec(ctx, `UPDATE function_deployments SET status='superseded',finished_at=now(),updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 AND status='active'`, projectID, functionID, *current); err != nil {
			return domain.FunctionDeployment{}, err
		}
	}
	updated, _, err := (&Repository{pool: nil}).functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	updated, err = scanFunctionDeploymentPublic(tx.QueryRow(ctx, `UPDATE function_deployments SET status='active',activated_at=COALESCE(activated_at,now()),updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionDeploymentProjection, projectID, functionID, deploymentID))
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_functions SET active_deployment_id=$3,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, functionID, deploymentID); err != nil {
		return domain.FunctionDeployment{}, err
	}
	_ = actor
	return updated, nil
}

// functionDeploymentByIDTx is the transaction variant used by activation and
// worker state transitions. Keeping the project and function predicates in
// every query makes cross-tenant IDs fail closed.
func (r *Repository) functionDeploymentByIDTx(ctx context.Context, tx pgx.Tx, projectID, functionID, deploymentID uuid.UUID, lock bool) (domain.FunctionDeployment, string, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var item domain.FunctionDeployment
	var createdBy *uuid.UUID
	var path string
	err := tx.QueryRow(ctx, `SELECT `+functionDeploymentProjection+`,source_path FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND id=$3`+suffix, projectID, functionID, deploymentID).Scan(&item.ID, &item.FunctionID, &item.ProjectID, &item.Version, &item.Source, &item.SourceName, &item.SizeBytes, &item.ChecksumSHA256, &item.Status, &item.BuildStatus, &item.ErrorMessage, &createdBy, &item.QueuedAt, &item.BuildStartedAt, &item.BuiltAt, &item.ActivatedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt, &path)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FunctionDeployment{}, "", ErrNotFound
	}
	if err != nil {
		return domain.FunctionDeployment{}, "", err
	}
	if createdBy != nil {
		value := createdBy.String()
		item.CreatedByAccountID = &value
	}
	return item, path, nil
}

type functionDeploymentBuildStorage struct {
	BuildPath           string
	BuildSizeBytes      int64
	BuildChecksumSHA256 string
	BuildWorkerID       string
}

func (r *Repository) functionDeploymentBuildStorageTx(ctx context.Context, tx pgx.Tx, projectID, functionID, deploymentID uuid.UUID, lock bool) (functionDeploymentBuildStorage, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var path, checksum, workerID *string
	var size int64
	err := tx.QueryRow(ctx, `SELECT build_path,build_size_bytes,build_checksum_sha256,build_worker_id FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND id=$3`+suffix, projectID, functionID, deploymentID).Scan(&path, &size, &checksum, &workerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return functionDeploymentBuildStorage{}, ErrNotFound
	}
	if err != nil {
		return functionDeploymentBuildStorage{}, err
	}
	storage := functionDeploymentBuildStorage{BuildSizeBytes: size}
	if path != nil {
		storage.BuildPath = *path
	}
	if checksum != nil {
		storage.BuildChecksumSHA256 = *checksum
	}
	if workerID != nil {
		storage.BuildWorkerID = *workerID
	}
	return storage, nil
}

// functionDeploymentRuntimeConfigTx overlays immutable deployment settings
// onto the current function identity. Enabled/status and permissions remain
// live controls, while runtime/entrypoint/commands/timeout/logging cannot
// drift after a deployment has been built.
func (r *Repository) functionDeploymentRuntimeConfigTx(ctx context.Context, tx pgx.Tx, projectID, functionID, deploymentID uuid.UUID, function domain.Function) (domain.Function, error) {
	var runtime, entrypoint, commands string
	var timeout int
	var logging bool
	err := tx.QueryRow(ctx, `SELECT runtime_snapshot,entrypoint_snapshot,commands_snapshot,timeout_seconds_snapshot,logging_snapshot FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND id=$3`, projectID, functionID, deploymentID).Scan(&runtime, &entrypoint, &commands, &timeout, &logging)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Function{}, ErrNotFound
	}
	if err != nil {
		return domain.Function{}, err
	}
	function.Runtime = runtime
	function.Entrypoint = entrypoint
	function.Commands = commands
	function.TimeoutSeconds = timeout
	function.Logging = logging
	return function, nil
}

// FunctionDeploymentStoragePaths returns opaque source/build paths for a
// post-commit filesystem cleanup. It is intentionally separate from the
// public deployment projection.
func (r *Repository) FunctionDeploymentStoragePaths(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, actor FunctionActor) ([]string, error) {
	if _, err := r.requireFunctionRead(ctx, projectID, actor); err != nil {
		return nil, err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return nil, err
	}
	var sourcePath string
	var buildPath *string
	err := r.pool.QueryRow(ctx, `SELECT source_path,build_path FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND id=$3`, projectID, functionID, deploymentID).Scan(&sourcePath, &buildPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	paths := []string{sourcePath}
	if buildPath != nil && strings.TrimSpace(*buildPath) != "" {
		paths = append(paths, *buildPath)
	}
	return paths, nil
}

// ClaimNextFunctionDeployment leases one source deployment for building. A
// deployment may already be active because activation is allowed to be
// requested before the asynchronous build completes; invocations remain
// blocked until build_status becomes succeeded.
func (r *Repository) ClaimNextFunctionDeployment(ctx context.Context, workerID string) (FunctionBuildJob, error) {
	if !validFunctionWorkerID(workerID) {
		return FunctionBuildJob{}, ErrInvalidFunctionSettings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return FunctionBuildJob{}, err
	}
	defer tx.Rollback(ctx)
	var deploymentID, projectID, functionID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT d.id,d.project_id,d.function_id
		FROM function_deployments d
		JOIN project_functions f ON f.id=d.function_id AND f.project_id=d.project_id
		WHERE d.status IN ('ready','active') AND d.build_status IN ('queued','deferred')
		ORDER BY d.queued_at,d.id
		LIMIT 1
		FOR UPDATE OF d SKIP LOCKED`).Scan(&deploymentID, &projectID, &functionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return FunctionBuildJob{}, ErrNoDeploymentJob
	}
	if err != nil {
		return FunctionBuildJob{}, err
	}
	function, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		return FunctionBuildJob{}, err
	}
	deployment, sourcePath, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return FunctionBuildJob{}, err
	}
	if deployment.BuildStatus != "queued" && deployment.BuildStatus != "deferred" {
		return FunctionBuildJob{}, ErrNoDeploymentJob
	}
	function, err = r.functionDeploymentRuntimeConfigTx(ctx, tx, projectID, functionID, deploymentID, function)
	if err != nil {
		return FunctionBuildJob{}, err
	}
	deployment, err = scanFunctionDeploymentPublic(tx.QueryRow(ctx, `UPDATE function_deployments SET build_status='running',build_started_at=now(),build_worker_id=$4,updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 AND build_status IN ('queued','deferred') RETURNING `+functionDeploymentProjection, projectID, functionID, deploymentID, workerID))
	if err != nil {
		return FunctionBuildJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FunctionBuildJob{}, err
	}
	return FunctionBuildJob{Function: function, Deployment: deployment, SourcePath: sourcePath}, nil
}

// RequeueStaleFunctionDeployments makes a crashed builder's work available to
// another worker. It does not alter activation status, only build lease data.
func (r *Repository) RequeueStaleFunctionDeployments(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, ErrInvalidFunctionSettings
	}
	result, err := r.pool.Exec(ctx, `UPDATE function_deployments SET build_status='deferred',build_started_at=NULL,build_worker_id=NULL,updated_at=now() WHERE build_status='running' AND build_started_at IS NOT NULL AND build_started_at < now() - ($1::double precision * interval '1 second')`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// CompleteFunctionDeploymentBuild publishes the worker-produced immutable
// archive and adjusts artifact quota for its final size in one transaction.
func (r *Repository) CompleteFunctionDeploymentBuild(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, workerID, buildPath string, buildSizeBytes int64, buildChecksumSHA256 string) (domain.FunctionDeployment, error) {
	if !validFunctionWorkerID(workerID) || !validFunctionArtifactPath(buildPath) || buildSizeBytes <= 0 || !validSHA256(buildChecksumSHA256) {
		return domain.FunctionDeployment{}, ErrInvalidFunctionSettings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	defer tx.Rollback(ctx)
	function, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	item, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	storage, err := r.functionDeploymentBuildStorageTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if item.BuildStatus != "running" || storage.BuildWorkerID != workerID {
		return domain.FunctionDeployment{}, ErrExecutionNotAvailable
	}
	if storage.BuildSizeBytes > function.ArtifactUsedBytes {
		return domain.FunctionDeployment{}, ErrInvalidFunctionSettings
	}
	newUsedBytes := function.ArtifactUsedBytes - storage.BuildSizeBytes + buildSizeBytes
	if newUsedBytes > function.ArtifactQuotaBytes {
		return domain.FunctionDeployment{}, ErrFunctionQuotaExceeded
	}
	if _, err := tx.Exec(ctx, `UPDATE project_functions SET artifact_used_bytes=$3,updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, functionID, newUsedBytes); err != nil {
		return domain.FunctionDeployment{}, err
	}
	updated, err := scanFunctionDeploymentPublic(tx.QueryRow(ctx, `UPDATE function_deployments SET build_path=$4,build_size_bytes=$5,build_checksum_sha256=$6,build_status='succeeded',build_worker_id=NULL,error_message=NULL,built_at=COALESCE(built_at,now()),updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionDeploymentProjection, projectID, functionID, deploymentID, buildPath, buildSizeBytes, strings.ToLower(buildChecksumSHA256)))
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionDeployment{}, err
	}
	return updated, nil
}

// FailFunctionDeploymentBuild records a bounded build failure while releasing
// the builder lease. A failed build is not executable and must be replaced by
// a new deployment; the previously active deployment remains represented by
// the function pointer until an operator activates another ready build.
func (r *Repository) FailFunctionDeploymentBuild(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, workerID, errorMessage string) (domain.FunctionDeployment, error) {
	if !validFunctionWorkerID(workerID) {
		return domain.FunctionDeployment{}, ErrInvalidFunctionSettings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := r.functionByID(ctx, tx, projectID, functionID, true); err != nil {
		return domain.FunctionDeployment{}, err
	}
	item, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	storage, err := r.functionDeploymentBuildStorageTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if item.BuildStatus != "running" || storage.BuildWorkerID != workerID {
		return domain.FunctionDeployment{}, ErrExecutionNotAvailable
	}
	failureMessage := normalizeFunctionBuildError(errorMessage)
	// Invocations accepted while the build was queued must not remain stuck
	// forever when the immutable artifact cannot be produced. They are fenced
	// to this deployment and transition atomically with its failed state.
	failedExecutions, err := tx.Query(ctx, `UPDATE function_executions SET status='failed',error_message=$4,finished_at=now(),updated_at=now() WHERE project_id=$1 AND function_id=$2 AND deployment_id=$3 AND status='accepted' RETURNING started_at,finished_at`, projectID, functionID, deploymentID, failureMessage)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	for failedExecutions.Next() {
		var startedAt, finishedAt *time.Time
		if err := failedExecutions.Scan(&startedAt, &finishedAt); err != nil {
			failedExecutions.Close()
			return domain.FunctionDeployment{}, err
		}
		if finishedAt == nil {
			continue
		}
		delta := UsageDelta{FunctionFailureCount: 1}
		if startedAt != nil {
			if computeMS := finishedAt.Sub(*startedAt).Milliseconds(); computeMS > 0 {
				delta.FunctionComputeMS = computeMS
			}
		}
		if err := incrementUsageTx(ctx, tx, projectID, *finishedAt, delta); err != nil {
			failedExecutions.Close()
			return domain.FunctionDeployment{}, err
		}
	}
	if err := failedExecutions.Err(); err != nil {
		failedExecutions.Close()
		return domain.FunctionDeployment{}, err
	}
	failedExecutions.Close()
	updated, err := scanFunctionDeploymentPublic(tx.QueryRow(ctx, `UPDATE function_deployments SET build_status='failed',build_worker_id=NULL,error_message=$4,updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionDeploymentProjection, projectID, functionID, deploymentID, failureMessage))
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionDeployment{}, err
	}
	return updated, nil
}

func normalizeFunctionBuildError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "function build failed"
	}
	if len(value) > 4000 {
		return value[:4000]
	}
	return value
}

func validFunctionArtifactPath(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		parsed, err := uuid.Parse(part)
		if err != nil || parsed.Version() != uuid.Version(7) {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (r *Repository) ActivateFunctionDeployment(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, actor FunctionActor) (domain.FunctionDeployment, domain.Function, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionDeployment{}, domain.Function{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.FunctionDeployment{}, domain.Function{}, err
	}
	item, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, false)
	if err != nil {
		return domain.FunctionDeployment{}, domain.Function{}, err
	}
	item, err = activateFunctionDeploymentTx(ctx, tx, projectID, functionID, deploymentID, actor, item)
	if err != nil {
		return domain.FunctionDeployment{}, domain.Function{}, err
	}
	function, err := r.functionByID(ctx, tx, projectID, functionID, false)
	if err != nil {
		return domain.FunctionDeployment{}, domain.Function{}, err
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_deployment.activate", "function_deployment", deploymentID, map[string]any{"version": item.Version}); err != nil {
		return domain.FunctionDeployment{}, domain.Function{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionDeployment{}, domain.Function{}, err
	}
	return item, function, nil
}

// DeleteFunctionDeployment preserves the original single-path return for
// callers that only know about source artifacts. New callers should use
// DeleteFunctionDeploymentWithArtifacts so source and build bytes are cleaned
// up atomically with the metadata deletion.
func (r *Repository) DeleteFunctionDeployment(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, actor FunctionActor) (string, error) {
	paths, err := r.DeleteFunctionDeploymentWithArtifacts(ctx, projectID, functionID, deploymentID, actor)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// DeleteFunctionDeploymentWithArtifacts removes metadata/accounting in one
// transaction and returns only opaque source/build paths for post-commit
// filesystem cleanup.
func (r *Repository) DeleteFunctionDeploymentWithArtifacts(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, actor FunctionActor) ([]string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return nil, err
	}
	function, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		return nil, err
	}
	item, path, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return nil, err
	}
	if (function.ActiveDeploymentID != nil && *function.ActiveDeploymentID == deploymentID.String()) || item.Status == "active" {
		return nil, ErrDeploymentActive
	}
	buildStorage, err := r.functionDeploymentBuildStorageTx(ctx, tx, projectID, functionID, deploymentID, false)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND id=$3`, projectID, functionID, deploymentID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_functions SET artifact_used_bytes=GREATEST(0,artifact_used_bytes-$3),updated_at=now() WHERE project_id=$1 AND id=$2`, projectID, functionID, item.SizeBytes+buildStorage.BuildSizeBytes); err != nil {
		return nil, err
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_deployment.delete", "function_deployment", deploymentID, map[string]any{"version": item.Version, "size_bytes": item.SizeBytes}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	paths := []string{path}
	if strings.TrimSpace(buildStorage.BuildPath) != "" {
		paths = append(paths, buildStorage.BuildPath)
	}
	return paths, nil
}

// TransitionFunctionDeployment is an internal builder boundary. It performs
// no source extraction or process execution; a trusted builder can call it
// after doing that work outside this API process.
func (r *Repository) TransitionFunctionDeployment(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, next, errorMessage string) (domain.FunctionDeployment, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	defer tx.Rollback(ctx)
	item, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if !validFunctionDeploymentTransition(item.Status, next) {
		return domain.FunctionDeployment{}, ErrInvalidFunctionTransition
	}
	var updated domain.FunctionDeployment
	switch next {
	case "building":
		updated, err = scanFunctionDeploymentPublic(tx.QueryRow(ctx, `UPDATE function_deployments SET status='building',build_status='running',build_started_at=COALESCE(build_started_at,now()),updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionDeploymentProjection, projectID, functionID, deploymentID))
	case "ready":
		updated, err = scanFunctionDeploymentPublic(tx.QueryRow(ctx, `UPDATE function_deployments SET status='ready',build_status='succeeded',built_at=COALESCE(built_at,now()),error_message=NULL,updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionDeploymentProjection, projectID, functionID, deploymentID))
	case "failed":
		updated, err = scanFunctionDeploymentPublic(tx.QueryRow(ctx, `UPDATE function_deployments SET status='failed',build_status='failed',error_message=$4,finished_at=now(),updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionDeploymentProjection, projectID, functionID, deploymentID, nullableError(errorMessage)))
	case "cancelled":
		updated, err = scanFunctionDeploymentPublic(tx.QueryRow(ctx, `UPDATE function_deployments SET status='cancelled',finished_at=now(),updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionDeploymentProjection, projectID, functionID, deploymentID))
	default:
		return domain.FunctionDeployment{}, ErrInvalidFunctionTransition
	}
	if err != nil {
		return domain.FunctionDeployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionDeployment{}, err
	}
	return updated, nil
}

func validFunctionDeploymentTransition(current, next string) bool {
	switch current {
	case "queued":
		return next == "building" || next == "failed" || next == "cancelled" || next == "ready"
	case "building":
		return next == "ready" || next == "failed" || next == "cancelled"
	case "ready":
		return next == "active" || next == "cancelled"
	case "active":
		return next == "superseded"
	default:
		return false
	}
}

func nullableError(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}

func (r *Repository) AppendFunctionBuildLog(ctx context.Context, projectID, functionID, deploymentID, id uuid.UUID, sequence int64, level, message string) (domain.FunctionBuildLog, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionBuildLog{}, err
	}
	defer tx.Rollback(ctx)
	if sequence <= 0 {
		if _, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true); err != nil {
			return domain.FunctionBuildLog{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM function_build_logs WHERE project_id=$1 AND deployment_id=$2`, projectID, deploymentID).Scan(&sequence); err != nil {
			return domain.FunctionBuildLog{}, err
		}
	} else if _, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, false); err != nil {
		return domain.FunctionBuildLog{}, err
	}
	item, err := scanFunctionBuildLog(tx.QueryRow(ctx, `INSERT INTO function_build_logs (id,deployment_id,function_id,project_id,sequence,level,message) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+functionBuildLogProjection, id, deploymentID, functionID, projectID, sequence, level, message))
	if err != nil {
		return domain.FunctionBuildLog{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionBuildLog{}, err
	}
	return item, nil
}

func (r *Repository) ListFunctionBuildLogs(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, actor FunctionActor, limit int, after int64) ([]domain.FunctionBuildLog, error) {
	if _, err := r.requireFunctionRead(ctx, projectID, actor); err != nil {
		return nil, err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+functionBuildLogProjection+` FROM function_build_logs WHERE project_id=$1 AND function_id=$2 AND deployment_id=$3 AND sequence>$4 ORDER BY sequence LIMIT $5`, projectID, functionID, deploymentID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.FunctionBuildLog, 0, limit)
	for rows.Next() {
		item, scanErr := scanFunctionBuildLog(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CreateFunctionExecution is the deployment-pinned enqueue primitive used by
// internal callers. It only records accepted work; the worker owns execution.
func (r *Repository) CreateFunctionExecution(ctx context.Context, id, projectID, functionID, deploymentID uuid.UUID, trigger string) (domain.FunctionExecution, error) {
	return r.CreateFunctionExecutionWithInput(ctx, id, projectID, functionID, deploymentID, trigger, json.RawMessage(`{}`))
}

func (r *Repository) CreateFunctionExecutionWithInput(ctx context.Context, id, projectID, functionID, deploymentID uuid.UUID, trigger string, input json.RawMessage) (domain.FunctionExecution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	defer tx.Rollback(ctx)
	function, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	if function.ActiveDeploymentID == nil || *function.ActiveDeploymentID != deploymentID.String() {
		return domain.FunctionExecution{}, ErrExecutionNotAvailable
	}
	if !function.Enabled || function.Status != "active" {
		return domain.FunctionExecution{}, ErrFunctionDisabled
	}
	item, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, false)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	if item.Status != "active" || item.BuildStatus == "failed" {
		return domain.FunctionExecution{}, ErrExecutionNotAvailable
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if len(input) > 65536 || !json.Valid(input) {
		return domain.FunctionExecution{}, ErrInvalidFunctionSettings
	}
	if !validFunctionExecutionTrigger(trigger) {
		return domain.FunctionExecution{}, ErrInvalidFunctionSettings
	}
	execution, err := scanFunctionExecution(tx.QueryRow(ctx, `INSERT INTO function_executions (id,deployment_id,function_id,project_id,trigger,input_json) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+functionExecutionProjection, id, deploymentID, functionID, projectID, trigger, []byte(input)))
	if err != nil {
		return domain.FunctionExecution{}, mapError(err)
	}
	if err := incrementUsageTx(ctx, tx, projectID, execution.CreatedAt, UsageDelta{FunctionInvocationCount: 1}); err != nil {
		return domain.FunctionExecution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionExecution{}, err
	}
	return execution, nil
}

// CreateFunctionExecutionForActor is the management-plane invocation path.
// It resolves the currently active deployment while holding the function row
// lock, so a concurrent activation cannot route an invocation to an old
// deployment. API-key callers must have functions.write.
func (r *Repository) CreateFunctionExecutionForActor(ctx context.Context, id, projectID, functionID uuid.UUID, actor FunctionActor, trigger string, input json.RawMessage) (domain.FunctionExecution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	defer tx.Rollback(ctx)
	if err := r.requireFunctionWriteTx(ctx, tx, projectID, actor); err != nil {
		return domain.FunctionExecution{}, err
	}
	function, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	if function.ActiveDeploymentID == nil {
		return domain.FunctionExecution{}, ErrExecutionNotAvailable
	}
	deploymentID, err := uuid.Parse(*function.ActiveDeploymentID)
	if err != nil {
		return domain.FunctionExecution{}, ErrExecutionNotAvailable
	}
	if !function.Enabled || function.Status != "active" {
		return domain.FunctionExecution{}, ErrFunctionDisabled
	}
	deployment, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, false)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	if deployment.Status != "active" || deployment.BuildStatus == "failed" {
		return domain.FunctionExecution{}, ErrExecutionNotAvailable
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if len(input) > 65536 || !json.Valid(input) || !validFunctionExecutionTrigger(trigger) {
		return domain.FunctionExecution{}, ErrInvalidFunctionSettings
	}
	execution, err := scanFunctionExecution(tx.QueryRow(ctx, `INSERT INTO function_executions (id,deployment_id,function_id,project_id,trigger,input_json) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+functionExecutionProjection, id, deploymentID, functionID, projectID, trigger, []byte(input)))
	if err != nil {
		return domain.FunctionExecution{}, mapError(err)
	}
	if err := incrementUsageTx(ctx, tx, projectID, execution.CreatedAt, UsageDelta{FunctionInvocationCount: 1}); err != nil {
		return domain.FunctionExecution{}, err
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_execution.accept", "function_execution", id, map[string]any{"trigger": trigger, "deployment_id": deploymentID}); err != nil {
		return domain.FunctionExecution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionExecution{}, err
	}
	return execution, nil
}

// CreateFunctionExecutionForApplication is the public project-user path. The
// function's execute_permissions are evaluated inside the same transaction
// that resolves the active deployment, preventing a permission or activation
// race from routing an invocation unexpectedly.
func (r *Repository) CreateFunctionExecutionForApplication(ctx context.Context, id, projectID, functionID uuid.UUID, projectUserID *uuid.UUID, trigger string, input json.RawMessage) (domain.FunctionExecution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	defer tx.Rollback(ctx)
	function, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	actor := DatabaseActor{Kind: DatabaseAnonymousActor}
	permissionActor := database.Actor{}
	if projectUserID != nil && *projectUserID != uuid.Nil {
		actor.Kind = DatabaseApplicationActor
		actor.ProjectUserID = *projectUserID
		permissionActor = database.Actor{Authenticated: true, UserID: *projectUserID}
	}
	if !function.Enabled || function.Status != "active" {
		return domain.FunctionExecution{}, ErrFunctionDisabled
	}
	if !database.Grants(function.ExecutePermissions, permissionActor) {
		return domain.FunctionExecution{}, ErrForbidden
	}
	if function.ActiveDeploymentID == nil {
		return domain.FunctionExecution{}, ErrExecutionNotAvailable
	}
	deploymentID, err := uuid.Parse(*function.ActiveDeploymentID)
	if err != nil {
		return domain.FunctionExecution{}, ErrExecutionNotAvailable
	}
	deployment, _, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, false)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	if deployment.Status != "active" || deployment.BuildStatus == "failed" {
		return domain.FunctionExecution{}, ErrExecutionNotAvailable
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if len(input) > 65536 || !json.Valid(input) || !validFunctionExecutionTrigger(trigger) {
		return domain.FunctionExecution{}, ErrInvalidFunctionSettings
	}
	execution, err := scanFunctionExecution(tx.QueryRow(ctx, `INSERT INTO function_executions (id,deployment_id,function_id,project_id,trigger,input_json) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+functionExecutionProjection, id, deploymentID, functionID, projectID, trigger, []byte(input)))
	if err != nil {
		return domain.FunctionExecution{}, mapError(err)
	}
	if err := incrementUsageTx(ctx, tx, projectID, execution.CreatedAt, UsageDelta{FunctionInvocationCount: 1}); err != nil {
		return domain.FunctionExecution{}, err
	}
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_execution.accept", "function_execution", id, map[string]any{"trigger": trigger, "deployment_id": deploymentID}); err != nil {
		return domain.FunctionExecution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionExecution{}, err
	}
	return execution, nil
}

func (r *Repository) TransitionFunctionExecution(ctx context.Context, projectID, functionID, executionID uuid.UUID, next, errorMessage string) (domain.FunctionExecution, error) {
	return r.transitionFunctionExecutionResult(ctx, projectID, functionID, executionID, "", next, errorMessage, nil, nil, nil)
}

func (r *Repository) TransitionFunctionExecutionResult(ctx context.Context, projectID, functionID, executionID uuid.UUID, next, errorMessage string, responseStatus *int, output json.RawMessage, outputContentType *string) (domain.FunctionExecution, error) {
	return r.transitionFunctionExecutionResult(ctx, projectID, functionID, executionID, "", next, errorMessage, responseStatus, output, outputContentType)
}

// TransitionFunctionExecutionResultForWorker fences terminal writes to the
// worker that holds the lease. A stale worker that was requeued cannot publish
// output over a newer attempt.
func (r *Repository) TransitionFunctionExecutionResultForWorker(ctx context.Context, projectID, functionID, executionID uuid.UUID, workerID, next, errorMessage string, responseStatus *int, output json.RawMessage, outputContentType *string) (domain.FunctionExecution, error) {
	if !validFunctionWorkerID(workerID) {
		return domain.FunctionExecution{}, ErrInvalidFunctionSettings
	}
	return r.transitionFunctionExecutionResult(ctx, projectID, functionID, executionID, workerID, next, errorMessage, responseStatus, output, outputContentType)
}

func (r *Repository) transitionFunctionExecutionResult(ctx context.Context, projectID, functionID, executionID uuid.UUID, workerID, next, errorMessage string, responseStatus *int, output json.RawMessage, outputContentType *string) (domain.FunctionExecution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	defer tx.Rollback(ctx)
	var current domain.FunctionExecution
	current, err = scanFunctionExecution(tx.QueryRow(ctx, `SELECT `+functionExecutionProjection+` FROM function_executions WHERE project_id=$1 AND function_id=$2 AND id=$3 FOR UPDATE`, projectID, functionID, executionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FunctionExecution{}, ErrNotFound
	}
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	if workerID != "" {
		var owner *string
		if err := tx.QueryRow(ctx, `SELECT worker_id FROM function_executions WHERE project_id=$1 AND function_id=$2 AND id=$3`, projectID, functionID, executionID).Scan(&owner); err != nil {
			return domain.FunctionExecution{}, err
		}
		if owner == nil || *owner != workerID {
			return domain.FunctionExecution{}, ErrExecutionNotAvailable
		}
	}
	if !validFunctionExecutionTransition(current.Status, next) {
		return domain.FunctionExecution{}, ErrInvalidFunctionTransition
	}
	var item domain.FunctionExecution
	switch next {
	case "running":
		item, err = scanFunctionExecution(tx.QueryRow(ctx, `UPDATE function_executions SET status='running',started_at=COALESCE(started_at,now()),updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionExecutionProjection, projectID, functionID, executionID))
	case "succeeded":
		if len(output) > 1048576 || (len(output) > 0 && !json.Valid(output)) {
			return domain.FunctionExecution{}, ErrInvalidFunctionSettings
		}
		item, err = scanFunctionExecution(tx.QueryRow(ctx, `UPDATE function_executions SET status='succeeded',finished_at=now(),error_message=NULL,response_status=$4,output_json=$5,output_content_type=$6,claimed_at=NULL,worker_id=NULL,updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionExecutionProjection, projectID, functionID, executionID, responseStatus, nullableJSON(output), outputContentType))
	case "failed":
		item, err = scanFunctionExecution(tx.QueryRow(ctx, `UPDATE function_executions SET status='failed',finished_at=now(),error_message=$4,claimed_at=NULL,worker_id=NULL,updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionExecutionProjection, projectID, functionID, executionID, nullableError(errorMessage)))
	case "cancelled":
		item, err = scanFunctionExecution(tx.QueryRow(ctx, `UPDATE function_executions SET status='cancelled',finished_at=now(),claimed_at=NULL,worker_id=NULL,updated_at=now() WHERE project_id=$1 AND function_id=$2 AND id=$3 RETURNING `+functionExecutionProjection, projectID, functionID, executionID))
	default:
		return domain.FunctionExecution{}, ErrInvalidFunctionTransition
	}
	if err != nil {
		return domain.FunctionExecution{}, err
	}
	if next == "succeeded" || next == "failed" || next == "cancelled" {
		usageAt := item.FinishedAt
		if usageAt == nil {
			usageAt = &item.UpdatedAt
		}
		delta := UsageDelta{}
		if next == "failed" {
			delta.FunctionFailureCount = 1
		}
		if current.StartedAt != nil && item.FinishedAt != nil {
			if computeMS := item.FinishedAt.Sub(*current.StartedAt).Milliseconds(); computeMS > 0 {
				delta.FunctionComputeMS = computeMS
			}
		}
		if err := incrementUsageTx(ctx, tx, projectID, *usageAt, delta); err != nil {
			return domain.FunctionExecution{}, err
		}
	}
	metadata := map[string]any{"status": next}
	if responseStatus != nil {
		metadata["response_status"] = *responseStatus
	}
	if errorMessage != "" {
		// Do not forward a runtime error body to integrations; function errors
		// can contain user data or accidentally echoed secrets.
		metadata["has_error"] = true
	}
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "function_execution."+next, "function_execution", executionID, metadata); err != nil {
		return domain.FunctionExecution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionExecution{}, err
	}
	return item, nil
}

func validFunctionExecutionTransition(current, next string) bool {
	switch current {
	case "accepted":
		return next == "running" || next == "failed" || next == "cancelled"
	case "running":
		return next == "succeeded" || next == "failed" || next == "cancelled"
	default:
		return false
	}
}

func validFunctionExecutionTrigger(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index, character := range value {
		alphaNumeric := (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		if alphaNumeric {
			continue
		}
		if index > 0 && (character == '-' || character == '_' || character == '.') {
			continue
		}
		return false
	}
	return true
}

func (r *Repository) ListFunctionExecutions(ctx context.Context, projectID, functionID uuid.UUID, actor FunctionActor, limit int, cursor *uuid.UUID) ([]domain.FunctionExecution, string, error) {
	if _, err := r.requireFunctionRead(ctx, projectID, actor); err != nil {
		return nil, "", err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return nil, "", err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+functionExecutionProjection+` FROM function_executions WHERE project_id=$1 AND function_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, projectID, functionID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.FunctionExecution, 0, limit)
	for rows.Next() {
		item, scanErr := scanFunctionExecution(rows)
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

func (r *Repository) GetFunctionExecution(ctx context.Context, projectID, functionID, executionID uuid.UUID, actor FunctionActor) (domain.FunctionExecution, error) {
	if _, err := r.requireFunctionRead(ctx, projectID, actor); err != nil {
		return domain.FunctionExecution{}, err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return domain.FunctionExecution{}, err
	}
	item, err := scanFunctionExecution(r.pool.QueryRow(ctx, `SELECT `+functionExecutionProjection+` FROM function_executions WHERE project_id=$1 AND function_id=$2 AND id=$3`, projectID, functionID, executionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FunctionExecution{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) AppendFunctionExecutionLog(ctx context.Context, projectID, functionID, executionID, id uuid.UUID, sequence int64, level, message string) (domain.FunctionExecutionLog, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FunctionExecutionLog{}, err
	}
	defer tx.Rollback(ctx)
	_, err = scanFunctionExecution(tx.QueryRow(ctx, `SELECT `+functionExecutionProjection+` FROM function_executions WHERE project_id=$1 AND function_id=$2 AND id=$3 FOR UPDATE`, projectID, functionID, executionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FunctionExecutionLog{}, ErrNotFound
	}
	if err != nil {
		return domain.FunctionExecutionLog{}, err
	}
	if sequence <= 0 {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM function_execution_logs WHERE project_id=$1 AND execution_id=$2`, projectID, executionID).Scan(&sequence); err != nil {
			return domain.FunctionExecutionLog{}, err
		}
	}
	item, err := scanFunctionExecutionLog(tx.QueryRow(ctx, `INSERT INTO function_execution_logs (id,execution_id,function_id,project_id,sequence,level,message) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+functionExecutionLogProjection, id, executionID, functionID, projectID, sequence, level, message))
	if err != nil {
		return domain.FunctionExecutionLog{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FunctionExecutionLog{}, err
	}
	return item, nil
}

// ClaimNextFunctionExecution atomically leases the oldest accepted execution.
// PostgreSQL row locking is the source of truth, so multiple workers can poll
// concurrently without duplicate execution. The function and deployment are
// read in the same transaction and remain tenant-bound by every predicate.
func (r *Repository) ClaimNextFunctionExecution(ctx context.Context, workerID string) (FunctionExecutionJob, error) {
	if !validFunctionWorkerID(workerID) {
		return FunctionExecutionJob{}, ErrInvalidFunctionSettings
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return FunctionExecutionJob{}, err
	}
	defer tx.Rollback(ctx)
	var executionID, projectID, functionID, deploymentID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT e.id,e.project_id,e.function_id,e.deployment_id
		FROM function_executions e
		JOIN project_functions f ON f.id=e.function_id AND f.project_id=e.project_id
		JOIN function_deployments d ON d.id=e.deployment_id AND d.function_id=e.function_id AND d.project_id=e.project_id
		WHERE e.status='accepted' AND d.status='active' AND d.build_status='succeeded'
		ORDER BY e.created_at,e.id
		LIMIT 1
		FOR UPDATE OF e SKIP LOCKED`).Scan(&executionID, &projectID, &functionID, &deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return FunctionExecutionJob{}, ErrNoExecutionJob
	}
	if err != nil {
		return FunctionExecutionJob{}, err
	}
	function, err := r.functionByID(ctx, tx, projectID, functionID, true)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return FunctionExecutionJob{}, ErrNoExecutionJob
		}
		return FunctionExecutionJob{}, err
	}
	deployment, sourcePath, err := r.functionDeploymentByIDTx(ctx, tx, projectID, functionID, deploymentID, true)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return FunctionExecutionJob{}, ErrNoExecutionJob
		}
		return FunctionExecutionJob{}, err
	}
	if deployment.Status != "active" || deployment.BuildStatus != "succeeded" {
		return FunctionExecutionJob{}, ErrNoExecutionJob
	}
	execution, err := scanFunctionExecution(tx.QueryRow(ctx, `UPDATE function_executions SET status='running',started_at=COALESCE(started_at,now()),claimed_at=now(),worker_id=$4,updated_at=now() WHERE id=$1 AND project_id=$2 AND function_id=$3 AND status='accepted' RETURNING `+functionExecutionProjection, executionID, projectID, functionID, workerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return FunctionExecutionJob{}, ErrNoExecutionJob
	}
	if err != nil {
		return FunctionExecutionJob{}, err
	}
	// The update includes explicit tenant predicates and the identifiers came
	// from the locked row, so a future query edit cannot silently cross tenants.
	if execution.ProjectID != projectID.String() || execution.FunctionID != functionID.String() || execution.DeploymentID != deploymentID.String() {
		return FunctionExecutionJob{}, ErrExecutionNotAvailable
	}
	function, err = r.functionDeploymentRuntimeConfigTx(ctx, tx, projectID, functionID, deploymentID, function)
	if err != nil {
		return FunctionExecutionJob{}, err
	}
	buildStorage, err := r.functionDeploymentBuildStorageTx(ctx, tx, projectID, functionID, deploymentID, false)
	if err != nil {
		return FunctionExecutionJob{}, err
	}
	if execution.Status != "running" || deployment.Status != "active" || deployment.BuildStatus != "succeeded" {
		return FunctionExecutionJob{}, ErrExecutionNotAvailable
	}
	if err := tx.Commit(ctx); err != nil {
		return FunctionExecutionJob{}, err
	}
	return FunctionExecutionJob{Execution: execution, Function: function, Deployment: deployment, SourcePath: sourcePath, BuildPath: buildStorage.BuildPath, BuildChecksumSHA256: buildStorage.BuildChecksumSHA256}, nil
}

// RequeueStaleFunctionExecutions returns leases whose worker disappeared.
// Callers should use a timeout comfortably larger than the maximum function
// timeout to avoid racing a healthy worker that is still flushing logs.
func (r *Repository) RequeueStaleFunctionExecutions(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, ErrInvalidFunctionSettings
	}
	result, err := r.pool.Exec(ctx, `UPDATE function_executions SET status='accepted',started_at=NULL,claimed_at=NULL,worker_id=NULL,updated_at=now() WHERE status='running' AND claimed_at IS NOT NULL AND claimed_at < now() - ($1::double precision * interval '1 second')`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func validFunctionWorkerID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// FunctionRuntimeVariables decrypts values only for the trusted execution
// worker. It never returns metadata to an API actor and rejects malformed
// ciphertext rather than starting a process with partial configuration.
func (r *Repository) FunctionRuntimeVariables(ctx context.Context, projectID, functionID uuid.UUID, cipher *functionsecret.Cipher) ([]FunctionRuntimeVariable, error) {
	if cipher == nil {
		return nil, ErrFunctionSecretUnavailable
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT key,value_ciphertext,is_secret FROM function_variables WHERE project_id=$1 AND function_id=$2 ORDER BY key`, projectID, functionID)
	if err != nil {
		return nil, err
	}
	return materializeFunctionRuntimeVariables(rows, cipher)
}

// FunctionRuntimeVariablesForDeployment decrypts the ciphertext snapshot that
// belongs to one immutable deployment. Updating function_variables therefore
// cannot silently alter a built revision or an invocation already queued for
// it.
func (r *Repository) FunctionRuntimeVariablesForDeployment(ctx context.Context, projectID, functionID, deploymentID uuid.UUID, cipher *functionsecret.Cipher) ([]FunctionRuntimeVariable, error) {
	if cipher == nil {
		return nil, ErrFunctionSecretUnavailable
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM function_deployments WHERE project_id=$1 AND function_id=$2 AND id=$3)`, projectID, functionID, deploymentID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT key,value_ciphertext,is_secret FROM function_deployment_variables WHERE project_id=$1 AND function_id=$2 AND deployment_id=$3 ORDER BY key`, projectID, functionID, deploymentID)
	if err != nil {
		return nil, err
	}
	return materializeFunctionRuntimeVariables(rows, cipher)
}

func materializeFunctionRuntimeVariables(rows functionVariableRows, cipher *functionsecret.Cipher) ([]FunctionRuntimeVariable, error) {
	defer rows.Close()
	variables := make([]FunctionRuntimeVariable, 0)
	for rows.Next() {
		var key string
		var ciphertext []byte
		var secret bool
		if err := rows.Scan(&key, &ciphertext, &secret); err != nil {
			return nil, err
		}
		if len(ciphertext) == 0 {
			return nil, ErrFunctionSecretUnavailable
		}
		plaintext, err := cipher.Decrypt(ciphertext)
		if err != nil || strings.IndexByte(string(plaintext), 0) >= 0 {
			return nil, ErrFunctionSecretUnavailable
		}
		variables = append(variables, FunctionRuntimeVariable{Key: key, Value: string(plaintext), IsSecret: secret})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return variables, nil
}

func (r *Repository) ListFunctionExecutionLogs(ctx context.Context, projectID, functionID, executionID uuid.UUID, actor FunctionActor, limit int, after int64) ([]domain.FunctionExecutionLog, error) {
	if _, err := r.requireFunctionRead(ctx, projectID, actor); err != nil {
		return nil, err
	}
	if _, err := r.functionByID(ctx, r.pool, projectID, functionID, false); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+functionExecutionLogProjection+` FROM function_execution_logs WHERE project_id=$1 AND function_id=$2 AND execution_id=$3 AND sequence>$4 ORDER BY sequence LIMIT $5`, projectID, functionID, executionID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.FunctionExecutionLog, 0, limit)
	for rows.Next() {
		item, scanErr := scanFunctionExecutionLog(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) auditFunction(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, actor FunctionActor, action, targetType string, target uuid.UUID, metadata map[string]any) error {
	orgID, err := projectOrganizationIDValue(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["project_id"] = projectID.String()
	if functionActorIsAPIKey(actor) {
		metadata["actor"] = "api_key"
		metadata["api_key_id"] = actor.APIKeyID.String()
		if err := writeAuditMetadata(ctx, tx, orgID, uuid.Nil, action, targetType, target, metadata); err != nil {
			return err
		}
		return r.enqueueWebhookEventTx(ctx, tx, projectID, action, targetType, target, metadata)
	}
	if err := writeAuditMetadata(ctx, tx, orgID, actor.AccountID, action, targetType, target, metadata); err != nil {
		return err
	}
	return r.enqueueWebhookEventTx(ctx, tx, projectID, action, targetType, target, metadata)
}

// Keep the standard time import in this file as a reminder that all worker
// transition timestamps are generated by PostgreSQL's now(), not host input.
var _ = time.Second
