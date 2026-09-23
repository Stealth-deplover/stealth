package installengine

import (
	"bytes"
	"fmt"
)

func validateBuildKitConfigAsset(contents []byte) error {
	for _, marker := range []string{
		"[worker.oci]", "rootless = true", "noProcessSandbox = false",
		"gc = true", "maxUsedSpace = \"10GB\"", "max-parallelism = 2",
		"[frontend.\"dockerfile.v0\"]", "enabled = true",
	} {
		if !bytes.Contains(contents, []byte(marker)) {
			return fmt.Errorf("BuildKit configuration is missing required setting %q", marker)
		}
	}
	for _, forbidden := range []string{"insecure-entitlements", "security.insecure", "network.host", "gateway.v0", "rootless = false"} {
		if bytes.Contains(contents, []byte(forbidden)) {
			return fmt.Errorf("BuildKit configuration contains forbidden setting %q", forbidden)
		}
	}
	return nil
}
