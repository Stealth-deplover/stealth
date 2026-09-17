package repository

// Function execution persistence owns enqueueing, worker leases, result
// transitions, runtime-variable materialization, and execution logs.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_execution.accept", "function_execution", id, map[string]any{"function_id": functionID.String(), "trigger": trigger, "deployment_id": deploymentID}); err != nil {
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
	if err := r.auditFunction(ctx, tx, projectID, actor, "function_execution.accept", "function_execution", id, map[string]any{"function_id": functionID.String(), "trigger": trigger, "deployment_id": deploymentID}); err != nil {
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
	metadata := map[string]any{"function_id": functionID.String(), "status": next}
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
	if err := r.enqueueWebhookEventTx(ctx, tx, projectID, "function_execution.running", "function_execution", executionID, map[string]any{"function_id": functionID.String(), "status": execution.Status}); err != nil {
		return FunctionExecutionJob{}, err
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
