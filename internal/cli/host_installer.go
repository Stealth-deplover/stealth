package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/setupconfig"
	"github.com/Stealth-deplover/stealth/internal/setupinstall"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

type cloudflareClientFactory func(string) (cloudflare.Client, error)

type setupHandoffStatusPayload struct {
	Pending bool `json:"pending"`
}

// executeHostInstallation is the only host-side owner of a browser-requested
// production installation. It deliberately receives the durable run ID from
// setup state and rechecks it after taking the installation lock.
func (a *App) executeHostInstallation(ctx context.Context, layout InstallLayout, values map[string]string, store setupstate.Store, runID string) error {
	if a == nil || a.runner == nil {
		return errors.New("CLI command runner is not configured")
	}
	if store == nil {
		return errors.New("setup state store is not configured")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("installation run identifier is required")
	}
	state, err := store.Load(ctx)
	if err != nil {
		return err
	}
	if state.InstallRunID != runID {
		return errors.New("installation run does not own setup state")
	}
	if state.Phase == setupstate.PhaseComplete {
		return nil
	}
	if !setupstate.InstallationRequested(state) {
		return errors.New("installation has not been requested")
	}

	lock, err := installengine.AcquireProcessLock(layout.StateDir, "install.lock", "installation")
	if err != nil {
		return err
	}
	defer lock.Close()

	state, err = store.Load(ctx)
	if err != nil {
		return err
	}
	if state.InstallRunID != runID {
		return errors.New("installation run no longer owns setup state")
	}
	if state.Phase == setupstate.PhaseComplete {
		return nil
	}
	if state.Phase == setupstate.PhaseHandoff {
		return a.finishHostHandoff(ctx, layout, values, store, runID)
	}
	if state.Phase != setupstate.PhaseInstallRequested && state.Phase != setupstate.PhaseInstalling {
		return fmt.Errorf("setup installation cannot resume from phase %q", state.Phase)
	}
	if err := setupconfig.ValidateInstallableSetup(state); err != nil {
		return a.failHostInstallation(ctx, store, runID, "install_validation_failed", err)
	}

	claimed, err := store.Update(ctx, func(state *setupstate.State) error {
		if state.InstallRunID != runID {
			return errors.New("installation run does not own setup state")
		}
		if state.Phase == setupstate.PhaseInstallRequested {
			if err := setupstate.BeginInstallation(state, runID); err != nil {
				return err
			}
			return setupstate.UpdateInstallationProgress(state, runID, installengine.StepNames[installengine.StepConfiguration])
		}
		if state.Phase != setupstate.PhaseInstalling {
			return errors.New("setup installation is no longer startable")
		}
		return nil
	})
	if err != nil {
		return err
	}
	state = claimed

	plan, err := setupinstall.BuildPlan(state, layout.Root)
	if err != nil {
		return a.failHostInstallation(ctx, store, runID, "install_plan_failed", err)
	}
	fmt.Fprintln(a.out, "✓ Installing production services")

	installContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var progressErr error
	engine := a.installEngine()
	err = engine.InstallLocked(installContext, plan, func(event installengine.Event) {
		if event.Status == "started" {
			fmt.Fprintf(a.out, "  → %s\n", event.Step)
		}
		if event.Step == "" {
			return
		}
		if updateErr := persistHostProgress(ctx, store, runID, event.Step); updateErr != nil && progressErr == nil {
			progressErr = updateErr
			cancel()
		}
	})
	if progressErr != nil {
		return progressErr
	}
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return err
		}
		return a.failHostInstallation(ctx, store, runID, "install_failed", err)
	}

	fmt.Fprintln(a.out, "✓ Production health checks passed")
	if plan.Cloudflare {
		if err := persistHostProgress(ctx, store, runID, "Cloudflare Tunnel health"); err != nil {
			return err
		}
		if err := a.waitForHostCloudflareTunnelHealth(ctx, values, store); err != nil {
			return a.failHostInstallation(ctx, store, runID, "cloudflare_tunnel_unhealthy", err)
		}
	}
	if _, err := store.Update(ctx, func(state *setupstate.State) error {
		return setupstate.MarkInstallationHandoff(state, runID)
	}); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "✓ Production handoff ready")
	return a.finishHostHandoff(ctx, layout, values, store, runID)
}

func persistHostProgress(ctx context.Context, store setupstate.Store, runID, step string) error {
	_, err := store.Update(ctx, func(state *setupstate.State) error {
		return setupstate.UpdateInstallationProgress(state, runID, step)
	})
	return err
}

func (a *App) failHostInstallation(ctx context.Context, store setupstate.Store, runID, code string, cause error) error {
	if cause == nil {
		cause = errors.New("host installation failed")
	}
	if errors.Is(cause, context.Canceled) && ctx.Err() != nil {
		return cause
	}
	message := safeHostInstallError(cause)
	_, updateErr := store.Update(context.Background(), func(state *setupstate.State) error {
		return setupstate.FailInstallation(state, runID, code, message)
	})
	if updateErr != nil {
		return errors.Join(cause, updateErr)
	}
	return cause
}

