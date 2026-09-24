package appstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNamespacesPublishOpenAndRemoveUUIDArtifacts(t *testing.T) {
	ctx := context.Background()
	store, err := New(t.TempDir(), 1024, 2048)
	if err != nil {
		t.Fatal(err)
	}
	projectID := uuid.Must(uuid.NewV7())
	appID := uuid.Must(uuid.NewV7())
	deploymentID := uuid.Must(uuid.NewV7())
	content := "source bytes"
	prepared, err := store.Sources.BeginUpload(ctx, projectID, appID, deploymentID, strings.NewReader(content), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Sources.Cleanup(&prepared)
	if strings.Contains(prepared.RelativePath, "source.zip") || prepared.RelativePath != projectID.String()+"/"+appID.String()+"/"+deploymentID.String() {
		t.Fatalf("source path is not UUID-derived: %q", prepared.RelativePath)
	}
	want := sha256.Sum256([]byte(content))
	if prepared.Checksum != hex.EncodeToString(want[:]) {
		t.Fatalf("source checksum = %s", prepared.Checksum)
	}
	if err := store.Sources.Commit(ctx, &prepared); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Sources.OpenRelative(ctx, prepared.RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(opened)
	_ = opened.Close()
	if err != nil || string(read) != content {
		t.Fatalf("OpenRelative() = %q, %v", read, err)
	}

	image, err := store.Images.BeginUpload(ctx, projectID, appID, deploymentID, strings.NewReader("oci bytes"), 2048)
	if err != nil {
		t.Fatal(err)
	}
	if image.RelativePath != prepared.RelativePath {
		t.Fatalf("parallel namespaces should use stable ID locators: %q / %q", prepared.RelativePath, image.RelativePath)
	}
	if err := store.Images.Commit(ctx, &image); err != nil {
		t.Fatal(err)
	}
	if store.Sources.Root() == store.Images.Root() {
		t.Fatal("source and image namespaces share a root")
	}
	if err := store.Sources.RemoveProject(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	if err := store.Images.RemoveProject(ctx, projectID); err != nil {
		t.Fatal(err)
	}
}

func TestUploadEnforcesNamespaceLimitAndCleanup(t *testing.T) {
	store, err := New(t.TempDir(), 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.Sources.BeginUpload(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), strings.NewReader("four"), 3)
	if err == nil || prepared.TempPath != "" {
		t.Fatalf("oversized upload = %#v, %v", prepared, err)
	}
}

func TestCleanupStaleUploadsRemovesOnlyOldPrivateStagingFiles(t *testing.T) {
	ctx := context.Background()
	store, err := New(t.TempDir(), 1024, 2048)
	if err != nil {
		t.Fatal(err)
	}
	projectID := uuid.Must(uuid.NewV7())
	appID := uuid.Must(uuid.NewV7())
	old, err := store.Sources.BeginUpload(ctx, projectID, appID, uuid.Must(uuid.NewV7()), strings.NewReader("old staging"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Sources.Cleanup(&old)
	recent, err := store.Sources.BeginUpload(ctx, projectID, appID, uuid.Must(uuid.NewV7()), strings.NewReader("recent staging"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Sources.Cleanup(&recent)
	now := time.Now()
	if err := os.Chtimes(old.TempPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	committed, err := store.Sources.BeginUpload(ctx, projectID, appID, uuid.Must(uuid.NewV7()), strings.NewReader("published"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sources.Commit(ctx, &committed); err != nil {
		t.Fatal(err)
	}
	keepPath := filepath.Join(store.Sources.Root(), filepath.FromSlash(committed.RelativePath))
	removed, err := store.Sources.CleanupStaleUploads(ctx, time.Hour)
	if err != nil || removed != 1 {
		t.Fatalf("CleanupStaleUploads() = %d, %v", removed, err)
	}
	if _, err := os.Stat(old.TempPath); !os.IsNotExist(err) {
		t.Fatalf("stale upload remained: %v", err)
	}
	for _, path := range []string{recent.TempPath, keepPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cleanup removed a recent or committed artifact %s: %v", path, err)
		}
	}
}
