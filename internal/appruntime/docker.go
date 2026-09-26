package appruntime

import (
	"context"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"time"
)

const (
	defaultRuntimeNetwork     = "stealth_app_runtime"
	defaultActionTimeout      = 30 * time.Second
	defaultImageImportTimeout = 10 * time.Minute
	defaultOutputLimit        = 1 << 20
	maxContainerList          = 10000
	runtimeSchema             = "v1"
)

var (
	ErrRuntimeUnavailable       = errors.New("Moby runtime is unavailable")
	ErrRuntimeNetworkConflict   = errors.New("App runtime network ownership conflict")
	ErrRuntimeOwnershipConflict = errors.New("App container ownership conflict")
	ErrImageVerification        = errors.New("App image verification failed")
	ErrImageImport              = errors.New("App image import failed")
	ErrInvalidRuntimeJob        = errors.New("invalid App runtime job")
	errUnsupportedImageVolumes  = errors.New("runtime image declares unsupported volumes")
	ErrContainerCreate          = errors.New("App container create failed")
	ErrContainerStart           = errors.New("App container start failed")
	ErrContainerInspection      = errors.New("App container inspection failed")
	ErrDockerObjectNotFound     = errors.New("Docker object not found")
	ErrDockerOutputTooLarge     = errors.New("Docker output exceeded its bound")
)

type Moby struct {
	Runner        CommandRunner
	NetworkName   string
	ActionTimeout time.Duration
	ImportTimeout time.Duration
	Security      RuntimeSecurityProfile
}

func NewMoby(runner CommandRunner, networkName string, actionTimeout, importTimeout time.Duration) (*Moby, error) {
	if runner == nil {
		runner = ExecCommandRunner{}
	}
	if networkName == "" {
		networkName = defaultRuntimeNetwork
	}
	if !validDockerName(networkName) {
		return nil, errors.New("invalid App runtime network name")
	}
	if actionTimeout <= 0 {
		actionTimeout = defaultActionTimeout
	}
	if actionTimeout < 5*time.Second || actionTimeout > 2*time.Minute {
		return nil, errors.New("App runtime action timeout is outside its supported range")
	}
	if importTimeout <= 0 {
		importTimeout = defaultImageImportTimeout
	}
	if importTimeout < time.Minute || importTimeout > 30*time.Minute {
		return nil, errors.New("App image import timeout is outside its supported range")
	}
	return &Moby{Runner: runner, NetworkName: networkName, ActionTimeout: actionTimeout, ImportTimeout: importTimeout}, nil
}

func IsDockerNotFound(err error) bool { return errors.Is(err, ErrDockerObjectNotFound) }

func IsOwnershipConflict(err error) bool {
	return errors.Is(err, ErrRuntimeOwnershipConflict) || errors.Is(err, ErrRuntimeNetworkConflict)
}

func safeRuntimeError(err error) string {
	switch {
	case errors.Is(err, ErrRuntimeNetworkConflict):
		return "runtime network conflict"
	case errors.Is(err, ErrRuntimeOwnershipConflict):
		return "container ownership conflict"
	case errors.Is(err, errRuntimeImageCacheReferenceList):
		switch {
		case errors.Is(err, ErrImageVerification):
			return "runtime image tag ownership verification failed"
		case errors.Is(err, ErrDockerOutputTooLarge):
			return "runtime image inventory exceeded bounds"
		default:
			return "runtime image inventory unavailable"
		}
	case errors.Is(err, errRuntimeImageCacheInspection):
		switch {
		case errors.Is(err, ErrImageVerification):
			return "runtime image metadata verification failed"
		case errors.Is(err, ErrDockerOutputTooLarge):
			return "runtime image inspection exceeded bounds"
		default:
			return "runtime image inspection unavailable"
		}
	case errors.Is(err, ErrImageVerification), errors.Is(err, ociartifact.ErrInvalidArchive):
		return "image verification failed"
	case errors.Is(err, ErrImageArtifactUnavailable), errors.Is(err, ErrDockerObjectNotFound):
		return "image artifact unavailable"
	case errors.Is(err, errUnsupportedImageVolumes):
		return "runtime image declares unsupported volumes"
	case errors.Is(err, ErrUnsupportedRuntimePlatform):
		return "unsupported runtime platform"
	case errors.Is(err, ErrImageImport):
		return "image import failed"
	case errors.Is(err, ErrContainerCreate):
		return "container create failed"
	case errors.Is(err, ErrContainerStart):
		return "container start failed"
	case errors.Is(err, ErrContainerInspection):
		return "container inspection failed"
	case errors.Is(err, ErrRuntimeUnavailable), errors.Is(err, context.DeadlineExceeded):
		return "runtime unavailable"
	default:
		return "runtime unavailable"
	}
}
