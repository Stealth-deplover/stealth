package repository

import (
	"context"
	"errors"

	"github.com/Stealth-deplover/stealth/internal/apikey"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
