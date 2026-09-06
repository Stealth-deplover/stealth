package sitestore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func testUUID(t *testing.T) uuid.UUID {
	t.Helper()
	return uuid.Must(uuid.NewV7())
}

func TestStorePublishesAndServesImmutableDirectory(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectID, siteID, deploymentID := testUUID(t), testUUID(t), testUUID(t)
	staging, relative, err := store.BeginStaging(projectID, siteID, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "index.html"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(staging, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "assets", "app.js"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEntrypoint(staging, "index.html"); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitDirectory(staging, relative); err != nil {
		t.Fatal(err)
	}
	file, _, err := store.OpenFile(relative, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ok" {
		t.Fatalf("served file = %q, want ok", data)
	}
	if err := store.CommitDirectory(staging, relative); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second commit error = %v, want destination/path failure", err)
	}
}

func TestStoreRejectsTraversalAndSymlinks(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectID, siteID, deploymentID := testUUID(t), testUUID(t), testUUID(t)
	staging, relative, err := store.BeginStaging(projectID, siteID, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "index.html"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("index.html", filepath.Join(staging, "link.html")); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitDirectory(staging, relative); err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"../index.html", "assets/../../index.html", "\\etc\\passwd", "./index.html"} {
		if _, _, err := store.OpenFile(relative, request); !errors.Is(err, ErrInvalidFile) {
			t.Errorf("OpenFile(%q) error = %v, want ErrInvalidFile", request, err)
		}
	}
	if _, _, err := store.OpenFile(relative, "link.html"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("symlink OpenFile error = %v, want ErrInvalidPath", err)
	}
	if _, _, err := store.OpenFile(relative, "missing.html"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing OpenFile error = %v, want os.ErrNotExist", err)
	}
	if _, err := ArtifactRelativePath(uuid.Nil, siteID, deploymentID); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("nil artifact path error = %v", err)
	}
}

func TestStoreRejectsSymlinkedArtifactParents(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectID, siteID, deploymentID := testUUID(t), testUUID(t), testUUID(t)
	staging, relative, err := store.BeginStaging(projectID, siteID, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "index.html"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitDirectory(staging, relative); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(store.Root(), projectID.String())
	outside := t.TempDir()
	if err := os.Rename(projectPath, projectPath+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, projectPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.OpenFile(relative, "index.html"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("OpenFile through symlinked project parent error = %v, want ErrInvalidPath", err)
	}
}

func TestValidateEntrypointRequiresRootIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEntrypoint(root, "assets/index.html"); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("nested entrypoint error = %v", err)
	}
	if err := ValidateEntrypoint(root, "index.html"); err != nil {
		t.Fatal(err)
	}
}
