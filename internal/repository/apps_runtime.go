package repository

// App runtime persistence is the private durable coordination seam between
// PostgreSQL desired state and the trusted Moby reconciler. Docker IDs and
// image paths in these projections never enter the normal App API response.

import (
	"errors"
	"fmt"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
	"strings"
	"time"
)

const (
	AppRuntimeDriftInterval = 30 * time.Second
	AppRuntimeMaxErrorBytes = 512
)

var (
	ErrNoAppRuntimeJob           = errors.New("no App runtime job available")
	ErrNoAppHealthCheckJob       = errors.New("no App health check available")
	ErrNoAppRuntimeCleanup       = errors.New("no App runtime cleanup job available")
	ErrAppRuntimeLeaseLost       = errors.New("App runtime lease is no longer owned by this worker")
	ErrAppRuntimeStale           = errors.New("App desired state changed during runtime reconciliation")
	ErrInvalidAppRuntimeJob      = errors.New("invalid App runtime job")
	ErrAppRuntimeLogSourcesLimit = errors.New("App runtime log source limit exceeded")
)

const maxAppRuntimeLogSources = 2048

// AppRuntimeJob is a trusted worker projection. ImagePath is private and is
// read only from the immutable selected AppDeployment.
type AppRuntimeJob struct {
	App           domain.App
	Deployment    domain.AppDeployment
	RouteIdentity uuid.UUID
	ContainerName string
	ImagePath     string
	WorkerID      string
	LeaseToken    uuid.UUID
	FailureCount  int
}

// AppRuntimeContainer is the private identity confirmed by Docker inspect.
type AppRuntimeContainer struct {
	ID          string
	Name        string
	ImageID     string
	ImageDigest string
	RuntimeTag  string
	Address     string
}

// AppHealthCheckJob is fenced by the same per-App runtime lease used by
// reconciliation. Its container identity is an internal worker value only.
type AppHealthCheckJob struct {
	App                domain.App
	ContainerID        string
	Address            string
	RouteIdentity      uuid.UUID
	ContainerName      string
	HealthStatus       string
	HealthFailureCount int
	WorkerID           string
	LeaseToken         uuid.UUID
}

// AppRuntimeCleanupJob survives deletion of its App and project rows. The
// runtime worker still verifies ownership labels before acting on its target.
type AppRuntimeCleanupJob struct {
	ID                  uuid.UUID
	ProjectID           *uuid.UUID
	AppID               uuid.UUID
	ContainerID         *string
	ContainerName       string
	StopGracePeriodSecs int
	WorkerID            string
	LeaseToken          uuid.UUID
	AttemptCount        int
}

func AppRuntimeContainerName(appID uuid.UUID) string {
	return "stealth-app-" + strings.ReplaceAll(appID.String(), "-", "")
}

func validateRuntimeJob(job AppRuntimeJob) error {
	if job.LeaseToken == uuid.Nil || !validFunctionWorkerID(job.WorkerID) {
		return ErrInvalidAppRuntimeJob
	}
	appID, err := uuid.Parse(job.App.ID)
	if err != nil || appID == uuid.Nil {
		return ErrInvalidAppRuntimeJob
	}
	if job.RouteIdentity == uuid.Nil || job.ContainerName != AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) {
		return ErrInvalidAppRuntimeJob
	}
	projectID, err := uuid.Parse(job.App.ProjectID)
	if err != nil || projectID == uuid.Nil || job.App.DesiredGeneration < 1 || len(job.App.WorkloadSpecSHA256) != 64 {
		return ErrInvalidAppRuntimeJob
	}
	if job.App.DesiredDeploymentID != nil {
		deploymentID, parseErr := uuid.Parse(*job.App.DesiredDeploymentID)
		if parseErr != nil || deploymentID == uuid.Nil {
			return ErrInvalidAppRuntimeJob
		}
	}
	return nil
}

func runtimeDesiredStateMatches(current, expected domain.App) bool {
	return current.ID == expected.ID && current.ProjectID == expected.ProjectID && current.Enabled == expected.Enabled &&
		current.DesiredGeneration == expected.DesiredGeneration && current.WorkloadSpecSHA256 == expected.WorkloadSpecSHA256 &&
		optionalStringEqual(current.DesiredDeploymentID, expected.DesiredDeploymentID)
}

func optionalStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func optionalUUID(value *string) any {
	if value == nil {
		return nil
	}
	parsed, err := uuid.Parse(*value)
	if err != nil {
		return nil
	}
	return parsed
}

func validRuntimeContainerName(appID uuid.UUID, name string) bool {
	return ValidAppRuntimeContainerName(appID, name)
}

func validRuntimeContainerID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func validRuntimeImageID(value string) bool {
	return len(value) == 71 && strings.HasPrefix(value, "sha256:") && validRuntimeDigest(value)
}

func validRuntimeDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[7:] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func safeAppRuntimeError(value string) string {
	value = strings.TrimSpace(value)
	allowed := map[string]struct{}{
		"runtime unavailable": {}, "image artifact unavailable": {}, "image verification failed": {},
		"image import failed": {}, "runtime network conflict": {}, "container ownership conflict": {},
		"container create failed": {}, "container start failed": {}, "container inspection failed": {},
		"container exited unexpectedly": {}, "unsupported runtime platform": {},
		"runtime image declares unsupported volumes": {}, "runtime cleanup unavailable": {},
	}
	if _, ok := allowed[value]; !ok || len(value) > AppRuntimeMaxErrorBytes {
		return ""
	}
	return value
}

func appRuntimeDebugIdentity(job AppRuntimeJob) string {
	return fmt.Sprintf("project_id=%s app_id=%s generation=%d", job.App.ProjectID, job.App.ID, job.App.DesiredGeneration)
}
