// Command cloudflare-import-init derives a Cloudflare-only encrypted artifact
// from legacy setup state for the production worker's one-time PostgreSQL
// import. It is network-free and accepts no command-line arguments.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/cloudflareimport"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
)

const (
	sourcePath      = "/state/setup-state.enc"
	sourceDirectory = "/state"
	destinationPath = "/output/cloudflare-import.enc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if len(os.Args) != 1 {
		logger.Error("Cloudflare import preparation accepts no arguments")
		os.Exit(2)
	}
	cipher, err := configuredCipher()
	if err != nil {
		logger.Error("Cloudflare import preparation cannot validate its encryption key; worker startup is blocked")
		os.Exit(1)
	}
	owner, err := cloudflareimport.OwnerForSourceDirectory(sourceDirectory)
	if err != nil {
		logger.Error("Cloudflare import preparation cannot validate the source state directory; worker startup is blocked")
		os.Exit(1)
	}
	outcome, err := cloudflareimport.Prepare(context.Background(), sourcePath, destinationPath, cipher, owner)
	if err != nil {
		// Recovery is optional, but a directory-boundary failure must prevent
		// worker startup. Package errors never include decrypted state or secrets.
		if errors.Is(err, cloudflareimport.ErrWorkerBoundaryUnsafe) {
			logger.Error("Cloudflare import directory is unsafe; worker startup is blocked", "reason", safeError(err))
			os.Exit(1)
		}
		logger.Warn("Cloudflare import preparation could not recover legacy state", "reason", safeError(err))
		return
	}
	logger.Info("Cloudflare import preparation completed", "result", outcome)
}

func configuredCipher() (*functionsecret.Cipher, error) {
	raw := strings.TrimSpace(os.Getenv("FUNCTIONS_SECRET_KEY"))
	if raw == "" {
		return nil, errors.New("missing encryption key")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(raw)
	}
	if err != nil || len(key) != functionsecret.KeySize {
		return nil, errors.New("invalid encryption key")
	}
	return functionsecret.New(key)
}

func safeError(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "legacy setup snapshot is missing"
	default:
		return err.Error()
	}
}
