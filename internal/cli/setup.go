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
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/bootstrap"
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

type setupPreparedMessage struct {
	session       bootstrapSessionPayload
	localURL      string
	tunnelURL     string
	containerName string
	warning       string
	complete      bool
	err           error
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
	if !a.hasInteractiveTerminal() {
		fmt.Fprintln(a.errOut, "First-run owner setup requires an interactive TTY.")
		fmt.Fprintln(a.errOut, "Run `stealth setup` from a terminal; the installation was left unchanged.")
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
	if !isQuickTunnelContainerName(containerName) {
		return "", fmt.Errorf("temporary tunnel container name is invalid")
	}
	if _, err := a.runner.Output(ctx, layout.Root, "docker", "run", "--detach", "--name", containerName, "--network", network, "--pull=missing", quickTunnelCloudflaredImage, "tunnel", "--no-autoupdate", "--url", "http://proxy:80"); err != nil {
		return containerName, fmt.Errorf("start temporary onboarding tunnel: %w", err)
	}
	for attempt := 0; attempt < positiveAttempts(a.pollAttempts); attempt++ {
		if err := ctx.Err(); err != nil {
			return containerName, err
		}
		logs, err := a.runner.Output(ctx, layout.Root, "docker", "logs", containerName)
		if err == nil {
			if tunnelURL := parseQuickTunnelURL(logs); tunnelURL != "" {
				return tunnelURL, nil
			}
		}
		if attempt+1 < positiveAttempts(a.pollAttempts) {
			if err := waitSetupPoll(ctx, a.pollInterval); err != nil {
				return containerName, err
			}
		}
	}
	return containerName, fmt.Errorf("temporary onboarding tunnel did not publish a TryCloudflare URL in time")
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
	text := string(output)
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
			return "https://" + host
		}
	}
	return ""
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
