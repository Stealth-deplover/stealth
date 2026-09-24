package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
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
			os.Exit(1)
		} else if !errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(os.Stderr, "STEALTH_BUILDKIT_MTLS_PROBE: credential entry %d could not be checked\n", index+1)
			os.Exit(1)
		}
	}
}
