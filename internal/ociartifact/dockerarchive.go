package ociartifact

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type dockerManifestItem struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

// WriteDockerArchive copies a previously verified OCI layout into the Docker
// load tar format. Docker's load API requires a top-level manifest.json, which
// a standard OCI layout does not include. The extra file exists only in the
// streamed copy; the durable artifact remains an OCI layout. The imported image
// has no tag until its config and layers have been checked by the runtime.
func WriteDockerArchive(reader io.ReadSeeker, info ImageInfo, output io.Writer) error {
	if reader == nil || output == nil || !validDigest(info.ManifestDigest) || !validDigest(info.ConfigDigest) ||
		info.ArchiveSize <= 0 || len(info.LayerDigests) != len(info.LayerDiffIDs) {
		return ErrInvalidArchive
	}
	for _, digest := range info.LayerDigests {
		if !validDigest(digest) {
			return ErrInvalidArchive
		}
	}
	for _, digest := range info.LayerDiffIDs {
		if !validDigest(digest) {
			return ErrInvalidArchive
		}
	}
	archiveSize, err := reader.Seek(0, io.SeekEnd)
	if err != nil || archiveSize != info.ArchiveSize {
		return ErrInvalidArchive
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return ErrInvalidArchive
	}

	input := tar.NewReader(reader)
	outputTar := tar.NewWriter(output)
	seen := make(map[string]struct{})
	blobs := make(map[string]int64)
	var total int64
	fileCount := 0
	layoutSeen, indexSeen := false, false
	for {
		header, err := input.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: read OCI archive", ErrInvalidArchive)
		}
		name, err := safeName(header.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("%w: duplicate archive path", ErrInvalidArchive)
		}
		seen[name] = struct{}{}
		if header.Typeflag == tar.TypeDir {
			if name != "." && name != "blobs" && name != "blobs/sha256" {
				return fmt.Errorf("%w: unexpected directory", ErrInvalidArchive)
			}
			if err := outputTar.WriteHeader(&tar.Header{
				Name: name + "/", Mode: 0o700, Typeflag: tar.TypeDir, Format: tar.FormatUSTAR,
				ModTime: time.Unix(0, 0),
			}); err != nil {
				return err
			}
			continue
		}
		if (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) || header.Size <= 0 {
			return fmt.Errorf("%w: links and special files are not allowed", ErrInvalidArchive)
		}
		fileCount++
		if fileCount > maxEntries || header.Size > info.ArchiveSize-total {
			return fmt.Errorf("%w: archive exceeds configured bounds", ErrInvalidArchive)
		}
		total += header.Size
		if name == "oci-layout" {
			layoutSeen = true
		} else if name == "index.json" {
			indexSeen = true
		} else if strings.HasPrefix(name, "blobs/sha256/") {
			digest := strings.TrimPrefix(name, "blobs/sha256/")
			if !validHexDigest(digest) {
				return ErrInvalidArchive
			}
			if err := outputTar.WriteHeader(&tar.Header{
				Name: name, Mode: 0o600, Size: header.Size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
				ModTime: time.Unix(0, 0),
			}); err != nil {
				return err
			}
			hasher := sha256.New()
			written, copyErr := io.CopyN(io.MultiWriter(outputTar, hasher), input, header.Size)
			actualDigest := hex.EncodeToString(hasher.Sum(nil))
			if copyErr != nil || written != header.Size || actualDigest != digest {
				return fmt.Errorf("%w: blob checksum mismatch for %s (got %s)", ErrInvalidArchive, digest, actualDigest)
			}
			blobs[digest] = header.Size
			continue
		} else {
			return fmt.Errorf("%w: unexpected OCI layout path", ErrInvalidArchive)
		}
		if name == "oci-layout" && header.Size > maxLayoutBytes || name == "index.json" && header.Size > maxIndexBytes {
			return fmt.Errorf("%w: OCI metadata exceeds configured bounds", ErrInvalidArchive)
		}
		if err := outputTar.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: header.Size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
			ModTime: time.Unix(0, 0),
		}); err != nil {
			return err
		}
		if written, err := io.CopyN(outputTar, input, header.Size); err != nil || written != header.Size {
			if err != nil {
				return err
			}
			return io.ErrUnexpectedEOF
		}
	}
	if !layoutSeen || !indexSeen || total > info.ArchiveSize ||
		blobs[strings.TrimPrefix(info.ManifestDigest, "sha256:")] <= 0 ||
		blobs[strings.TrimPrefix(info.ConfigDigest, "sha256:")] <= 0 {
		return fmt.Errorf("%w: OCI layout is incomplete", ErrInvalidArchive)
	}
	for _, digest := range info.LayerDigests {
		if blobs[strings.TrimPrefix(digest, "sha256:")] <= 0 {
			return fmt.Errorf("%w: image layer is missing", ErrInvalidArchive)
		}
	}

	layers := make([]string, len(info.LayerDigests))
	for index, digest := range info.LayerDigests {
		layers[index] = blobPath(digest)
	}
	manifest, err := json.Marshal([]dockerManifestItem{{
		Config: blobPath(info.ConfigDigest), RepoTags: []string{}, Layers: layers,
	}})
	if err != nil {
		return ErrInvalidArchive
	}
	if err := outputTar.WriteHeader(&tar.Header{
		Name: "manifest.json", Mode: 0o600, Size: int64(len(manifest)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
		ModTime: time.Unix(0, 0),
	}); err != nil {
		return err
	}
	if _, err := outputTar.Write(manifest); err != nil {
		return err
	}
	return outputTar.Close()
}

func blobPath(digest string) string {
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}
