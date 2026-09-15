package cli

// Setup workflow owns argument parsing, installation checks, bootstrap API
// calls, and temporary tunnel lifecycle. TUI state and rendering live in
// setup_tui.go so the operational path remains readable and independently
// testable.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/installengine"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

const (
	// Keep this image pinned. Quick Tunnels are a temporary onboarding
	// transport, not part of the production Compose stack.
	quickTunnelCloudflaredImage = "cloudflare/cloudflared:2026.9.0@sha256:ff69a2225ad7c6f85ed84fbd5f3087df46202426b2388ec60214098e0adf05e9"
	quickTunnelNetwork          = "stealth_network"
	quickTunnelContainerPrefix  = "stealth-onboarding-"
	setupPollInterval           = 2 * time.Second
)

var quickTunnelURLPattern = regexp.MustCompile(`(?i)https://[a-z0-9][a-z0-9-]*\.trycloudflare\.com`)

type bootstrapHTTPError struct {
	status int
}

func (e *bootstrapHTTPError) Error() string {
	return fmt.Sprintf("bootstrap API returned HTTP %d", e.status)
}

type bootstrapStatusPayload struct {
	SetupRequired bool `json:"setup_required"`
}

type bootstrapAdoptionAccountPayload struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Provider      string    `json:"provider"`
	ProviderLogin string    `json:"provider_login"`
	CreatedAt     time.Time `json:"created_at"`
}

type bootstrapAdoptionAccountsPayload struct {
	Accounts []bootstrapAdoptionAccountPayload `json:"accounts"`
}

