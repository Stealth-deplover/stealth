package buildkitmetadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestParseReadsCurrentBuildKitMetadataDescriptorObject(t *testing.T) {
	descriptor, err := json.Marshal(map[string]any{
		"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": testDigest, "size": 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := fmt.Sprintf(`{"containerimage.config.digest":"sha256:%s","containerimage.descriptor":%s,"containerimage.digest":%q}`, strings.Repeat("b", 64), descriptor, testDigest)
	got, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got.ImageDigest != testDigest || got.MediaType != "application/vnd.oci.image.manifest.v1+json" {
		t.Fatalf("Parse() = %#v", got)
	}
}

func TestParseRejectsMalformedMetadata(t *testing.T) {
	for _, input := range []string{
		`{}`, `[]`, `{"containerimage.digest":"sha256:BAD"}`,
		fmt.Sprintf(`{"containerimage.digest":%q,"containerimage.digest":%q}`, testDigest, testDigest),
		fmt.Sprintf(`{"containerimage.digest":%q,"containerimage.descriptor":{"digest":%q,"digest":%q,"size":42,"mediaType":"application/vnd.oci.image.manifest.v1+json"}}`, testDigest, testDigest, testDigest),
		fmt.Sprintf(`{"containerimage.digest":%q} {}`, testDigest),
		fmt.Sprintf(`{"containerimage.digest":%q,"containerimage.descriptor":"not-an-object"}`, testDigest),
		fmt.Sprintf(`{"containerimage.digest":%q,"containerimage.descriptor":{"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1,"mediaType":"application/vnd.oci.image.manifest.v1+json"}}`, testDigest),
	} {
		if _, err := Parse(strings.NewReader(input)); !errors.Is(err, ErrInvalidMetadata) && err == nil {
			t.Fatalf("Parse(%s) accepted malformed metadata", input)
		}
	}
	input := fmt.Sprintf(`{"containerimage.digest":%q,"containerimage.descriptor":{"digest":%q,"digest":%q,"size":42,"mediaType":"application/vnd.oci.image.manifest.v1+json"}}`, testDigest, testDigest, testDigest)
	if _, err := Parse(strings.NewReader(input)); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("Parse() accepted duplicate descriptor fields: %v", err)
	}
}

func TestParseRejectsOversizedMetadata(t *testing.T) {
	if _, err := Parse(strings.NewReader(strings.Repeat("x", MaxMetadataBytes+1))); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestValidDigest(t *testing.T) {
	if !ValidDigest(testDigest) || ValidDigest("sha256:"+strings.Repeat("A", 64)) || ValidDigest(" "+testDigest) {
		t.Fatal("ValidDigest() returned an unexpected result")
	}
}
