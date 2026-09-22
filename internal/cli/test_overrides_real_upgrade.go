//go:build aud17realupgrade

package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"time"
)

// This file is compiled only into the CI upgrade-smoke target binary. It
// keeps the production binary's asset base, command runner, and host
// ownership behavior unchanged while allowing the subprocess test to use a
// local target-asset server and avoid starting a second Docker stack before
// the shell smoke. The temporary fixture is owned by the unprivileged test
// process, so its ownership operation is represented by the installengine
// seam; the real Compose smoke remains authoritative for the fixed UID/GID
// runtime boundary.
type realUpgradeCommandRunner struct{}

func (realUpgradeCommandRunner) Run(context.Context, string, io.Writer, io.Writer, string, ...string) error {
	return nil
}

func (realUpgradeCommandRunner) Output(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}

func (realUpgradeCommandRunner) CombinedOutput(context.Context, string, string, ...string) ([]byte, error) {
	return nil, nil
}

func compiledCLITestOverrides() *cliTestOverrides {
	assetBase := strings.TrimRight(strings.TrimSpace(os.Getenv("STEALTH_REAL_V025_ASSET_BASE")), "/")
	if assetBase == "" {
		return nil
	}
	return &cliTestOverrides{
		assetBase:              assetBase,
		runner:                 realUpgradeCommandRunner{},
		traefikOwnershipSetter: func(string, int, int) error { return nil },
		pollAttempts:           1,
		pollInterval:           time.Millisecond,
	}
}
