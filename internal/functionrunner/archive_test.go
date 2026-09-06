package functionrunner

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZipRejectsTraversalAndSymlink(t *testing.T) {
	tests := []struct {
		name string
		make func(*zip.Writer) error
	}{
		{name: "dot segment", make: func(writer *zip.Writer) error {
			h, err := writer.Create("../escape.js")
			if err != nil {
				return err
			}
			_, err = io.WriteString(h, "bad")
			return err
		}},
		{name: "absolute", make: func(writer *zip.Writer) error {
			h, err := writer.Create("/escape.js")
			if err != nil {
				return err
			}
			_, err = io.WriteString(h, "bad")
			return err
		}},
		{name: "symlink", make: func(writer *zip.Writer) error {
			header := &zip.FileHeader{Name: "link", Method: zip.Store}
			header.SetMode(os.ModeSymlink | 0o777)
			part, err := writer.CreateHeader(header)
			if err != nil {
				return err
			}
			_, err = io.WriteString(part, "target")
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			if err := test.make(writer); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			destination := t.TempDir()
			if _, err := Extract(context.Background(), &archive, "source.zip", destination, ArchiveLimits{}); !errors.Is(err, ErrArchiveTraversal) && !errors.Is(err, ErrArchiveEntry) {
				t.Fatalf("Extract() error = %v, want an archive safety error", err)
			}
			entries, err := os.ReadDir(destination)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("unsafe archive wrote %d entries", len(entries))
			}
		})
	}
}

func TestExtractTarGzipBoundsExpandedBytes(t *testing.T) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "main.js", Mode: 0o644, Size: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Extract(context.Background(), &compressed, "source.tgz", t.TempDir(), ArchiveLimits{MaxBytes: 7, MaxFiles: 2, MaxEntry: 8})
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("Extract() error = %v, want ErrArchiveTooLarge", err)
	}
}

func TestExtractTarGzipAcceptsExplicitRootDirectory(t *testing.T) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "./", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
	content := []byte("<html>ok</html>")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "./index.html", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	stats, err := Extract(context.Background(), &compressed, "site.tgz", destination, ArchiveLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 1 || stats.Bytes != int64(len(content)) {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "index.html")); err != nil || string(got) != string(content) {
		t.Fatalf("index.html = %q, err = %v", got, err)
	}
}

func TestExtractGitArchiveStripsProviderRoot(t *testing.T) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	entries := []struct {
		name string
		mode int64
		data string
		dir  bool
	}{
		{name: "landing-main/", mode: 0o755, dir: true},
		{name: "landing-main/package.json", mode: 0o644, data: `{"scripts":{"build":"echo ok"}}`},
		{name: "landing-main/dist/", mode: 0o755, dir: true},
		{name: "landing-main/dist/index.html", mode: 0o644, data: "ok"},
	}
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode}
		if entry.dir {
			header.Typeflag = tar.TypeDir
		} else {
			header.Size = int64(len(entry.data))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if !entry.dir {
			if _, err := io.WriteString(tarWriter, entry.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	stats, err := Extract(context.Background(), &compressed, "git-github-landing-main.tar.gz", destination, ArchiveLimits{StripTopLevel: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 2 {
		t.Fatalf("files = %d, want 2", stats.Files)
	}
	if _, err := os.Stat(filepath.Join(destination, "package.json")); err != nil {
		t.Fatalf("package.json was not stripped to repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "dist", "index.html")); err != nil {
		t.Fatalf("dist/index.html was not stripped to repository root: %v", err)
	}
}

func TestExtractGitArchiveRejectsMultipleRoots(t *testing.T) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"repo-main/", "repo-main/index.html", "other/"} {
		header := &tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir}
		if name == "repo-main/index.html" {
			header.Typeflag = tar.TypeReg
			header.Mode = 0o644
			header.Size = 2
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if name == "repo-main/index.html" {
			if _, err := tarWriter.Write([]byte("ok")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Extract(context.Background(), &compressed, "git-github-repo-main.tar.gz", t.TempDir(), ArchiveLimits{StripTopLevel: true})
	if !errors.Is(err, ErrArchiveEntry) {
		t.Fatalf("error = %v, want ErrArchiveEntry", err)
	}
}

func TestExtractRejectsCompressedArchiveBeyondConfiguredLimit(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	part, err := writer.Create("main.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "console.log(1)"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Extract(context.Background(), &archive, "source.zip", t.TempDir(), ArchiveLimits{MaxCompressed: int64(archive.Len() - 1)})
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("Extract() error = %v, want ErrArchiveTooLarge", err)
	}
}

func TestExtractCreatesOnlyRegularFiles(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	part, err := writer.Create("src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "console.log(1)"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	stats, err := Extract(context.Background(), &archive, "source.zip", destination, ArchiveLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 1 || stats.Bytes != int64(len("console.log(1)")) {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	info, err := os.Stat(filepath.Join(destination, "src", "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("entrypoint mode = %s, want regular", info.Mode())
	}
}

func TestExtractTrustedPermitsSafeRelativeSymlink(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	content := []byte("console.log(1)")
	if err := writer.WriteHeader(&tar.Header{Name: "src/main.js", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: "current.js", Linkname: "src/main.js", Typeflag: tar.TypeSymlink, Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	stats, err := ExtractTrusted(context.Background(), &archive, "build.tar", destination, ArchiveLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 2 {
		t.Fatalf("trusted stats = %+v, want two files", stats)
	}
	link, err := os.Readlink(filepath.Join(destination, "current.js"))
	if err != nil {
		t.Fatal(err)
	}
	if link != "src/main.js" {
		t.Fatalf("symlink target = %q, want src/main.js", link)
	}
}

func TestExtractTrustedRejectsEscapingSymlink(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "link", Linkname: "../outside", Typeflag: tar.TypeSymlink, Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTrusted(context.Background(), &archive, "build.tar", t.TempDir(), ArchiveLimits{}); !errors.Is(err, ErrArchiveEntry) {
		t.Fatalf("ExtractTrusted() error = %v, want ErrArchiveEntry", err)
	}
}
