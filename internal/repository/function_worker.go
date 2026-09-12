package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/google/uuid"
)

// FunctionBuildStore is the persistence capability required by the function
// build worker. It owns build leases, immutable artifact publication, bounded
// failure transitions, runtime-variable snapshots, and build logs.
type FunctionBuildStore interface {
	ClaimNextFunctionDeployment(context.Context, string) (FunctionBuildJob, error)
	RequeueStaleFunctionDeployments(context.Context, time.Duration) (int64, error)
	CompleteFunctionDeploymentBuild(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string, int64, string) (domain.FunctionDeployment, error)
	FailFunctionDeploymentBuild(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string) (domain.FunctionDeployment, error)
	FunctionRuntimeVariablesForDeployment(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *functionsecret.Cipher) ([]FunctionRuntimeVariable, error)
	AppendFunctionBuildLog(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, int64, string, string) (domain.FunctionBuildLog, error)
}

// FunctionExecutionStore is the persistence capability required by the
// function execution worker. Its transition method enforces the worker lease
// and tenant predicates before publishing a result.
type FunctionExecutionStore interface {
	ClaimNextFunctionExecution(context.Context, string) (FunctionExecutionJob, error)
	RequeueStaleFunctionExecutions(context.Context, time.Duration) (int64, error)
	FunctionRuntimeVariablesForDeployment(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *functionsecret.Cipher) ([]FunctionRuntimeVariable, error)
	TransitionFunctionExecutionResultForWorker(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string, string, *int, json.RawMessage, *string) (domain.FunctionExecution, error)
	AppendFunctionExecutionLog(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, int64, string, string) (domain.FunctionExecutionLog, error)
}

// FunctionWorkerStore is the composition seam used by the combined worker.
// Keeping build and execution methods as separate embedded capabilities makes
// it possible to run either worker with a narrower adapter later.
type FunctionWorkerStore interface {
	FunctionBuildStore
	FunctionExecutionStore
}

var (
	_ FunctionBuildStore     = (*Repository)(nil)
	_ FunctionExecutionStore = (*Repository)(nil)
	_ FunctionWorkerStore    = (*Repository)(nil)
)
