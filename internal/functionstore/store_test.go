package functionstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestStoreUsesOpaqueUUIDPathsAndStreamsBytes(t *testing.T) {
	store, err := New(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	projectID := uuid.Must(uuid.NewV7())
	functionID := uuid.Must(uuid.NewV7())
	deploymentID := uuid.Must(uuid.NewV7())
	payload := []byte("opaque source bytes\x00\xff")

	artifact, err := store.BeginUpload(context.Background(), projectID, functionID, deploymentID, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.RelativePath != filepath.ToSlash(filepath.Join(projectID.String(), functionID.String(), deploymentID.String())) {
		t.Fatalf("relative path = %q", artifact.RelativePath)
	}
	if strings.Contains(artifact.RelativePath, "source") || strings.Contains(artifact.RelativePath, "zip") {
		t.Fatalf("user-facing source metadata influenced path: %q", artifact.RelativePath)
	}
	if artifact.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", artifact.Size, len(payload))
	}
	if err := store.Commit(&artifact); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifact.TempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary upload still exists: %v", err)
	}
	file, err := store.OpenRelative(artifact.RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("stored bytes = %q, want %q", got, payload)
	}
	if err := store.RemoveRelative("../outside"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal remove error = %v, want ErrInvalidPath", err)
	}
}

func TestStoreEnforcesUploadLimitAndCleansTemporaryFile(t *testing.T) {
	store, err := New(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.BeginUpload(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), strings.NewReader("12345"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
	if artifact.TempPath != "" {
		t.Fatalf("failed upload returned temp path %q", artifact.TempPath)
	}
	var files int
	if err := filepath.WalkDir(store.Root(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			files++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if files != 0 {
		t.Fatalf("failed upload left files: %d", files)
	}
}

func TestStoreConcurrentUploads(t *testing.T) {
	store, err := New(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	projectID := uuid.Must(uuid.NewV7())
	const count = 16
	errCh := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		functionID := uuid.Must(uuid.NewV7())
		deploymentID := uuid.Must(uuid.NewV7())
		payload := []byte(strings.Repeat("x", i+1))
		wg.Add(1)
		go func() {
			defer wg.Done()
			artifact, beginErr := store.BeginUpload(context.Background(), projectID, functionID, deploymentID, bytes.NewReader(payload))
			if beginErr != nil {
				errCh <- beginErr
				return
			}
			if err := store.Commit(&artifact); err != nil {
				errCh <- err
				return
			}
			file, err := store.OpenRelative(artifact.RelativePath)
			if err != nil {
				errCh <- err
				return
			}
			got, readErr := io.ReadAll(file)
			_ = file.Close()
			if readErr != nil {
				errCh <- readErr
				return
			}
			if !bytes.Equal(got, payload) {
				errCh <- errors.New("concurrent upload bytes changed")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
