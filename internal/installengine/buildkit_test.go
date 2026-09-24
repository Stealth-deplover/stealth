package installengine

import (
	"strings"
	"testing"
)

func TestBuildKitDaemonAssetRequiresMutualTLS(t *testing.T) {
	valid := testBuildKitConfigAsset()
	if err := validateBuildKitConfigAsset([]byte(valid)); err != nil {
		t.Fatalf("valid BuildKit TLS config rejected: %v", err)
	}
	for name, marker := range map[string]string{
		"TLS section":        "[grpc.tls]",
		"server certificate": `cert = "/run/secrets/stealth-buildkit/server-cert.pem"`,
		"server key":         `key = "/run/secrets/stealth-buildkit/server-key.pem"`,
		"client CA":          `ca = "/run/secrets/stealth-buildkit/ca.pem"`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateBuildKitConfigAsset([]byte(strings.Replace(valid, marker, "", 1))); err == nil {
				t.Fatalf("BuildKit daemon config without %s was accepted", name)
			}
		})
	}
}

func TestProductionComposeKeepsBuildKitCredentialsRoleSeparated(t *testing.T) {
	valid := testProductionComposeAsset()
	if err := validateProductionComposeAsset([]byte(valid)); err != nil {
		t.Fatalf("valid BuildKit mTLS service layout rejected: %v", err)
	}
	cases := map[string]string{
		"worker mounts server volume": strings.Replace(valid,
			"buildkit_worker_credentials:/run/secrets/stealth-buildkit:ro",
			"buildkit_server_credentials:/run/secrets/stealth-buildkit:ro", 1),
		"BuildKit initializer sees worker key": strings.Replace(valid,
			"./state/buildkit-mtls/health/key.pem:/input/health-client-key.pem:ro",
			"./state/buildkit-mtls/worker/key.pem:/input/client-key.pem:ro", 1),
		"healthcheck client identity omitted": strings.Replace(valid,
			`"/run/secrets/stealth-buildkit/health-client-key.pem", `, "", 1),
		"BuildKit service receives host key paths": strings.Replace(valid,
			"buildkit_server_credentials:/run/secrets/stealth-buildkit:ro",
			"./state/buildkit-mtls/server/key.pem:/run/secrets/stealth-buildkit/server-key.pem:ro", 1),
	}
	for name, asset := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateProductionComposeAsset([]byte(asset)); err == nil {
				t.Fatalf("unsafe credential layout %q was accepted", name)
			}
		})
	}
}
