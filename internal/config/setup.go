package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type setupSettings struct {
	mode                   bool
	installRoot            string
	stateFile              string
	handoffFile            string
	productionCompose      string
	setupCompose           string
	cloudflareClientID     string
	cloudflareClientSecret string
	cloudflareAPIBaseURL   string
}

func loadSetupSettings() (setupSettings, error) {
	mode, err := strconv.ParseBool(value("SETUP_MODE", "false"))
	if err != nil {
		return setupSettings{}, fmt.Errorf("SETUP_MODE must be true or false")
	}
	root := strings.TrimSpace(os.Getenv("STEALTH_INSTALL_ROOT"))
	if root != "" {
		root, err = filepath.Abs(root)
		if err != nil {
			return setupSettings{}, fmt.Errorf("STEALTH_INSTALL_ROOT must be a valid filesystem path")
		}
	}
	stateFile := strings.TrimSpace(os.Getenv("STEALTH_SETUP_STATE_FILE"))
	if stateFile == "" && root != "" {
		stateFile = filepath.Join(root, "state", "setup-state.enc")
	}
	productionCompose := strings.TrimSpace(os.Getenv("STEALTH_PRODUCTION_COMPOSE_FILE"))
	if productionCompose == "" && root != "" {
		productionCompose = filepath.Join(root, "compose.production.yaml")
	}
	handoffFile := strings.TrimSpace(os.Getenv("STEALTH_SETUP_HANDOFF_FILE"))
	if handoffFile == "" {
		handoffFile = "/var/lib/stealth/storage/.setup-handoff.enc"
	}
	setupCompose := strings.TrimSpace(os.Getenv("STEALTH_SETUP_COMPOSE_FILE"))
	if setupCompose == "" && root != "" {
		setupCompose = filepath.Join(root, "compose.setup.yaml")
	}
	return setupSettings{
		mode:                   mode,
		installRoot:            root,
		stateFile:              stateFile,
		handoffFile:            handoffFile,
		productionCompose:      productionCompose,
		setupCompose:           setupCompose,
		cloudflareClientID:     strings.TrimSpace(os.Getenv("CLOUDFLARE_OAUTH_CLIENT_ID")),
		cloudflareClientSecret: os.Getenv("CLOUDFLARE_OAUTH_CLIENT_SECRET"),
		cloudflareAPIBaseURL:   strings.TrimSpace(os.Getenv("CLOUDFLARE_API_BASE_URL")),
	}, nil
}

func (s setupSettings) apply(c *Config) {
	c.SetupMode = s.mode
	c.InstallRoot = s.installRoot
	c.SetupStateFile = s.stateFile
	c.SetupHandoffFile = s.handoffFile
	c.ProductionComposeFile = s.productionCompose
	c.SetupComposeFile = s.setupCompose
	c.CloudflareOAuthClientID = s.cloudflareClientID
	c.CloudflareOAuthClientSecret = s.cloudflareClientSecret
	c.CloudflareAPIBaseURL = s.cloudflareAPIBaseURL
}
