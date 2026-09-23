// Command ingress-control is a narrow, one-shot maintenance runtime for the
// host `stealth ingress` commands. It has no Docker client, socket, storage
// mount, or setup-state access.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Stealth-deplover/stealth/internal/cloudflare"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/ingresscontrol"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, logger); err != nil {
		fmt.Fprintln(os.Stderr, safeError(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out *os.File, logger *slog.Logger) error {
	if len(args) == 0 {
		return errors.New("usage: stealth-ingress-control status|verify|cutover|rollback")
	}
	command := args[0]
	var siteHostname, siteSHA256 string
	if command == "verify" {
		flags := flag.NewFlagSet("ingress-control verify", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		flags.StringVar(&siteHostname, "site-hostname", "", "platform Site hostname below workload_base_domain")
		flags.StringVar(&siteSHA256, "site-sha256", "", "expected Site response body SHA-256")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("verify does not accept positional arguments")
		}
	} else if len(args) != 1 {
		return errors.New("status, cutover, and rollback do not accept arguments")
	}
	if command != "status" && command != "verify" && command != "cutover" && command != "rollback" {
		return fmt.Errorf("unknown ingress operation %q", command)
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is not configured")
	}
	secretKey, err := decodeSecretKey(os.Getenv("FUNCTIONS_SECRET_KEY"))
	if err != nil {
		return err
	}
	cipher, err := functionsecret.New(secretKey)
	if err != nil {
		return errors.New("Cloudflare credential encryption key is invalid")
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("DATABASE_URL is invalid")
	}
	poolConfig.MaxConns = 3
	poolConfig.MinConns = 0
	poolConfig.MaxConnLifetime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return errors.New("could not connect to the configured PostgreSQL database")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return errors.New("configured PostgreSQL database is unavailable")
	}
	repo := repository.NewWithDependencies(pool, repository.Dependencies{CloudflareCipher: cipher})
	reconciler, err := cloudflare.NewReconciler(repo, func(token string) (cloudflare.Client, error) {
		return cloudflare.NewClient(token, os.Getenv("CLOUDFLARE_API_BASE_URL"), &http.Client{Timeout: 20 * time.Second})
	}, time.Minute, logger)
	if err != nil {
		return errors.New("Cloudflare reconciler could not be initialized")
	}
	controller, err := ingresscontrol.New(repo, reconciler, ingresscontrol.NewHTTPProbe(), os.Getenv("PUBLIC_APP_URL"), logger)
	if err != nil {
		return err
	}
	switch command {
	case "status":
		return controller.Status(ctx, out)
	case "verify":
		if err := controller.Verify(ctx, siteHostname, siteSHA256); err != nil {
			return err
		}
		fmt.Fprintln(out, "Ingress provider, public Console HTTPS, security headers, and HSTS verified.")
		if siteHostname != "" {
			fmt.Fprintln(out, "Platform Site HTTPS verified.")
		}
		return nil
	case "cutover":
		return controller.Cutover(ctx, out)
	case "rollback":
		return controller.Rollback(ctx, out)
	default:
		return errors.New("unsupported ingress operation")
	}
}

func decodeSecretKey(raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	}
	if err != nil || len(key) != functionsecret.KeySize {
		return nil, errors.New("FUNCTIONS_SECRET_KEY must be base64-encoded 32 bytes")
	}
	return key, nil
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == 0 {
			return ' '
		}
		return character
	}, strings.TrimSpace(err.Error()))
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
