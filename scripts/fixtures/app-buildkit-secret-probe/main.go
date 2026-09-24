package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	switch {
	case len(os.Args) == 2 && os.Args[1] == "serve":
		serve()
		return
	case len(os.Args) == 2 && os.Args[1] == "verify-runtime":
		verifyRuntime()
		return
	case len(os.Args) != 1:
		_, _ = fmt.Fprintln(os.Stderr, "usage: buildkit-secret-probe [serve|verify-runtime]")
		os.Exit(2)
	}
	verifyBuildKitSecrets()
}

func verifyBuildKitSecrets() {
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
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("app-runtime-smoke-ok\n"))
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
	response, err := http.Get("http://127.0.0.1:8080/healthz")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "runtime smoke app listener is unavailable: %v\n", err)
		os.Exit(6)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(os.Stderr, "runtime smoke app listener returned HTTP %d\n", response.StatusCode)
		os.Exit(7)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || strings.TrimSpace(string(body)) != "app-runtime-smoke-ok" {
		_, _ = fmt.Fprintln(os.Stderr, "runtime smoke app listener returned an unexpected response")
		os.Exit(8)
	}
}
