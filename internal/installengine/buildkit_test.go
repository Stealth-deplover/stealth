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
			"${STEALTH_INSTALL_ROOT:-.}/private/buildkit-mtls/health/key.pem:/input/health-client-key.pem:ro",
			"${STEALTH_INSTALL_ROOT:-.}/private/buildkit-mtls/worker/key.pem:/input/client-key.pem:ro", 1),
		"healthcheck client identity omitted": strings.Replace(valid,
			`"/run/secrets/stealth-buildkit/health-client-key.pem", `, "", 1),
		"worker initializer Compose interpolation is not escaped": strings.Replace(valid, "$$stale", "$stale", 1),
		"BuildKit service receives host key paths": strings.Replace(valid,
			"buildkit_server_credentials:/run/secrets/stealth-buildkit:ro",
			"${STEALTH_INSTALL_ROOT:-.}/private/buildkit-mtls/server/key.pem:/run/secrets/stealth-buildkit/server-key.pem:ro", 1),
		"Cloudflare source initializer mounts private parent": strings.Replace(valid,
			"${STEALTH_INSTALL_ROOT:-.}/state:/source:ro",
			"${STEALTH_INSTALL_ROOT:-.}:/source:ro", 1),
		"Cloudflare state initializer mounts all state": strings.Replace(valid,
			"cloudflare_setup_state_input:/input:ro",
			"${STEALTH_INSTALL_ROOT:-.}/state:/state:ro", 1),
		"Cloudflare source initializer adds another capability": strings.Replace(valid,
			"cap_add: [CHOWN, DAC_READ_SEARCH]",
			"cap_add: [CHOWN, DAC_READ_SEARCH, SYS_ADMIN]", 1),
		"unrelated service mounts private parent": strings.Replace(valid,
			"  traefik:\n", "  traefik:\n    volumes:\n      - ./private:/private:ro\n", 1),
		"unrelated service mounts installation root": strings.Replace(valid,
			"  traefik:\n", "  traefik:\n    volumes:\n      - ./:/install:ro\n", 1),
		"telemetry does not mask its host filesystem view": strings.Replace(valid,
			"      - type: tmpfs\n        target: /hostfs/${STEALTH_INSTALL_ROOT:?set STEALTH_INSTALL_ROOT}/private\n        read_only: true\n        tmpfs:\n          size: 1048576\n", "", 1),
		"BuildKit initializer mounts PKI parent": strings.Replace(valid,
			"${STEALTH_INSTALL_ROOT:-.}/private/buildkit-mtls/server/key.pem:/input/server-key.pem:ro\"\n",
			"${STEALTH_INSTALL_ROOT:-.}/private/buildkit-mtls/server/key.pem:/input/server-key.pem:ro\"\n      - ${STEALTH_INSTALL_ROOT:-.}/private/buildkit-mtls:/input/pki:ro\n", 1),
	}
	for name, asset := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateProductionComposeAsset([]byte(asset)); err == nil {
				t.Fatalf("unsafe credential layout %q was accepted", name)
			}
		})
	}
}