func safeHostInstallError(err error) string {
	if err == nil {
		return "host installation failed"
	}
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	for _, marker := range []string{"password", "secret", "token", "private key", "postgres://", "redis://", "s3"} {
		if strings.Contains(lower, marker) {
			return "host installation failed; inspect the host CLI output or run `stealth doctor`"
		}
	}
	message = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\x00' {
			return ' '
		}
		return r
	}, message)
	if len(message) > 240 {
		message = message[:240]
	}
	if message == "" {
		return "host installation failed"
	}
	return message
}

func (a *App) waitForHostCloudflareTunnelHealth(ctx context.Context, values map[string]string, store setupProgressSource) error {
	state, err := store.Load(ctx)
	if err != nil {
		return errors.New("Cloudflare Tunnel health could not be verified")
	}
	binding := state.EffectiveCloudflareBinding()
	if err := state.Cloudflare.Binding.ValidateDraft(state.Draft); err != nil || binding.AccountID == "" || binding.TunnelID == "" {
		return errors.New("Cloudflare Tunnel state is inconsistent")
	}
	token := state.Secret("cloudflare_access_token")
	if strings.TrimSpace(token) == "" {
		return errors.New("Cloudflare API token is missing")
	}
	factory := a.cloudflareFactory
	if factory == nil {
		factory = func(token string) (cloudflare.Client, error) {
			return cloudflare.NewClient(token, values["CLOUDFLARE_API_BASE_URL"], a.httpClient)
		}
	}
	client, err := factory(token)
	if err != nil {
		return errors.New("Cloudflare Tunnel health could not be verified")
	}
	healthContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		status, statusErr := client.TunnelStatus(healthContext, binding.AccountID, binding.TunnelID)
		if statusErr == nil && cloudflare.StatusIsHealthy(status) {
			return nil
		}
		if healthContext.Err() != nil {
			break
		}
		if err := waitSetupPoll(healthContext, setupWaitInterval(a)); err != nil {
			return err
		}
	}
	return errors.New("Cloudflare Tunnel did not become healthy")
}

func (a *App) finishHostHandoff(ctx context.Context, layout InstallLayout, values map[string]string, store setupstate.Store, runID string) error {
	key, err := bootstrapCLIKey(values)
	if err != nil {
		return a.failHostInstallation(ctx, store, runID, "handoff_failed", err)
	}
	if err := a.waitForSetupHandoff(ctx, values, key); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return err
		}
		return a.failHostInstallation(ctx, store, runID, "handoff_failed", err)
	}
	if err := persistHostProgress(ctx, store, runID, "Cleanup"); err != nil {
		return err
	}
	state, err := store.Load(ctx)
	if err != nil {
		return err
	}
	cleanupErr := error(nil)
	if isQuickTunnelContainerName(state.QuickTunnel) {
		cleanupErr = errors.Join(cleanupErr, a.closeQuickTunnel(ctx, layout, state.QuickTunnel))
	}
	cleanupErr = errors.Join(cleanupErr, a.cleanupSetupServices(ctx, layout))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cleanupErr != nil {
		const message = "production is ready, but temporary setup cleanup needs to be retried"
		_, updateErr := store.Update(context.Background(), func(state *setupstate.State) error {
			if state.InstallRunID != runID {
				return errors.New("installation run does not own setup state")
			}
			if state.Phase != setupstate.PhaseHandoff {
				return errors.New("setup installation is no longer in handoff")
			}
			state.Step = "Cleanup"
			state.ErrorCode = "setup_cleanup_failed"
			state.ErrorMessage = message
			state.LastEventID++
			return nil
		})
		if updateErr != nil {
			return errors.Join(errors.New(message), updateErr)
		}
		return errors.New(message)
	}
	if _, err := store.Update(ctx, func(state *setupstate.State) error {
		return setupstate.CompleteInstallation(state, runID)
	}); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "✓ Setup complete")
	return nil
}

func (a *App) cleanupSetupServices(ctx context.Context, layout InstallLayout) error {
	if layout.SetupComposeFile == "" {
		return nil
	}
	return a.runner.Run(ctx, layout.Root, io.Discard, io.Discard, "docker", "compose", "--env-file", layout.EnvFile, "-f", layout.SetupComposeFile, "rm", "-sf", "setup", "setup-console", "setup-proxy")
}

func (a *App) waitForSetupHandoff(ctx context.Context, values map[string]string, key []byte) error {
	endpoint := "http://127.0.0.1:" + setupPortsFromConfig(values).API + "/v1/setup/handoff/status"
	deadline := time.NewTimer(bootstrap.CodeLifetime)
	defer deadline.Stop()
	for {
		pending, err := a.setupHandoffPending(ctx, endpoint, key)
		if err == nil && !pending {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return nil
		case <-time.After(setupWaitInterval(a)):
		}
	}
}

func (a *App) setupHandoffPending(ctx context.Context, endpoint string, key []byte) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(key))
	response, err := a.httpClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, &bootstrapHTTPError{status: response.StatusCode}
	}
	var payload setupHandoffStatusPayload
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return false, err
	}
	return payload.Pending, nil
}
