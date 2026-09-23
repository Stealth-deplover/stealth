package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/installengine"
)

func (a *App) runIngress(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.errOut, "Usage: stealth ingress status|verify [--site-hostname HOST --site-sha256 DIGEST]|cutover|rollback")
		return 2
	}
	operation := args[0]
	if operation != "status" && operation != "verify" && operation != "cutover" && operation != "rollback" {
		fmt.Fprintf(a.errOut, "unknown ingress operation %q\n", operation)
		return 2
	}
	var siteHostname, siteSHA256 string
	if operation == "verify" {
		flags := flag.NewFlagSet("stealth ingress verify", flag.ContinueOnError)
		flags.SetOutput(a.errOut)
		flags.StringVar(&siteHostname, "site-hostname", "", "verify one public platform Site hostname")
		flags.StringVar(&siteSHA256, "site-sha256", "", "require a matching Site response body SHA-256")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if flags.NArg() != 0 || siteSHA256 != "" && siteHostname == "" {
			fmt.Fprintln(a.errOut, "verify accepts only --site-hostname and optional --site-sha256")
			return 2
		}
	} else if len(args) != 1 {
		fmt.Fprintf(a.errOut, "ingress %s does not accept arguments\n", operation)
		return 2
	}

	layout, err := a.layout()
	if err != nil {
		fmt.Fprintf(a.errOut, "cannot determine installation directory: %v\n", err)
		return 1
	}
	if !installationExists(layout) {
		fmt.Fprintf(a.errOut, "Stealth is not installed at %s\n", layout.Root)
		return 1
	}
	values, err := readEnvFile(layout.EnvFile)
	if err != nil {
		fmt.Fprintf(a.errOut, "could not read production configuration: %v\n", err)
		return 1
	}
	if strings.EqualFold(strings.TrimSpace(values["SETUP_MODE"]), "true") {
		fmt.Fprintln(a.errOut, "ingress operations require a completed production installation")
		return 1
	}
	var locks []*os.File
	if operation == "cutover" || operation == "rollback" {
		// install.lock is already the update/repair coordinator. Hold it with a
		// focused ingress lock so no platform replacement races provider work.
		for _, lockSpec := range [][2]string{{"install.lock", "installation"}, {"ingress.lock", "ingress"}} {
			lock, lockErr := installengine.AcquireProcessLock(layout.StateDir, lockSpec[0], lockSpec[1])
			if lockErr != nil {
				for _, held := range locks {
					_ = held.Close()
				}
				fmt.Fprintf(a.errOut, "could not coordinate ingress operation: %v\n", lockErr)
				return 1
			}
			locks = append(locks, lock)
		}
		defer func() {
			for index := len(locks) - 1; index >= 0; index-- {
				_ = locks[index].Close()
			}
		}()
	}
	var proxyStatus ServiceStatus
	if operation == "status" {
		statusCtx, statusCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer statusCancel()
		statuses, statusErr := a.composeStatuses(statusCtx, layout)
		if statusErr != nil {
			fmt.Fprintf(a.errOut, "could not inspect rollback-origin health: %v\n", statusErr)
			return 1
		}
		proxyStatus = statuses["proxy"]
	}
	if operation == "cutover" || operation == "verify" {
		preflightCtx, preflightCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer preflightCancel()
		if err := a.checkIngressServices(preflightCtx, layout, values, operation); err != nil {
			fmt.Fprintf(a.errOut, "ingress preflight failed: %v\n", err)
			return 1
		}
	}

	ctx, stop := signalContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	command := []string{"run", "--rm", "--no-deps", "ingress-control", operation}
	if operation == "verify" && siteHostname != "" {
		command = append(command, "--site-hostname", siteHostname)
		if siteSHA256 != "" {
			command = append(command, "--site-sha256", siteSHA256)
		}
	}
	if err := a.runner.Run(ctx, layout.Root, a.out, a.errOut, "docker", a.composeArgs(layout, command...)...); err != nil {
		fmt.Fprintf(a.errOut, "stealth ingress %s failed: %v\n", operation, err)
		return 1
	}
	if operation == "status" {
		if proxyStatus.Healthy() {
			fmt.Fprintln(a.out, "Rollback origin health: proxy/Nginx healthy")
		} else {
			fmt.Fprintf(a.out, "Rollback origin health: proxy/Nginx %s\n", proxyStatus.Display())
		}
	}
	return 0
}

func (a *App) checkIngressServices(ctx context.Context, layout InstallLayout, config map[string]string, operation string) error {
	statuses, err := a.composeStatuses(ctx, layout)
	if err != nil {
		return err
	}
	required := []string{"cloudflared"}
	switch operation {
	case "cutover", "verify":
		required = append(required, "api", "console", "traefik", "proxy")
	case "rollback":
		required = append(required, "proxy")
	}
	for _, service := range required {
		status := statuses[service]
		if service == "cloudflared" {
			if strings.EqualFold(status.State, "running") || strings.HasPrefix(strings.ToLower(status.Status), "up ") {
				continue
			}
			return errors.New("Cloudflare Named Tunnel is not active for this installation; cutover and verification require the configured tunnel profile")
		}
		if !status.Healthy() {
			return fmt.Errorf("%s is not healthy", service)
		}
	}
	if bundledPostgres(config["DATABASE_URL"]) {
		if !statuses["postgres"].Healthy() {
			return errors.New("bundled PostgreSQL is not healthy")
		}
	}
	return nil
}

func bundledPostgres(databaseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(databaseURL))
	if err != nil {
		return true
	}
	return parsed.Hostname() == "postgres"
}
