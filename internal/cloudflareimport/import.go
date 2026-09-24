// Package cloudflareimport prepares and reads the narrow encrypted artifact
// used to migrate the legacy Cloudflare setup connection into PostgreSQL.
package cloudflareimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Stealth-deplover/stealth/internal/domainname"
	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

const (
	// Version identifies the serialized Cloudflare import envelope contract.
	Version               = 1
	maxSetupSnapshotBytes = 8 << 20
	maxArtifactBytes      = 16 << 10
	workerGroupID         = 10001
)

const (
	StateConnection        = "connection"
	StateReconnectRequired = "reconnect_required"
)

const (
	legacySetupSnapshotName = "setup-state.enc"
	legacySetupTempName     = ".setup-state.enc.tmp"
)

const (
	OutcomeNoImport          = "no_import"
	OutcomeConnection        = "connection"
	OutcomeReconnectRequired = "reconnect_required"
)

var (
	errInvalidPath       = errors.New("Cloudflare import path is invalid")
	errInvalidSource     = errors.New("encrypted setup snapshot could not be recovered")
	errInvalidArtifact   = errors.New("Cloudflare import artifact is invalid")
	errArtifactDecrypt   = errors.New("Cloudflare import artifact could not be decrypted")
	errArtifactEncrypt   = errors.New("Cloudflare import artifact could not be encrypted")
	errArtifactIO        = errors.New("Cloudflare import artifact could not be published")
	errUnsafeImportEntry = errors.New("Cloudflare import entry is not a regular file")
	// ErrWorkerBoundaryUnsafe means the worker-visible directory contains a
	// legacy full setup snapshot or unexpected content that cannot be safely
	// removed. The preparation service must fail so Compose does not start the
	// worker with a wider decryptable trust boundary.
	ErrWorkerBoundaryUnsafe = errors.New("worker Cloudflare import directory is unsafe")
)

// Envelope is the complete plaintext contract visible to the production
// worker. Keep this type limited to the durable Cloudflare connection fields.
// ReconnectRequired carries no credential or partial provider identity.
type Envelope struct {
	Version         int    `json:"version"`
	State           string `json:"state"`
	AccountID       string `json:"account_id,omitempty"`
	ConsoleZoneID   string `json:"console_zone_id,omitempty"`
	ConsoleHostname string `json:"console_hostname,omitempty"`
	TunnelID        string `json:"tunnel_id,omitempty"`
	TunnelName      string `json:"tunnel_name,omitempty"`
	ConsoleRecordID string `json:"console_record_id,omitempty"`
	APIToken        string `json:"api_token,omitempty"`
}

// FileOwner is used by the root-only Compose preparation container to make
// the encrypted artifact readable by the fixed non-root worker identity.
type FileOwner struct {
	UID int
	GID int
}

// Prepare reads the complete encrypted onboarding snapshot in the trusted
// one-shot preparation process, derives only Cloudflare connection fields,
// and atomically publishes an encrypted narrow artifact. A missing source or
// a valid non-Cloudflare setup removes any stale derived artifact.
func Prepare(ctx context.Context, sourcePath, destinationPath string, cipher *functionsecret.Cipher, owner *FileOwner) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !validFilePath(sourcePath) || !validFilePath(destinationPath) || sourcePath == destinationPath || cipher == nil {
		return "", errInvalidPath
	}
	importDirectory := filepath.Dir(destinationPath)
	relativeSource, err := filepath.Rel(importDirectory, sourcePath)
	if err != nil || relativeSource == "." || (relativeSource != ".." && !strings.HasPrefix(relativeSource, ".."+string(filepath.Separator))) {
		return "", errInvalidPath
	}
	if err := cleanWorkerImportDirectory(importDirectory, filepath.Base(destinationPath)); err != nil {
		return "", err
	}
	sourceInfo, err := os.Lstat(sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		if removeErr := removeArtifact(destinationPath); removeErr != nil {
			return "", removeErr
		}
		return OutcomeNoImport, nil
	}
	if err != nil || sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() || sourceInfo.Size() <= 0 || sourceInfo.Size() > maxSetupSnapshotBytes {
		return "", clearArtifactAfterFailure(destinationPath, errInvalidSource)
	}
	state, err := setupstate.LoadEncryptedSnapshot(ctx, sourcePath, cipher)
	if err != nil {
		return "", clearArtifactAfterFailure(destinationPath, errInvalidSource)
	}

	envelope, outcome := envelopeFromSetupState(state)
	if outcome == OutcomeNoImport {
		if err := removeArtifact(destinationPath); err != nil {
			return "", err
		}
		return outcome, nil
	}
	ciphertext, err := Encrypt(envelope, cipher)
	if err != nil {
		return "", clearArtifactAfterFailure(destinationPath, err)
	}
	if err := writeAtomic(destinationPath, ciphertext, owner); err != nil {
		return "", clearArtifactAfterFailure(destinationPath, err)
	}
	return outcome, nil
}

