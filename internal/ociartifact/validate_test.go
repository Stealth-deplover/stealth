package ociartifact

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

const testManifestMediaType = "application/vnd.oci.image.manifest.v1+json"

func TestValidateOCIArchive(t *testing.T) {
	entries, digest := validEntries(t)
	archive := makeTar(t, entries)
	if err := Validate(bytes.NewReader(archive), digest, int64(len(archive))); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	info, err := Inspect(bytes.NewReader(archive), digest, int64(len(archive)))
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if info.ManifestDigest != digest || info.OS != "linux" || info.Architecture != "amd64" || len(info.LayerDiffIDs) != 1 || info.ArchiveSize != int64(len(archive)) {
		t.Fatalf("Inspect() info = %+v", info)
	}
}

func TestWriteDockerArchiveAddsLoadManifestToVerifiedOCILayout(t *testing.T) {
	entries, digest := validEntries(t)
	archive := makeTar(t, entries)
	info, err := Inspect(bytes.NewReader(archive), digest, int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}

	var converted bytes.Buffer
	if err := WriteDockerArchive(bytes.NewReader(archive), info, &converted); err != nil {
		t.Fatalf("WriteDockerArchive() error = %v", err)
	}
	reader := tar.NewReader(bytes.NewReader(converted.Bytes()))
	var manifestBytes []byte
	seen := make(map[string]bool)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("converted tar read error = %v", err)
		}
		seen[header.Name] = true
		if header.Name == "manifest.json" {
			manifestBytes, err = io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if !seen["oci-layout"] || !seen["index.json"] || !seen[blobPath(info.ConfigDigest)] || !seen[blobPath(info.LayerDigests[0])] {
		t.Fatalf("converted tar omitted OCI entries: %v", seen)
	}
	var manifest []dockerManifestItem
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || len(manifest) != 1 {
		t.Fatalf("Docker load manifest = %s, error = %v", manifestBytes, err)
	}
	if manifest[0].Config != blobPath(info.ConfigDigest) || len(manifest[0].RepoTags) != 0 ||
		len(manifest[0].Layers) != 1 || manifest[0].Layers[0] != blobPath(info.LayerDigests[0]) {
		t.Fatalf("Docker load manifest = %+v", manifest[0])
	}
}

func TestValidateRejectsInvalidOCIArchives(t *testing.T) {
	entries, digest := validEntries(t)
	valid := makeTar(t, entries)
	wrongDigest := "sha256:" + string(bytes.Repeat([]byte{'b'}, 64))
	badIndex := makeTar(t, []tarEntry{{name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)}, {name: "index.json", body: []byte("not-json")}})
	traversalEntries := append(cloneEntries(entries), tarEntry{name: "../escape", body: []byte("x")})
	absoluteEntries := append(cloneEntries(entries), tarEntry{name: "/absolute", body: []byte("x")})
	traversal := makeTar(t, traversalEntries)
	absolute := makeTar(t, absoluteEntries)
	missingLayout := makeTar(t, []tarEntry{{name: "index.json", body: []byte(`{"schemaVersion":2,"manifests":[]}`)}})
	missingIndex := makeTar(t, []tarEntry{{name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)}})
	duplicate := makeTar(t, []tarEntry{{name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)}, {name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)}})
	specialEntries := append(cloneEntries(entries), tarEntry{name: "special", typeflag: tar.TypeSymlink})
	special := makeTar(t, specialEntries)
	cases := []struct {
		name    string
		archive []byte
		digest  string
		limit   int64
	}{
		{name: "bad index", archive: badIndex, digest: digest, limit: 1 << 20},
		{name: "traversal", archive: traversal, digest: digest, limit: 1 << 20},
		{name: "absolute", archive: absolute, digest: digest, limit: 1 << 20},
		{name: "missing layout", archive: missingLayout, digest: digest, limit: 1 << 20},
		{name: "missing index", archive: missingIndex, digest: digest, limit: 1 << 20},
		{name: "duplicate critical path", archive: duplicate, digest: digest, limit: 1 << 20},
		{name: "special file", archive: special, digest: digest, limit: 1 << 20},
		{name: "wrong digest", archive: valid, digest: wrongDigest, limit: 1 << 20},
		{name: "oversized archive", archive: valid, digest: digest, limit: int64(len(valid) - 1)},
		{name: "invalid digest", archive: valid, digest: "sha256:bad", limit: 1 << 20},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(bytes.NewReader(test.archive), test.digest, test.limit); !errors.Is(err, ErrInvalidArchive) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

type tarEntry struct {
	name     string
	body     []byte
	typeflag byte
}

func validEntries(t *testing.T) ([]tarEntry, string) {
	t.Helper()
	layerBytes := makeTar(t, []tarEntry{{name: "layer-file", body: []byte("layer content")}})
	layerDigest := digestBytes(layerBytes)
	config, err := json.Marshal(map[string]any{
		"architecture": "amd64", "os": "linux",
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{layerDigest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	configDigest := digestBytes(config)
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     testManifestMediaType,
		"config":        map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": configDigest, "size": len(config)},
		"layers":        []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": layerDigest, "size": len(layerBytes)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := digestBytes(manifest)
	indexJSON, err := json.Marshal(index{SchemaVersion: 2, MediaType: "application/vnd.oci.image.index.v1+json", Manifests: []descriptor{{MediaType: testManifestMediaType, Digest: manifestDigest, Size: int64(len(manifest))}}})
	if err != nil {
		t.Fatal(err)
	}
	return []tarEntry{
		{name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{name: "index.json", body: indexJSON},
		{name: "blobs/sha256/" + hexDigest(configDigest), body: config},
		{name: "blobs/sha256/" + hexDigest(layerDigest), body: layerBytes},
		{name: "blobs/sha256/" + hexDigest(manifestDigest), body: manifest},
	}, manifestDigest
}

func makeTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		kind := entry.typeflag
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0o600, Typeflag: kind, Size: int64(len(entry.body))}
		if kind == tar.TypeSymlink {
			header.Linkname = "../../escape"
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(entry.body) > 0 {
			if _, err := writer.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func cloneEntries(entries []tarEntry) []tarEntry {
	cloned := make([]tarEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hexDigest(value string) string { return value[len("sha256:"):] }
