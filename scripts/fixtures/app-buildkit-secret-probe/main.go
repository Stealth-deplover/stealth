package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	switch {
	case len(os.Args) == 2 && os.Args[1] == "serve":
		serve()
		return
	case len(os.Args) == 2 && os.Args[1] == "verify-runtime":
		verifyRuntime()
		return
	case len(os.Args) == 2 && os.Args[1] == "verify-runtime-v2":
		verifyRuntimeConfigurationV2()
		return
	case len(os.Args) == 2 && os.Args[1] == "set-unhealthy":
		if err := os.WriteFile("/tmp/stealth-health-unhealthy", []byte("unhealthy\n"), 0o600); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "could not set health fixture state")
			os.Exit(9)
		}
		return
	case len(os.Args) == 2 && os.Args[1] == "set-healthy":
		if err := os.Remove("/tmp/stealth-health-unhealthy"); err != nil && !errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(os.Stderr, "could not clear health fixture state")
			os.Exit(9)
		}
		return
	case len(os.Args) != 1:
		_, _ = fmt.Fprintln(os.Stderr, "usage: buildkit-secret-probe [serve|verify-runtime]")
		os.Exit(2)
	}
	verifyBuildKitSecrets()
}

func verifyBuildKitSecrets() {
	for _, name := range []string{"APP_RUNTIME_SMOKE_MODE", "APP_RUNTIME_SMOKE_SECRET", "APPS_SECRET_KEY"} {
		if _, ok := os.LookupEnv(name); ok {
			_, _ = fmt.Fprintf(os.Stderr, "STEALTH_BUILDKIT_MTLS_PROBE: runtime-only environment variable %s is visible during build\n", name)
			os.Exit(30)
		}
	}
	for index, path := range []string{
		"/run/secrets/stealth-buildkit/ca.pem",
		"/run/secrets/stealth-buildkit/client-cert.pem",
		"/run/secrets/stealth-buildkit/client-key.pem",
		"/run/secrets/stealth-buildkit/server-cert.pem",
		"/run/secrets/stealth-buildkit/server-key.pem",
		"/run/secrets/stealth-buildkit/health-client-cert.pem",
		"/run/secrets/stealth-buildkit/health-client-key.pem",
	} {
		if _, err := os.Lstat(path); err == nil {
			_, _ = fmt.Fprintf(os.Stderr, "STEALTH_BUILDKIT_MTLS_PROBE: credential entry %d is visible\n", index+1)
			os.Exit(10 + index)
		} else if !errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(os.Stderr, "STEALTH_BUILDKIT_MTLS_PROBE: credential entry %d could not be checked\n", index+1)
			os.Exit(20 + index)
		}
	}
}

func serve() {
	if payload, err := os.ReadFile("/payload.txt"); err == nil {
		marker := strings.TrimSpace(string(payload))
		if marker != "" && len(marker) <= 200 {
			// Give the production file-log receiver time to discover the new
			// Docker JSON log file before the first deterministic fixture lines.
			time.Sleep(1500 * time.Millisecond)
			startedAt := time.Now().UnixNano()
			fmt.Printf("STEALTH_APP_RUNTIME_LOG_STDOUT_%s_%d\n", marker, startedAt)
			fmt.Fprintf(os.Stderr, "STEALTH_APP_RUNTIME_LOG_STDERR_%s_%d\n", marker, startedAt)
		}
	}
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := os.Stat("/tmp/stealth-health-unhealthy"); err == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("app-runtime-smoke-unhealthy\n"))
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("app-runtime-smoke-ok\n"))
	})
	http.HandleFunc("/configuration", func(w http.ResponseWriter, _ *http.Request) {
		mode := os.Getenv("APP_RUNTIME_SMOKE_MODE")
		secret := os.Getenv("APP_RUNTIME_SMOKE_SECRET")
		switch {
		case mode == "v1" && secret == "fake-smoke-secret-not-real-v1":
			_, _ = w.Write([]byte("app-config-v1\n"))
		case mode == "v2" && secret == "fake-smoke-secret-not-real-v2":
			_, _ = w.Write([]byte("app-config-v2\n"))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("app-config-unavailable\n"))
		}
	})
	if err := http.ListenAndServe("0.0.0.0:8080", nil); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "app runtime smoke server failed: %v\n", err)
		os.Exit(1)
	}
}

func verifyRuntime() {
	for _, name := range []string{
		"DATABASE_URL",
		"REDIS_URL",
		"FUNCTIONS_SECRET_KEY",
		"CLOUDFLARE_API_TOKEN",
		"APPS_BUILDKIT_CLIENT_KEY",
		"APPS_SECRET_KEY",
	} {
		if _, ok := os.LookupEnv(name); ok {
			_, _ = fmt.Fprintf(os.Stderr, "runtime smoke found forbidden environment variable %s\n", name)
			os.Exit(3)
		}
	}
	for _, path := range []string{
		"/var/run/docker.sock",
		"/var/lib/stealth/storage",
		"/var/lib/stealth/app-build-staging",
		"/var/lib/stealth/runner-staging",
		"/run/secrets/stealth-buildkit",
		"/var/lib/stealth/cloudflare-import",
		"/var/lib/stealth/traefik",
	} {
		if _, err := os.Lstat(path); err == nil {
			_, _ = fmt.Fprintf(os.Stderr, "runtime smoke found forbidden path %s\n", path)
			os.Exit(4)
		} else if !errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(os.Stderr, "runtime smoke could not inspect path %s\n", path)
			os.Exit(5)
		}
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK {
			_, _ = fmt.Fprintf(os.Stderr, "runtime smoke app listener returned HTTP %d\n", response.StatusCode)
			os.Exit(7)
		}
		if readErr != nil || closeErr != nil || strings.TrimSpace(string(body)) != "app-runtime-smoke-ok" {
			_, _ = fmt.Fprintln(os.Stderr, "runtime smoke app listener returned an unexpected response")
			os.Exit(8)
		}
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "runtime smoke app listener is unavailable: %v\n", lastErr)
	os.Exit(6)
}

func verifyRuntimeConfigurationV2() {
	if os.Getenv("APP_RUNTIME_SMOKE_MODE") != "v2" {
		_, _ = fmt.Fprintln(os.Stderr, "runtime smoke variable replacement is missing from the current App container")
		os.Exit(11)
	}
	if os.Getenv("APP_RUNTIME_SMOKE_SECRET") != "fake-smoke-secret-not-real-v2" {
		_, _ = fmt.Fprintln(os.Stderr, "runtime smoke encrypted value replacement is missing from the current App container")
		os.Exit(12)
	}
}