// PublishLegacySetupSnapshot copies only the bounded encrypted legacy setup
// snapshot into the narrow Compose handoff directory. It never follows the
// source symlink, accepts no other file type, and leaves the input directory
// empty when the optional source is absent.
func PublishLegacySetupSnapshot(ctx context.Context, sourcePath, inputDirectory string, owners ...*FileOwner) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(owners) > 1 {
		return false, errInvalidPath
	}
	var owner *FileOwner
	if len(owners) == 1 {
		owner = owners[0]
	}
	if !validFilePath(sourcePath) || filepath.Base(sourcePath) != legacySetupSnapshotName || !validFilePath(inputDirectory) ||
		filepath.Clean(filepath.Dir(sourcePath)) == filepath.Clean(inputDirectory) {
		return false, errInvalidPath
	}
	sourceDirectory := filepath.Dir(sourcePath)
	sourceDirInfo, err := os.Lstat(sourceDirectory)
	if err != nil || sourceDirInfo.Mode()&os.ModeSymlink != 0 || !sourceDirInfo.IsDir() {
		return false, errInvalidSource
	}
	if err := os.MkdirAll(inputDirectory, 0o700); err != nil {
		return false, errArtifactIO
	}
	inputInfo, err := os.Lstat(inputDirectory)
	if err != nil || inputInfo.Mode()&os.ModeSymlink != 0 || !inputInfo.IsDir() {
		return false, errArtifactIO
	}
	if owner != nil {
		if owner.UID < 0 || owner.GID < 0 || os.Geteuid() != 0 {
			return false, errArtifactIO
		}
		// The named volume may retain the host state directory's ownership
		// from an earlier run. Temporarily return it to this root one-shot so
		// stale contents can be removed without broad DAC override authority.
		if err := os.Chown(inputDirectory, 0, 0); err != nil {
			return false, errArtifactIO
		}
	}
	if err := os.Chmod(inputDirectory, 0o700); err != nil {
		return false, errArtifactIO
	}
	if err := cleanLegacySetupInputDirectory(inputDirectory); err != nil {
		return false, err
	}

	fd, err := syscall.Open(sourcePath, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ENOENT) {
		if err := restoreLegacySetupInputOwner(inputDirectory, owner); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, errInvalidSource
	}
	source := os.NewFile(uintptr(fd), sourcePath)
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSetupSnapshotBytes || info.Mode().Perm()&0o007 != 0 {
		return false, errInvalidSource
	}
	contents, err := io.ReadAll(io.LimitReader(source, maxSetupSnapshotBytes+1))
	if err != nil || len(contents) == 0 || len(contents) > maxSetupSnapshotBytes || int64(len(contents)) != info.Size() {
		return false, errInvalidSource
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := writeLegacySetupInput(inputDirectory, contents); err != nil {
		return false, err
	}
	if err := restoreLegacySetupInputOwner(inputDirectory, owner); err != nil {
		return false, err
	}
	return true, nil
}

func restoreLegacySetupInputOwner(directory string, owner *FileOwner) error {
	if owner == nil {
		return nil
	}
	if err := os.Chown(directory, owner.UID, owner.GID); err != nil {
		return errArtifactIO
	}
	if err := os.Chmod(directory, 0o770); err != nil {
		return errArtifactIO
	}
	if err := syncDirectory(directory); err != nil {
		return errArtifactIO
	}
	return nil
}

func cleanLegacySetupInputDirectory(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return errArtifactIO
	}
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		if name != legacySetupSnapshotName && name != legacySetupTempName && !strings.HasPrefix(name, ".setup-state.enc.tmp-") {
			return errInvalidSource
		}
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errInvalidSource
		}
		if err := os.Remove(path); err != nil {
			return errArtifactIO
		}
		removed = true
	}
	if removed {
		if err := syncDirectory(directory); err != nil {
			return errArtifactIO
		}
	}
	return nil
}

func writeLegacySetupInput(directory string, contents []byte) error {
	temporary, err := os.CreateTemp(directory, ".setup-state.enc.tmp-")
	if err != nil {
		return errArtifactIO
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return errArtifactIO
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errArtifactIO
	}
	if err := temporary.Chmod(0o400); err != nil {
		_ = temporary.Close()
		return errArtifactIO
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errArtifactIO
	}
	if err := temporary.Close(); err != nil {
		return errArtifactIO
	}
	destination := filepath.Join(directory, legacySetupSnapshotName)
	if err := os.Rename(temporaryPath, destination); err != nil {
		return errArtifactIO
	}
	if err := syncDirectory(directory); err != nil {
		return errArtifactIO
	}
	return nil
}

