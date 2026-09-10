package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestReleaseAsset(t *testing.T) {
	for _, test := range []struct {
		goos   string
		goarch string
		asset  string
	}{
		{"linux", "amd64", "stealth_Linux_x86_64.tar.gz"},
		{"linux", "arm64", "stealth_Linux_arm64.tar.gz"},
	} {
		asset, err := releaseAsset(test.goos, test.goarch)
		if err != nil || asset != test.asset {
			t.Fatalf("releaseAsset(%s/%s) = %q, %v", test.goos, test.goarch, asset, err)
		}
	}
	if _, err := releaseAsset("darwin", "arm64"); err == nil {
		t.Fatal("unsupported platform was accepted")
	}
}

func TestResolveReleaseVersionOverride(t *testing.T) {
	t.Setenv("STEALTH_VERSION", "v2.4.6")
	app := NewApp(nil, nil, nil)
	version, err := app.resolveReleaseVersion("")
	if err != nil || version != "v2.4.6" {
		t.Fatalf("resolveReleaseVersion = %q, %v", version, err)
	}
	if _, err := app.resolveReleaseVersion("v2.4.6-rc.1"); err == nil {
		t.Fatal("pre-release version was accepted")
	}
}

func TestVerifySHA256(t *testing.T) {
	contents := []byte("stealth cli")
	digest := sha256.Sum256(contents)
	if err := verifySHA256(contents, hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256(contents, hex.EncodeToString(make([]byte, sha256.Size))); err == nil {
		t.Fatal("incorrect checksum was accepted")
	}
}

func TestChecksumForAsset(t *testing.T) {
	checksum, err := checksumForAsset("abc  stealth_Linux_x86_64.tar.gz\ndef  stealth_Linux_arm64.tar.gz\n", "stealth_Linux_arm64.tar.gz")
	if err != nil || checksum != "def" {
		t.Fatalf("checksumForAsset = %q, %v", checksum, err)
	}
	if _, err := checksumForAsset("abc  other.tar.gz", "stealth_Linux_arm64.tar.gz"); err == nil {
		t.Fatal("missing checksum was accepted")
	}
}
