package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateFilename(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"report.pdf", true},
		{"résumé 2026.txt", true},
		{"", false},
		{".", false},
		{"..", false},
		{" leading.txt", false},
		{"trailing.txt ", false},
		{"nested/file.txt", false},
		{"nested\\file.txt", false},
		{"header\r\ninjection.txt", false},
		{"control\x7f.txt", false},
		{strings.Repeat("a", maxFilenameBytes+1), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateFilename(test.name)
			if (err == nil) != test.valid {
				t.Fatalf("ValidateFilename(%q) error = %v, valid = %v", test.name, err, test.valid)
			}
		})
	}
}

func TestNormalizeContentType(t *testing.T) {
	got, err := NormalizeContentType("Text/Plain; charset=utf-8")
	if err != nil || got != "text/plain" {
		t.Fatalf("NormalizeContentType() = %q, %v; want text/plain", got, err)
	}
	if got, err := NormalizeContentType(""); err != nil || got != "" {
		t.Fatalf("empty MIME = %q, %v; want empty", got, err)
	}
	for _, value := range []string{"not a mime", "text/plain\r\nX-Injected: yes"} {
		if _, err := NormalizeContentType(value); !errors.Is(err, ErrInvalidMIME) {
			t.Errorf("NormalizeContentType(%q) error = %v, want ErrInvalidMIME", value, err)
		}
	}
}

func TestBeginUploadCommitAndOpenUsesUUIDPathAndChecksum(t *testing.T) {
	root := t.TempDir()
	store, err := New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	projectID := uuid.Must(uuid.NewV7())
	bucketID := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	content := []byte("hello storage")
	prepared, err := store.BeginUpload(context.Background(), projectID, bucketID, fileID, strings.NewReader(string(content)), "text/plain; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(content)
	if prepared.Size != int64(len(content)) || prepared.Checksum != hex.EncodeToString(wantHash[:]) || prepared.ContentType != "text/plain" {
		t.Fatalf("prepared metadata = size %d checksum %q MIME %q", prepared.Size, prepared.Checksum, prepared.ContentType)
	}
	wantRelative := filepath.ToSlash(filepath.Join(projectID.String(), bucketID.String(), fileID.String()))
	if prepared.RelativePath != wantRelative {
		t.Fatalf("relative path = %q, want %q", prepared.RelativePath, wantRelative)
	}
	if strings.Contains(prepared.RelativePath, "hello") || strings.Contains(prepared.RelativePath, "..") {
		t.Fatalf("relative path contains user content: %q", prepared.RelativePath)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(prepared.RelativePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination before commit stat error = %v, want not exist", err)
	}
	if err := store.Commit(&prepared); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(&prepared); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("second Commit error = %v, want ErrInvalidPath", err)
	}
	file, err := store.OpenRelative(prepared.RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(file)
	closeErr := file.Close()
	if err != nil || closeErr != nil || string(got) != string(content) {
		t.Fatalf("opened content = %q, read error = %v, close error = %v", got, err, closeErr)
	}
	if err := store.RemoveRelative(prepared.RelativePath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenRelative(prepared.RelativePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open after removal error = %v, want not exist", err)
	}
}

func TestBeginUploadEnforcesLimitAndCleansTemporaryFile(t *testing.T) {
	store, err := New(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.BeginUpload(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), strings.NewReader("12345"), "text/plain")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("BeginUpload error = %v, want ErrTooLarge", err)
	}
	if prepared.TempPath != "" {
		t.Fatalf("failed upload returned temporary path %q", prepared.TempPath)
	}
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("root entry count = %d, want one UUID directory", len(entries))
	}
	if _, err := store.BeginUpload(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), strings.NewReader("1234"), ""); err != nil {
		t.Fatal(err)
	}
}

func TestBeginUploadHonorsCanceledContextAndRejectsTraversal(t *testing.T) {
	store, err := New(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.BeginUpload(ctx, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), strings.NewReader("data"), "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled BeginUpload error = %v, want context.Canceled", err)
	}
	if _, err := store.OpenRelative("../outside"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal OpenRelative error = %v, want ErrInvalidPath", err)
	}
	if err := store.RemoveRelative(filepath.Join(store.Root(), "outside")); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("absolute RemoveRelative error = %v, want ErrInvalidPath", err)
	}
}

func TestNewS3ValidatesConfigurationAndUsesBoundedStaging(t *testing.T) {
	store, err := NewS3(S3Options{
		Endpoint:       "http://minio.example.test:9000",
		Region:         "us-east-1",
		Bucket:         "stealth-files",
		AccessKey:      "access",
		SecretKey:      "secret",
		ForcePathStyle: true,
		Prefix:         "tenant-blobs",
		StagingRoot:    t.TempDir(),
	}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.objectKey("project/bucket/file")
	if err != nil || key != "tenant-blobs/project/bucket/file" {
		t.Fatalf("objectKey() = %q, %v", key, err)
	}
	if _, err := store.objectKey("../outside"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("objectKey traversal error = %v, want ErrInvalidPath", err)
	}
	for _, invalid := range []S3Options{
		{Endpoint: "http://minio.example.test:9000/path", Bucket: "stealth-files", AccessKey: "a", SecretKey: "s", StagingRoot: t.TempDir()},
		{Endpoint: "http://minio.example.test:9000", Bucket: "bad_bucket", AccessKey: "a", SecretKey: "s", StagingRoot: t.TempDir()},
	} {
		if _, err := NewS3(invalid, 1024); err == nil {
			t.Fatalf("NewS3(%+v) succeeded for invalid configuration", invalid)
		}
	}
}