// Encrypt returns an encrypted, validated artifact for a trusted producer.
func Encrypt(envelope Envelope, cipher *functionsecret.Cipher) ([]byte, error) {
	if err := validateEnvelope(envelope); err != nil || cipher == nil {
		return nil, errInvalidArtifact
	}
	plaintext, err := json.Marshal(envelope)
	if err != nil || len(plaintext) > maxArtifactBytes {
		return nil, errInvalidArtifact
	}
	ciphertext, err := cipher.Encrypt(plaintext)
	if err != nil || len(ciphertext) > maxArtifactBytes {
		return nil, errArtifactEncrypt
	}
	return ciphertext, nil
}

// Read decrypts the worker-visible file and rejects unknown fields, duplicate
// keys, non-canonical JSON, trailing data, and oversized artifacts.
func Read(path string, cipher *functionsecret.Cipher) (Envelope, error) {
	if !validFilePath(path) || cipher == nil {
		return Envelope{}, errInvalidPath
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return Envelope{}, os.ErrNotExist
		}
		return Envelope{}, errInvalidArtifact
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArtifactBytes {
		return Envelope{}, errUnsafeImportEntry
	}
	ciphertext, err := io.ReadAll(io.LimitReader(file, maxArtifactBytes+1))
	if err != nil || len(ciphertext) > maxArtifactBytes {
		return Envelope{}, errInvalidArtifact
	}
	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil {
		return Envelope{}, errArtifactDecrypt
	}
	var envelope Envelope
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, errInvalidArtifact
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Envelope{}, errInvalidArtifact
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(canonical, plaintext) || validateEnvelope(envelope) != nil {
		return Envelope{}, errInvalidArtifact
	}
	return envelope, nil
}

func envelopeFromSetupState(state setupstate.State) (Envelope, string) {
	binding := state.EffectiveCloudflareBinding().Normalized()
	apiToken := strings.TrimSpace(state.Secret("cloudflare_access_token"))
	tunnelToken := strings.TrimSpace(state.Secret("cloudflare_tunnel_token"))
	if tunnelToken == "" {
		tunnelToken = strings.TrimSpace(state.Secret("tunnel_token"))
	}
	hasIntent := state.Cloudflare.Mode != "" || state.Cloudflare.Connected || !state.Cloudflare.Binding.IsZero() || binding.HasIntent() || apiToken != "" || tunnelToken != ""
	if !hasIntent {
		return Envelope{}, OutcomeNoImport
	}
	if binding.Validate() != nil || binding.AccountID == "" || binding.ZoneID == "" || binding.Hostname == "" || binding.TunnelID == "" || binding.TunnelName == "" || binding.RecordID == "" {
		return Envelope{Version: Version, State: StateReconnectRequired}, OutcomeReconnectRequired
	}
	if len(apiToken) > 4096 || strings.ContainsAny(apiToken, "\x00\r\n") {
		apiToken = ""
	}
	return Envelope{
		Version: Version, State: StateConnection,
		AccountID: binding.AccountID, ConsoleZoneID: binding.ZoneID, ConsoleHostname: binding.Hostname,
		TunnelID: binding.TunnelID, TunnelName: binding.TunnelName, ConsoleRecordID: binding.RecordID,
		APIToken: apiToken,
	}, OutcomeConnection
}

func validateEnvelope(envelope Envelope) error {
	if envelope.Version != Version {
		return errInvalidArtifact
	}
	switch envelope.State {
	case StateReconnectRequired:
		if envelope.AccountID != "" || envelope.ConsoleZoneID != "" || envelope.ConsoleHostname != "" || envelope.TunnelID != "" || envelope.TunnelName != "" || envelope.ConsoleRecordID != "" || envelope.APIToken != "" {
			return errInvalidArtifact
		}
		return nil
	case StateConnection:
	default:
		return errInvalidArtifact
	}
	for _, field := range []struct {
		value string
		max   int
	}{
		{envelope.AccountID, 128}, {envelope.ConsoleZoneID, 128}, {envelope.TunnelID, 128},
		{envelope.TunnelName, 120}, {envelope.ConsoleRecordID, 128}, {envelope.ConsoleHostname, 253},
	} {
		if strings.TrimSpace(field.value) != field.value || field.value == "" || len(field.value) > field.max || strings.ContainsAny(field.value, "\x00\r\n") {
			return errInvalidArtifact
		}
	}
	hostname, err := domainname.NormalizeHostname(envelope.ConsoleHostname)
	if err != nil || hostname != envelope.ConsoleHostname {
		return errInvalidArtifact
	}
	if len(envelope.APIToken) > 4096 || strings.TrimSpace(envelope.APIToken) != envelope.APIToken || strings.ContainsAny(envelope.APIToken, "\x00\r\n") {
		return errInvalidArtifact
	}
	return nil
}

