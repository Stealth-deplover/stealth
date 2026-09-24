// Package buildkitmetadata validates the small public metadata contract used
// by the pinned buildctl client. It never derives image identity from logs.
package buildkitmetadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

const MaxMetadataBytes = 64 << 10

var (
	ErrInvalidMetadata = errors.New("invalid BuildKit metadata")
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Metadata struct {
	ImageDigest string
	MediaType   string
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Parse reads one bounded JSON object and requires the OCI export digest.
// Current BuildKit metadata files include containerimage.descriptor as a JSON
// object; older compatible metadata can omit it.
func Parse(reader io.Reader) (Metadata, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxMetadataBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxMetadataBytes {
		return Metadata{}, ErrInvalidMetadata
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') || rejectDuplicateJSONKeys(decoder) != nil {
		return Metadata{}, ErrInvalidMetadata
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Metadata{}, ErrInvalidMetadata
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var values map[string]json.RawMessage
	if err := decoder.Decode(&values); err != nil || values == nil {
		return Metadata{}, ErrInvalidMetadata
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Metadata{}, ErrInvalidMetadata
	}
	var result Metadata
	rawDigest, ok := values["containerimage.digest"]
	if !ok || json.Unmarshal(rawDigest, &result.ImageDigest) != nil || !digestPattern.MatchString(result.ImageDigest) {
		return Metadata{}, ErrInvalidMetadata
	}
	if rawDescriptor, ok := values["containerimage.descriptor"]; ok {
		if len(rawDescriptor) > 32<<10 || rejectDuplicateJSONKeysBytes(rawDescriptor) != nil {
			return Metadata{}, ErrInvalidMetadata
		}
		var item descriptor
		if err := json.Unmarshal(rawDescriptor, &item); err != nil || item.Digest != result.ImageDigest || item.Size <= 0 {
			return Metadata{}, ErrInvalidMetadata
		}
		if item.MediaType != "application/vnd.oci.image.manifest.v1+json" && item.MediaType != "application/vnd.oci.image.index.v1+json" && item.MediaType != "application/vnd.docker.distribution.manifest.v2+json" && item.MediaType != "application/vnd.docker.distribution.manifest.list.v2+json" {
			return Metadata{}, fmt.Errorf("%w: unsupported descriptor media type", ErrInvalidMetadata)
		}
		result.MediaType = item.MediaType
	}
	return result, nil
}

func ValidDigest(value string) bool { return digestPattern.MatchString(value) }

func rejectDuplicateJSONKeys(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return ErrInvalidMetadata
		}
		key, ok := keyToken.(string)
		if !ok || key == "" {
			return ErrInvalidMetadata
		}
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidMetadata
		}
		seen[key] = struct{}{}
		if err := consumeUniqueValue(decoder); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return ErrInvalidMetadata
	}
	return nil
}

func rejectDuplicateJSONKeysBytes(value []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') || rejectDuplicateJSONKeys(decoder) != nil {
		return ErrInvalidMetadata
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidMetadata
	}
	return nil
}

func consumeUniqueValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalidMetadata
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return ErrInvalidMetadata
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidMetadata
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidMetadata
			}
			seen[key] = struct{}{}
			if err := consumeUniqueValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalidMetadata
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalidMetadata
		}
	default:
		return ErrInvalidMetadata
	}
	return nil
}
