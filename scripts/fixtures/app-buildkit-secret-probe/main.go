package main

import (
	"errors"
	"os"
)

func main() {
	for _, path := range []string{
		"/run/secrets/stealth-buildkit/ca.pem",
		"/run/secrets/stealth-buildkit/client-cert.pem",
		"/run/secrets/stealth-buildkit/client-key.pem",
		"/run/secrets/stealth-buildkit/server-cert.pem",
		"/run/secrets/stealth-buildkit/server-key.pem",
		"/run/secrets/stealth-buildkit/health-client-cert.pem",
		"/run/secrets/stealth-buildkit/health-client-key.pem",
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			os.Exit(1)
		}
	}
}