func validFilePath(path string) bool {
	return strings.TrimSpace(path) != "" && filepath.IsAbs(path) && filepath.Clean(path) != string(filepath.Separator)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func clearArtifactAfterFailure(path string, cause error) error {
	if err := removeArtifact(path); err != nil {
		return err
	}
	return cause
}

func writeAtomic(path string, ciphertext []byte, owner *FileOwner) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return errArtifactIO
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return errArtifactIO
	}
	fileMode := os.FileMode(0o600)
	directoryMode := os.FileMode(0o700)
	if owner != nil {
		if owner.UID < 0 || owner.GID < 0 {
			return errArtifactIO
		}
		if err := os.Chown(directory, owner.UID, owner.GID); err != nil {
			return errArtifactIO
		}
		fileMode = 0o640
		directoryMode = 0o770
	}
	if err := os.Chmod(directory, directoryMode); err != nil {
		return errArtifactIO
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errUnsafeImportEntry
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errArtifactIO
	}
	temporary, err := os.CreateTemp(directory, ".cloudflare-import-*.tmp")
	if err != nil {
		return errArtifactIO
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(ciphertext); err != nil {
		_ = temporary.Close()
		return errArtifactIO
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errArtifactIO
	}
	if owner != nil {
		if err := temporary.Chown(owner.UID, owner.GID); err != nil {
			_ = temporary.Close()
			return errArtifactIO
		}
	}
	if err := temporary.Chmod(fileMode); err != nil {
		_ = temporary.Close()
		return errArtifactIO
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errArtifactIO
	}
	if err := temporary.Close(); err != nil {
		return errArtifactIO
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errArtifactIO
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return errArtifactIO
	}
	defer directoryFile.Close()
	if err := directoryFile.Sync(); err != nil {
		return errArtifactIO
	}
	return nil
}

func removeArtifact(path string) error {
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return errArtifactIO
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errArtifactIO
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errUnsafeImportEntry
	}
	if err := os.Remove(path); err != nil {
		return errArtifactIO
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return errArtifactIO
	}
	defer directoryFile.Close()
	if err := directoryFile.Sync(); err != nil {
		return errArtifactIO
	}
	return nil
}

// cleanWorkerImportDirectory removes the two filenames used by the previous
// initializer to copy the complete setup snapshot. It also removes abandoned
// narrow-artifact temp files and fails closed if any unrecognized entry would
// remain visible in the worker's directory mount.
func cleanWorkerImportDirectory(directory, artifactName string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrWorkerBoundaryUnsafe
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return ErrWorkerBoundaryUnsafe
	}
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		if name == artifactName {
			entryInfo, statErr := os.Lstat(filepath.Join(directory, name))
			if statErr != nil || entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
				return ErrWorkerBoundaryUnsafe
			}
			continue
		}
		if name == legacySetupSnapshotName || name == legacySetupTempName || (strings.HasPrefix(name, ".cloudflare-import-") && strings.HasSuffix(name, ".tmp")) {
			entryInfo, statErr := os.Lstat(filepath.Join(directory, name))
			if statErr != nil || entryInfo.IsDir() {
				return ErrWorkerBoundaryUnsafe
			}
			// Remove the exact entry without following a possible symlink. These
			// names are known initializer outputs, never source setup state.
			if err := os.Remove(filepath.Join(directory, name)); err != nil {
				return ErrWorkerBoundaryUnsafe
			}
			removed = true
			continue
		}
		return ErrWorkerBoundaryUnsafe
	}
	if removed {
		directoryFile, err := os.Open(directory)
		if err != nil {
			return ErrWorkerBoundaryUnsafe
		}
		defer directoryFile.Close()
		if err := directoryFile.Sync(); err != nil {
			return ErrWorkerBoundaryUnsafe
		}
	}
	return nil
}

// OwnerForSourceDirectory returns the host UID paired with the worker's fixed
// group so the non-root worker can read the encrypted import artifact.
func OwnerForSourceDirectory(path string) (*FileOwner, error) {
	if !validFilePath(path) {
		return nil, errInvalidPath
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errInvalidPath
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errInvalidPath
	}
	return &FileOwner{UID: int(stat.Uid), GID: workerGroupID}, nil
}
