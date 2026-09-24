// Command cloudflare-state-init publishes only the optional encrypted legacy
// setup snapshot into a narrow volume consumed by the Cloudflare importer.
// It is networkless and receives no setup decryption key.
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/cloudflareimport"
)

const (
	sourcePath     = "/source/setup-state.enc"
	inputDirectory = "/output"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if len(os.Args) != 1 {
		logger.Error("Cloudflare setup-state handoff accepts no arguments")
		os.Exit(2)
	}
	published, err := cloudflareimport.PublishLegacySetupSnapshot(context.Background(), sourcePath, inputDirectory)
	if err != nil {
		logger.Error("Cloudflare setup-state handoff rejected unsafe or unavailable source state", "reason", safeReason(err))
		os.Exit(1)
	}
	result := cloudflareimport.OutcomeNoImport
	if published {
		result = "source_published"
	}
	logger.Info("Cloudflare setup-state handoff completed", "result", result)
}

func safeReason(err error) string {
	if err == nil {
		return "unknown source error"
	}
	// The importer errors are deliberately path and payload independent. Keep
	// the log bounded in case a future implementation adds detail.
	reason := strings.TrimSpace(err.Error())
	if len(reason) > 160 {
		return "source validation failed"
	}
	return reason
}