type bootstrapSessionPayload struct {
	SetupCode string    `json:"setup_code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type setupQuickTunnelPayload struct {
	ContainerName string `json:"container_name"`
	URL           string `json:"url"`
}

type setupPreparedMessage struct {
	session       bootstrapSessionPayload
	localURL      string
	tunnelURL     string
	containerName string
	warning       string
	complete      bool
	err           error
}

// runWebBootstrap starts only the setup Compose project. It deliberately does
// not ask for provider credentials in the terminal; the browser owns the
// reviewed configuration, while the CLI remains responsible for local Docker
// capability checks and the short-lived access tunnel.
func (a *App) runWebBootstrap(ctx context.Context, checks []SystemCheck, layout InstallLayout, version string, existing bool) int {
	if !checksPass(checks) {
		fmt.Fprintf(a.errOut, "system requirements are not satisfied: %s\n", failedCheckSummary(checks))
		return 1
	}
	gid, err := dockerSocketGID("/var/run/docker.sock")
	if err != nil {
		fmt.Fprintf(a.errOut, "Docker socket is not accessible: %v\n", err)
		return 1
	}
	configContents := ""
	if !existing {
		configContents, err = installengine.GenerateConfig(installengine.ConfigOptions{
			Version:     version,
			PublicURL:   "http://localhost:8081",
			DockerGID:   gid,
			Setup:       true,
			InstallRoot: layout.Root,
		})
		if err != nil {
			fmt.Fprintf(a.errOut, "could not prepare setup configuration: %v\n", err)
			return 1
		}
	}
	plan := InstallPlan{
		Layout:             layout,
		Version:            version,
		PublicURL:          "http://localhost:8081",
		DockerGID:          gid,
		Setup:              true,
		ConfigContents:     configContents,
		InternalAPIURL:     "http://127.0.0.1:18081",
		InternalConsoleURL: "http://127.0.0.1:13001",
		InternalProxyURL:   "http://127.0.0.1:8081",
		Existing:           existing,
	}
	if err := a.installEngine().Install(ctx, plan, nil); err != nil {
		fmt.Fprintf(a.errOut, "could not start the setup service: %v\n", err)
		fmt.Fprintln(a.errOut, "Configuration was preserved; run `stealth install --repair` or `stealth doctor` for diagnostics.")
		return 1
	}
	fmt.Fprintln(a.out, "✓ System requirements checked")
	fmt.Fprintln(a.out, "✓ Setup service started")
	values, err := readEnvFile(layout.EnvFile)
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read setup configuration: %v\n", err)
		return 1
	}
	key, err := bootstrapCLIKey(values)
	if err != nil {
		fmt.Fprintln(a.errOut, err)
		return 1
	}
	setupPorts := setupPortsFromConfig(values)
	apiURL := "http://127.0.0.1:" + setupPorts.API
	status, err := a.bootstrapStatus(ctx, apiURL+"/v1/bootstrap/status")
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read first-run setup state: %v\n", err)
		return 1
	}
	state, stateErr := a.loadSetupState(values)
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		fmt.Fprintf(a.errOut, "could not read browser setup state: %v\n", stateErr)
		return 1
	}
	if !status.SetupRequired && stateErr == nil && state.Phase == setupstate.PhaseComplete {
		fmt.Fprintln(a.out, "Instance setup has already been completed.")
		return 0
	}
	var session bootstrapSessionPayload
	if status.SetupRequired {
		session, err = a.createBootstrapSession(ctx, apiURL+"/v1/bootstrap/sessions", key)
	} else {
		session, err = a.recoverSetupSession(ctx, apiURL+"/v1/setup/recovery", key)
	}
	if err != nil {
		fmt.Fprintf(a.errOut, "could not create a setup session: %v\n", err)
		return 1
	}
	if previousName := a.previousSetupTunnel(values); previousName != "" {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		cleanupErr := a.closeQuickTunnel(cleanupContext, layout, previousName)
		cleanupCancel()
		if cleanupErr != nil {
			fmt.Fprintf(a.errOut, "could not clean up the previous temporary setup tunnel: %v\n", cleanupErr)
			return 1
		}
	}
	containerName := newSetupContainerName()
	network := strings.TrimSpace(values["STEALTH_NETWORK_NAME"])
	if network == "" {
		network = quickTunnelNetwork
	}
	quickURL, tunnelErr := a.startQuickTunnelTo(ctx, layout, network, containerName, "http://setup-proxy:80")
	if tunnelErr != nil {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = a.closeQuickTunnel(cleanupContext, layout, containerName)
		cleanupCancel()
		a.printQuickTunnelFallback(tunnelErr)
		return 1
	}
	if err := a.registerQuickTunnel(ctx, apiURL+"/v1/setup/quick-tunnel", key, containerName, quickURL); err != nil {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = a.closeQuickTunnel(cleanupContext, layout, containerName)
		cleanupCancel()
		fmt.Fprintf(a.errOut, "could not register the temporary setup tunnel: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.out, "✓ Temporary setup tunnel started")
	fmt.Fprintf(a.out, "\nOpen %s/setup\n", quickURL)
	fmt.Fprintf(a.out, "Setup code: %s\n", session.SetupCode)
	fmt.Fprintf(a.out, "Expires: %s (15 minutes)\n", session.ExpiresAt.UTC().Format(time.RFC3339))
	fmt.Fprintln(a.out, "Complete setup in the browser. The temporary tunnel closes after the named production tunnel is verified.")
	return 0
}

func (a *App) loadSetupState(values map[string]string) (setupstate.State, error) {
	store, err := a.setupStateStore(values)
	if err != nil {
		return setupstate.State{}, err
	}
	return store.Load(context.Background())
}

func (a *App) setupStateStore(values map[string]string) (*setupstate.FileStore, error) {
	statePath := strings.TrimSpace(values["STEALTH_SETUP_STATE_FILE"])
	if statePath == "" {
		statePath = filepath.Join(strings.TrimRight(a.valueOrInstallRoot(values), "/"), "state", "setup-state.enc")
	}
	key, err := decodeConfigSecret(values["FUNCTIONS_SECRET_KEY"])
	if err != nil {
		return nil, err
	}
	cipher, err := functionsecret.New(key)
	if err != nil {
		return nil, err
	}
	return setupstate.NewFileStore(statePath, cipher)
}

func (a *App) previousSetupTunnel(values map[string]string) string {
	store, err := a.setupStateStore(values)
	if err != nil {
		return ""
	}
	state, err := store.Load(context.Background())
	if err != nil || !isQuickTunnelContainerName(state.QuickTunnel) {
		return ""
	}
	return state.QuickTunnel
}

func (a *App) valueOrInstallRoot(values map[string]string) string {
	if root := strings.TrimSpace(values["STEALTH_INSTALL_ROOT"]); root != "" {
		return root
	}
	if a != nil && a.homeDir != "" {
		return filepath.Join(a.homeDir, defaultHomeName)
	}
	return ""
}

func decodeConfigSecret(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("secret is empty")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(raw)
	}
	if err != nil || len(key) != functionsecret.KeySize {
		return nil, errors.New("secret is invalid")
	}
	return key, nil
}

func (a *App) runSetup(args []string) int {
	fs := flag.NewFlagSet("stealth setup", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	adoptOwner := fs.Bool("adopt-owner", false, "assign an existing account as Instance Owner on a legacy installation")
	fs.Usage = func() {
		fmt.Fprintln(a.errOut, "Usage: stealth setup [--adopt-owner]")
		fmt.Fprintln(a.errOut)
		fmt.Fprintln(a.errOut, "Resume first-run Instance Owner setup for an installed Stealth instance.")
		fmt.Fprintln(a.errOut, "The setup code expires after 15 minutes and the temporary onboarding tunnel is closed after use.")
		fmt.Fprintln(a.errOut, "Use --adopt-owner only from the local operator terminal to migrate an existing account.")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.errOut, "setup does not accept positional arguments")
		return 2
	}
	if *adoptOwner && !a.hasInteractiveTerminal() {
		fmt.Fprintln(a.errOut, "Owner adoption requires an interactive TTY.")
		fmt.Fprintln(a.errOut, "Run `stealth setup --adopt-owner` from a terminal; the installation was left unchanged.")
		return 1
	}
	ctx, stop := signalContext()
	defer stop()
	if *adoptOwner {
		return a.runAdoptOwnerWithContext(ctx)
	}
	return a.runSetupWithContext(ctx)
}

func (a *App) runAdoptOwnerWithContext(ctx context.Context) int {
	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	if !installationExists(layout) {
		fmt.Fprintln(a.errOut, "No complete Stealth installation was found. The installation was left unchanged.")
		return 1
	}
	values, err := readEnvFile(layout.EnvFile)
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read installation configuration: %v\n", err)
		return 1
	}
	key, err := bootstrapCLIKey(values)
	if err != nil {
		fmt.Fprintln(a.errOut, err)
		return 1
	}
	ports := portsFromConfig(values)
	accounts, err := a.bootstrapAdoptionAccounts(ctx, "http://127.0.0.1:"+ports.API+"/v1/bootstrap/adoption/accounts", key)
	if err != nil {
		fmt.Fprintf(a.errOut, "could not list accounts eligible for adoption: %v\n", err)
		fmt.Fprintln(a.errOut, "The installation and data were left unchanged.")
		return 1
	}
	if len(accounts) == 0 {
		fmt.Fprintln(a.out, "No existing account is available for Instance Owner adoption.")
		return 1
	}
	fmt.Fprintln(a.out, "Existing installation: choose the account to become the Instance Owner")
	fmt.Fprintln(a.out)
	for index, account := range accounts {
		identity := account.Email
		if identity == "" && account.ProviderLogin != "" {
			identity = "GitHub @" + account.ProviderLogin
		}
		if identity == "" {
			identity = "account without email"
		}
		fmt.Fprintf(a.out, "  %d. %s  (%s)\n", index+1, identity, account.ID)
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, "This assigns a privileged instance-level role; it does not change organization membership.")
	fmt.Fprintln(a.out, "Type the exact confirmation below to continue. Anything else cancels safely.")

	reader := bufio.NewReader(a.in)
	fmt.Fprint(a.out, "Confirm with: ADOPT <account-id>\n> ")
	line, readErr := reader.ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		fmt.Fprintf(a.errOut, "could not read confirmation: %v\n", readErr)
		return 1
	}
	confirmation := strings.TrimSpace(line)
	selectedID := ""
	for _, account := range accounts {
		if confirmation == "ADOPT "+account.ID {
			selectedID = account.ID
			break
		}
	}
	if selectedID == "" {
		fmt.Fprintln(a.out, "Adoption cancelled. The installation and data were left unchanged.")
		return 1
	}
	if err := a.adoptBootstrapOwner(ctx, "http://127.0.0.1:"+ports.API+"/v1/bootstrap/adoption/owner", key, selectedID); err != nil {
		fmt.Fprintf(a.errOut, "Instance Owner adoption failed: %v\n", err)
		fmt.Fprintln(a.errOut, "The installation and data were left unchanged.")
		return 1
	}
	fmt.Fprintf(a.out, "Instance Owner assigned to account %s.\n", selectedID)
	return 0
}

func (a *App) runSetupWithContext(ctx context.Context) int {
	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	if !installationExists(layout) {
		if partialInstallationExists(layout) {
			fmt.Fprintf(a.errOut, "A partial Stealth installation was found at %s, but config.env is missing.\n", layout.Root)
			fmt.Fprintln(a.errOut, "The installation was left unchanged. Run `stealth install --repair` or `stealth doctor` to recover it.")
			return 1
		}
		fmt.Fprintln(a.errOut, "No Stealth installation was found. Run `stealth install` first.")
		return 1
	}
	values, err := readEnvFile(layout.EnvFile)
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read installation configuration: %v\n", err)
		return 1
	}
	if strings.EqualFold(strings.TrimSpace(values["SETUP_MODE"]), "true") {
		version := readVersion(layout)
		if version == "" {
			version, err = imageVersion(values["STEALTH_API_IMAGE"])
			if err != nil {
				fmt.Fprintf(a.errOut, "could not determine setup release version: %v\n", err)
				return 1
			}
		}
		return a.runWebBootstrap(ctx, a.systemChecks(ctx, layout.Root), layout, version, true)
	}
	if statePath := strings.TrimSpace(values["STEALTH_SETUP_STATE_FILE"]); statePath != "" && installengine.FileExists(statePath) {
		if state, stateErr := a.loadSetupState(values); stateErr == nil && state.InstallRunID != "" && state.Phase != setupstate.PhaseComplete {
			version := readVersion(layout)
			if version == "" {
				version, err = imageVersion(values["STEALTH_API_IMAGE"])
				if err != nil {
					fmt.Fprintf(a.errOut, "could not determine setup release version: %v\n", err)
					return 1
				}
			}
			return a.runWebBootstrap(ctx, a.systemChecks(ctx, layout.Root), layout, version, true)
		}
	}
	ports := portsFromConfig(values)
	apiURL := "http://127.0.0.1:" + ports.API
	status, err := a.bootstrapStatus(ctx, apiURL+"/v1/bootstrap/status")
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read first-run setup state: %v\n", err)
		fmt.Fprintln(a.errOut, "The installation was left unchanged. Run `stealth doctor` for diagnostics.")
		return 1
	}
	if !status.SetupRequired {
		fmt.Fprintln(a.out, "Instance setup has already been completed.")
		return 0
	}
	if !a.hasInteractiveTerminal() {
		fmt.Fprintln(a.errOut, "First-run owner setup requires an interactive TTY.")
		return 1
	}
	if err := a.waitForInstallation(ctx, InstallPlan{Layout: layout}); err != nil {
		fmt.Fprintf(a.errOut, "the local Stealth services are not healthy: %v\n", err)
		fmt.Fprintln(a.errOut, "No temporary onboarding tunnel was started and the installation was left unchanged.")
		fmt.Fprintln(a.errOut, "Run `stealth doctor` and retry `stealth setup` after the stack is healthy.")
		return 1
	}
	return a.runSetupTUI(ctx, layout, values, apiURL, "http://127.0.0.1:"+ports.Proxy+"/setup")
}

// signalContext mirrors the install command's signal handling while keeping
// the setup flow independently resumable after a user cancellation.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
func (a *App) prepareSetup(ctx context.Context, layout InstallLayout, values map[string]string, apiURL, localURL, containerName, existingTunnelURL string, tunnelState *setupTunnelState) setupPreparedMessage {
	status, err := a.bootstrapStatus(ctx, apiURL+"/v1/bootstrap/status")
	if err != nil {
		if isBootstrapComplete(err) {
			return setupPreparedMessage{complete: true}
		}
		return setupPreparedMessage{err: err}
	}
	if !status.SetupRequired {
		return setupPreparedMessage{complete: true}
	}
	key, err := bootstrapCLIKey(values)
	if err != nil {
		return setupPreparedMessage{err: err}
	}
	session, err := a.createBootstrapSession(ctx, apiURL+"/v1/bootstrap/sessions", key)
	if err != nil {
		if isBootstrapComplete(err) {
			return setupPreparedMessage{complete: true}
		}
		return setupPreparedMessage{err: err}
	}
	message := setupPreparedMessage{session: session, localURL: localURL, containerName: containerName}
	if existingTunnelURL != "" {
		message.tunnelURL = existingTunnelURL
		return message
	}
	network := strings.TrimSpace(values["STEALTH_NETWORK_NAME"])
	if network == "" {
		network = quickTunnelNetwork
	}
	if !safeDockerName(network) {
		message.warning = "the temporary tunnel could not use the configured Docker network; continue with the local setup URL"
		message.containerName = ""
		return message
	}
	// Record the generated name before Docker is invoked. If Ctrl+C arrives
	// while docker run or log discovery is still in flight, the caller can
	// still find and remove this exact temporary container.
	if tunnelState != nil {
		tunnelState.markStarted(containerName)
	}
	tunnelName, tunnelErr := a.startQuickTunnel(ctx, layout, network, containerName)
	if tunnelErr != nil {
		if tunnelName != "" {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cleanupErr := a.closeQuickTunnel(cleanupContext, layout, tunnelName)
			cancel()
			if cleanupErr != nil {
				return setupPreparedMessage{err: fmt.Errorf("temporary onboarding tunnel failed and could not be cleaned up: %w", cleanupErr)}
			}
		}
		message.warning = "the temporary tunnel was unavailable; continue with the local setup URL"
		message.containerName = ""
		return message
	}
	message.tunnelURL = tunnelName
	return message
}

func (a *App) bootstrapStatus(ctx context.Context, endpoint string) (bootstrapStatusPayload, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return bootstrapStatusPayload{}, err
	}
	response, err := a.httpClient.Do(request)
	if err != nil {
		return bootstrapStatusPayload{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return bootstrapStatusPayload{}, &bootstrapHTTPError{status: response.StatusCode}
	}
	var payload bootstrapStatusPayload
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return bootstrapStatusPayload{}, fmt.Errorf("decode bootstrap status: %w", err)
	}
	return payload, nil
}

func (a *App) bootstrapAdoptionAccounts(ctx context.Context, endpoint string, key []byte) ([]bootstrapAdoptionAccountPayload, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(key))
	response, err := a.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &bootstrapHTTPError{status: response.StatusCode}
	}
	var payload bootstrapAdoptionAccountsPayload
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode adoption accounts: %w", err)
	}
	return payload.Accounts, nil
}

func (a *App) adoptBootstrapOwner(ctx context.Context, endpoint string, key []byte, accountID string) error {
	body, err := json.Marshal(map[string]string{"account_id": accountID})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(key))
	response, err := a.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &bootstrapHTTPError{status: response.StatusCode}
	}
	return nil
}

func (a *App) createBootstrapSession(ctx context.Context, endpoint string, key []byte) (bootstrapSessionPayload, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		return bootstrapSessionPayload{}, err
	}
	request.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(key))
	response, err := a.httpClient.Do(request)
	if err != nil {
		return bootstrapSessionPayload{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return bootstrapSessionPayload{}, &bootstrapHTTPError{status: response.StatusCode}
	}
	var payload bootstrapSessionPayload
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return bootstrapSessionPayload{}, fmt.Errorf("decode bootstrap session: %w", err)
	}
	if !bootstrap.ValidCode(payload.SetupCode) || !payload.ExpiresAt.After(time.Now().UTC()) {
		return bootstrapSessionPayload{}, fmt.Errorf("bootstrap API returned an invalid setup session")
	}
	return payload, nil
}

func (a *App) recoverSetupSession(ctx context.Context, endpoint string, key []byte) (bootstrapSessionPayload, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		return bootstrapSessionPayload{}, err
	}
	request.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(key))
	response, err := a.httpClient.Do(request)
	if err != nil {
		return bootstrapSessionPayload{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return bootstrapSessionPayload{}, &bootstrapHTTPError{status: response.StatusCode}
	}
	var payload bootstrapSessionPayload
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return bootstrapSessionPayload{}, fmt.Errorf("decode setup recovery session: %w", err)
	}
	if !bootstrap.ValidCode(payload.SetupCode) || !payload.ExpiresAt.After(time.Now().UTC()) {
		return bootstrapSessionPayload{}, fmt.Errorf("setup API returned an invalid recovery session")
	}
	return payload, nil
}

func bootstrapCLIKey(values map[string]string) ([]byte, error) {
	raw := strings.TrimSpace(values["BOOTSTRAP_CLI_KEY"])
	if raw == "" {
		return nil, fmt.Errorf("installation config has no dedicated bootstrap CLI key; add BOOTSTRAP_CLI_KEY before retrying (FUNCTIONS_SECRET_KEY cannot be reused)")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(raw)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("installation bootstrap CLI key is invalid")
	}
	return key, nil
}

func isBootstrapComplete(err error) bool {
	var statusErr *bootstrapHTTPError
	return errors.As(err, &statusErr) && statusErr.status == http.StatusGone
}

func (a *App) startQuickTunnel(ctx context.Context, layout InstallLayout, network, containerName string) (string, error) {
	return a.startQuickTunnelTo(ctx, layout, network, containerName, "http://proxy:80")
}

func (a *App) startQuickTunnelTo(ctx context.Context, layout InstallLayout, network, containerName, target string) (string, error) {
	if !isQuickTunnelContainerName(containerName) {
		return "", fmt.Errorf("temporary tunnel container name is invalid")
	}
	if !safeDockerName(network) || target != "http://proxy:80" && target != "http://setup-proxy:80" {
		return "", fmt.Errorf("temporary tunnel target is invalid")
	}
	if _, err := a.runner.Output(ctx, layout.Root, "docker", "run", "--detach", "--name", containerName, "--network", network, "--pull=missing", quickTunnelCloudflaredImage, "tunnel", "--no-autoupdate", "--url", target); err != nil {
		return containerName, fmt.Errorf("start temporary onboarding tunnel: %w", err)
	}
	var lastLogs []byte
	for attempt := 0; attempt < positiveAttempts(a.pollAttempts); attempt++ {
		if err := ctx.Err(); err != nil {
			return containerName, err
		}
		// cloudflared writes its Quick Tunnel URL to stderr, which
		// exec.Cmd.Output() would discard. Read the combined stream so the URL
		// is visible whether cloudflared logs to stdout or stderr.
		logs, logsErr := a.runner.CombinedOutput(ctx, layout.Root, "docker", "logs", containerName)
		if logsErr == nil {
			lastLogs = logs
			if tunnelURL, found := findQuickTunnelURL(logs); found {
				return tunnelURL, nil
			}
		}
		// Do not burn the whole deadline if the temporary container already
		// exited without publishing a URL.
		if stopped, statusErr := a.quickTunnelContainerStopped(ctx, layout, containerName); statusErr == nil && stopped {
			return containerName, quickTunnelExitedError(lastLogs)
		}
		if attempt+1 < positiveAttempts(a.pollAttempts) {
			if err := waitSetupPoll(ctx, a.pollInterval); err != nil {
				return containerName, err
			}
		}
	}
	return containerName, fmt.Errorf("temporary onboarding tunnel did not publish a TryCloudflare URL in time")
}

// quickTunnelContainerStopped reports whether the temporary cloudflared
// container has already reached a terminal state. A missing or unreadable
// status is treated as "unknown" so a transient Docker error never aborts a
// tunnel that could still publish a URL.
func (a *App) quickTunnelContainerStopped(ctx context.Context, layout InstallLayout, containerName string) (bool, error) {
	output, err := a.runner.Output(ctx, layout.Root, "docker", "inspect", "--format", "{{.State.Status}}", containerName)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(string(output))) {
	case "exited", "dead":
		return true, nil
	case "created", "running", "restarting", "paused":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected temporary tunnel container state")
	}
}

func quickTunnelExitedError(logs []byte) error {
	excerpt := sanitizeTunnelLogs(logs)
	if excerpt == "" {
		return fmt.Errorf("temporary Quick Tunnel exited before publishing a setup URL")
	}
	return fmt.Errorf("temporary Quick Tunnel exited before publishing a setup URL\n\ncloudflared:\n%s", excerpt)
}

// printQuickTunnelFallback explains how to reach the setup service when the
// temporary tunnel cannot start. The setup service stays bound to the loopback
// address on the host; it is never exposed publicly. The CLI does not know the
// operator's SSH username or server name, so it prints a generic template.
func (a *App) printQuickTunnelFallback(tunnelErr error) {
	fmt.Fprintln(a.errOut, "Quick Tunnel could not be started.")
	fmt.Fprintf(a.errOut, "%v\n", tunnelErr)
	fmt.Fprintln(a.errOut)
	fmt.Fprintln(a.errOut, "The setup service is still running securely on the VPS.")
	fmt.Fprintln(a.errOut)
	fmt.Fprintln(a.errOut, "From your local computer, run:")
	fmt.Fprintln(a.errOut)
	fmt.Fprintln(a.errOut, "  ssh -L 8081:127.0.0.1:8081 <user>@<server>")
	fmt.Fprintln(a.errOut)
	fmt.Fprintln(a.errOut, "Then open:")
	fmt.Fprintln(a.errOut)
	fmt.Fprintln(a.errOut, "  http://localhost:8081/setup")
}

func (a *App) registerQuickTunnel(ctx context.Context, endpoint string, key []byte, containerName, tunnelURL string) error {
	body, err := json.Marshal(setupQuickTunnelPayload{ContainerName: containerName, URL: tunnelURL})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(bootstrap.CLIProofHeader, bootstrap.CLIProof(key))
	response, err := a.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &bootstrapHTTPError{status: response.StatusCode}
	}
	return nil
}

func positiveAttempts(value int) int {
	if value < 1 {
		return 1
	}
	return value
}

func waitSetupPoll(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseQuickTunnelURL(output []byte) string {
	found, _ := findQuickTunnelURL(output)
	return found
}

// findQuickTunnelURL returns the first safe TryCloudflare Quick Tunnel URL in
// cloudflared logs. It accepts only HTTPS URLs whose host is directly under
// trycloudflare.com, ignores unrelated or lookalike URLs, and tolerates the
// surrounding informational and warning noise cloudflared emits.
func findQuickTunnelURL(logs []byte) (string, bool) {
	text := string(logs)
	for _, indexes := range quickTunnelURLPattern.FindAllStringIndex(text, -1) {
		if end := indexes[1]; end < len(text) && isURLHostContinuation(text, end) {
			continue
		}
		match := text[indexes[0]:indexes[1]]
		parsed, err := url.Parse(match)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if strings.HasSuffix(host, ".trycloudflare.com") && strings.TrimSuffix(host, ".trycloudflare.com") != "" {
			return "https://" + host, true
		}
	}
	return "", false
}

const (
	maxTunnelLogLines = 20
	maxTunnelLogBytes = 4 << 10
)

var (
	tunnelSecretPattern = regexp.MustCompile(`(?i)\b(token|secret|password|passwd|credential|credentials|api[_-]?key|access[_-]?key|client[_-]?secret)\b\s*[:=]\s*("[^"]*"|'[^']*'|\S+)`)
	tunnelBearerPattern = regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]+`)
)

// sanitizeTunnelLogs produces a short log excerpt for an error message while
// redacting values that could carry credentials. The tunnel token, setup code,
// and provider secrets must never reach the operator's terminal or logs.
func sanitizeTunnelLogs(logs []byte) string {
	text := strings.ReplaceAll(string(logs), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > maxTunnelLogLines {
		lines = lines[len(lines)-maxTunnelLogLines:]
	}
	for index, line := range lines {
		line = tunnelSecretPattern.ReplaceAllString(line, "$1=[redacted]")
		line = tunnelBearerPattern.ReplaceAllString(line, "$1 [redacted]")
		lines[index] = line
	}
	excerpt := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(excerpt) > maxTunnelLogBytes {
		excerpt = excerpt[len(excerpt)-maxTunnelLogBytes:]
	}
	return excerpt
}

func isURLHostContinuation(text string, start int) bool {
	character := text[start]
	if character != '.' && character != '-' && (character < '0' || character > '9') && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') {
		return false
	}
	// A period followed by whitespace or punctuation can be ordinary sentence
	// punctuation. A period followed by another hostname character is a
	// lookalike suffix such as .evil and must not be silently truncated.
	if character == '.' && (start+1 == len(text) || !isURLHostContinuationCharacter(text[start+1])) {
		return false
	}
	return true
}

func isURLHostContinuationCharacter(character byte) bool {
	return character == '.' || character == '-' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func (a *App) closeQuickTunnel(ctx context.Context, layout InstallLayout, containerName string) error {
	if !isQuickTunnelContainerName(containerName) {
		return nil
	}
	containers, err := a.runner.Output(ctx, layout.Root, "docker", "ps", "--all", "--filter", "name=^"+containerName+"$", "--format", "{{.Names}}")
	if err != nil {
		return fmt.Errorf("check temporary onboarding tunnel: %w", err)
	}
	found := false
	for _, candidate := range strings.Split(string(containers), "\n") {
		if strings.TrimSpace(candidate) == containerName {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	return a.runner.Run(ctx, layout.Root, io.Discard, io.Discard, "docker", "rm", "--force", containerName)
}

func isQuickTunnelContainerName(value string) bool {
	return strings.HasPrefix(value, quickTunnelContainerPrefix) && safeDockerName(value)
}

func safeDockerName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			if index == 0 && (character == '.' || character == '_' || character == '-') {
				return false
			}
			continue
		}
		return false
	}
	return true
}
