package installengine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/buildkitpki"
)

func TestLayoutSeparatesStateAndBuildKitPKI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "stealth")
	layout, err := NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if layout.StateDir != filepath.Join(root, "state") || layout.PrivateDir != filepath.Join(root, "private") || layout.BuildKitPKIDir != filepath.Join(root, "private", "buildkit-mtls") {
		t.Fatalf("layout security directories = state:%q private:%q pki:%q", layout.StateDir, layout.PrivateDir, layout.BuildKitPKIDir)
	}
	if relative, err := filepath.Rel(layout.StateDir, layout.BuildKitPKIDir); err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("BuildKit PKI is inside setup state: relative path %q, error %v", relative, err)
	}
}

func TestEnsureBuildKitPKIRelocatesLegacyBundleWithoutChangingIdentity(t *testing.T) {
	layout, err := NewLayout(filepath.Join(t.TempDir(), "stealth"))
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(layout.StateDir, buildkitpki.DirectoryName)
	if _, err := buildkitpki.Ensure(legacyPath); err != nil {
		t.Fatal(err)
	}
	legacy := buildkitpki.PathsAt(legacyPath)
	identityFiles := []string{legacy.CACert, legacy.CAKey, legacy.ServerCert, legacy.ServerKey, legacy.WorkerCert, legacy.WorkerKey, legacy.HealthCert, legacy.HealthKey}
	before := make(map[string][]byte, len(identityFiles))
	for _, path := range identityFiles {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path)] = contents
	}

	changed, err := ensureBuildKitPKI(layout)
	if err != nil || !changed {
		t.Fatalf("ensureBuildKitPKI(legacy) = %v, %v; want relocation", changed, err)
	}
	if _, err := os.Lstat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy BuildKit PKI remains after relocation: %v", err)
	}
	newPaths := buildkitpki.PathsAt(layout.BuildKitPKIDir)
	for key, path := range map[string]string{
		"buildkit-mtls/ca-cert.pem": newPaths.CACert,
		"buildkit-mtls/ca-key.pem":  newPaths.CAKey,
		"server/cert.pem":           newPaths.ServerCert,
		"server/key.pem":            newPaths.ServerKey,
		"worker/cert.pem":           newPaths.WorkerCert,
		"worker/key.pem":            newPaths.WorkerKey,
		"health/cert.pem":           newPaths.HealthCert,
		"health/key.pem":            newPaths.HealthKey,
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if key == "buildkit-mtls/ca-cert.pem" || key == "buildkit-mtls/ca-key.pem" {
			if !bytes.Equal(contents, before["buildkit-mtls/"+filepath.Base(path)]) {
				t.Fatalf("relocation changed %s", key)
			}
		} else if !bytes.Equal(contents, before[key]) {
			t.Fatalf("relocation changed %s", key)
		}
	}
	privateInfo, err := os.Stat(layout.PrivateDir)
	if err != nil || privateInfo.Mode().Perm() != 0o700 {
		t.Fatalf("private directory mode = %v, %v; want 0700", privateInfo, err)
	}
	changed, err = ensureBuildKitPKI(layout)
	if err != nil || changed {
		t.Fatalf("second ensureBuildKitPKI = %v, %v; want unchanged", changed, err)
	}
}

func TestEnsureBuildKitPKIFailsClosedOnCorruptLegacyOrDuplicateBundle(t *testing.T) {
	t.Run("corrupt old bundle is not replaced", func(t *testing.T) {
		layout, err := NewLayout(filepath.Join(t.TempDir(), "stealth"))
		if err != nil {
			t.Fatal(err)
		}
		legacyPath := filepath.Join(layout.StateDir, buildkitpki.DirectoryName)
		if _, err := buildkitpki.Ensure(legacyPath); err != nil {
			t.Fatal(err)
		}
		legacy := buildkitpki.PathsAt(legacyPath)
		if err := os.WriteFile(legacy.ServerCert, []byte("truncated"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ensureBuildKitPKI(layout); err == nil {
			t.Fatal("corrupt old bundle was silently replaced")
		}
		if _, err := os.Stat(legacyPath); err != nil {
			t.Fatalf("corrupt legacy bundle was removed: %v", err)
		}
		if _, err := os.Lstat(layout.BuildKitPKIDir); !os.IsNotExist(err) {
			t.Fatalf("new identity was created beside corrupt legacy bundle: %v", err)
		}
	})

	t.Run("both old and new bundles fail closed", func(t *testing.T) {
		layout, err := NewLayout(filepath.Join(t.TempDir(), "stealth"))
		if err != nil {
			t.Fatal(err)
		}
		legacyPath := filepath.Join(layout.StateDir, buildkitpki.DirectoryName)
		if _, err := buildkitpki.Ensure(legacyPath); err != nil {
			t.Fatal(err)
		}
		if _, err := buildkitpki.Ensure(layout.BuildKitPKIDir); err != nil {
			t.Fatal(err)
		}
		legacyCA, err := os.ReadFile(buildkitpki.PathsAt(legacyPath).CACert)
		if err != nil {
			t.Fatal(err)
		}
		privateCA, err := os.ReadFile(buildkitpki.PathsAt(layout.BuildKitPKIDir).CACert)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ensureBuildKitPKI(layout); err == nil {
			t.Fatal("two existing PKIs were silently selected")
		}
		if got, err := os.ReadFile(buildkitpki.PathsAt(legacyPath).CACert); err != nil || !bytes.Equal(got, legacyCA) {
			t.Fatalf("legacy CA changed after refusal: %v", err)
		}
		if got, err := os.ReadFile(buildkitpki.PathsAt(layout.BuildKitPKIDir).CACert); err != nil || !bytes.Equal(got, privateCA) {
			t.Fatalf("private CA changed after refusal: %v", err)
		}
	})
}

func TestProductionComposeSmokeEnsureBuildKitPKI(t *testing.T) {
	root := os.Getenv("STEALTH_BUILDKIT_PKI_SMOKE_ROOT")
	if root == "" {
		t.Skip("production Compose smoke did not request host BuildKit PKI preparation")
	}
	layout, err := NewLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensureBuildKitPKI(layout); err != nil {
		t.Fatalf("prepare production Compose smoke BuildKit PKI: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(layout.StateDir, buildkitpki.DirectoryName)); !os.IsNotExist(err) {
		t.Fatalf("legacy BuildKit PKI remains in StateDir: %v", err)
	}
}
