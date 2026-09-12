package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type updateTestRunner struct {
	version string
	err     error
	before  func()
}

func (runner updateTestRunner) Run(context.Context, string, io.Writer, io.Writer, string, ...string) error {
	return runner.err
}

func (runner updateTestRunner) Output(ctx context.Context, _ string, _ string, _ ...string) ([]byte, error) {
	if runner.before != nil {
		runner.before()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runner.err != nil {
		return nil, runner.err
	}
	return []byte("Stealth " + runner.version + "\nCommit: test\nBuilt: test\n"), nil
}

type updateTestServer struct {
	server               *httptest.Server
	mu                   sync.Mutex
	archive              []byte
	checksums            string
	release              githubRelease
	archiveStatus        int
	checksumsStatus      int
	archiveRequestCount  int
	checksumRequestCount int
}

func newUpdateTestServer(t *testing.T, archive []byte, checksums string) *updateTestServer {
	t.Helper()
	state := &updateTestServer{
		archive:   archive,
		checksums: checksums,
		release: githubRelease{
			TagName: "v0.2.0",
			Assets: []githubReleaseAsset{
				{Name: "stealth_Linux_x86_64.tar.gz"},
				{Name: "stealth_Linux_arm64.tar.gz"},
				{Name: "checksums.txt"},
			},
		},
	}
	state.server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/releases/latest":
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(state.release)
		case strings.HasSuffix(request.URL.Path, "/checksums.txt"):
			state.mu.Lock()
			state.checksumRequestCount++
			status := state.checksumsStatus
			checksumsBody := state.checksums
			state.mu.Unlock()
			if status == 0 {
				status = http.StatusOK
			}
			writer.WriteHeader(status)
			_, _ = io.WriteString(writer, checksumsBody)
		case strings.HasSuffix(request.URL.Path, ".tar.gz"):
			state.mu.Lock()
			state.archiveRequestCount++
			status := state.archiveStatus
			archiveBody := state.archive
			state.mu.Unlock()
			if status == 0 {
				status = http.StatusOK
			}
			writer.WriteHeader(status)
			_, _ = writer.Write(archiveBody)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(state.server.Close)
	return state
}

func newUpdateTestApp(t *testing.T, state *updateTestServer, current, target string) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	var errorsOutput bytes.Buffer
	app := NewApp(strings.NewReader(""), &output, &errorsOutput)
	app.httpClient = state.server.Client()
	app.releaseAPIBase = state.server.URL
	app.releaseDownloadBase = state.server.URL
	app.currentVersion = func() string { return current }
	app.executablePath = func() (string, error) { return target, nil }
	app.runner = updateTestRunner{version: state.release.TagName}
	return app, &output, &errorsOutput
}

func writeTestExecutable(t *testing.T, directory, contents string) string {
	t.Helper()
	target := filepath.Join(directory, "stealth")
	if err := os.WriteFile(target, []byte(contents), 0755); err != nil {
		t.Fatal(err)
	}
	return target
}

func testArchive(t *testing.T, name string, contents []byte, typeFlag byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{Name: name, Mode: 0755, Size: int64(len(contents)), Typeflag: typeFlag}
	if typeFlag == tar.TypeSymlink {
		header.Size = 0
		header.Linkname = "../../etc/passwd"
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if typeFlag == tar.TypeReg || typeFlag == tar.TypeRegA {
		if _, err := tarWriter.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func testChecksums(archive []byte, asset string) string {
	digest := sha256.Sum256(archive)
	checksum := hex.EncodeToString(digest[:]) + "  " + asset + "\n"
	if asset == "stealth_Linux_x86_64.tar.gz" {
		checksum += hex.EncodeToString(digest[:]) + "  stealth_Linux_arm64.tar.gz\n"
	} else {
		checksum += hex.EncodeToString(digest[:]) + "  stealth_Linux_x86_64.tar.gz\n"
	}
	return checksum
}

func TestRunUpdateSuccessfullyReplacesBinary(t *testing.T) {
	oldContents := "old cli"
	newContents := []byte("new cli")
	archive := testArchive(t, "stealth", newContents, tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), oldContents)
	app, output, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)

	if code := app.run([]string{"update"}); code != 0 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(newContents) {
		t.Fatalf("updated binary = %q, want %q", contents, newContents)
	}
	if !strings.Contains(output.String(), "SHA-256 checksum verified") || !strings.Contains(output.String(), "v0.1.0 → v0.2.0") {
		t.Fatalf("success output = %q", output.String())
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.archiveRequestCount != 1 || state.checksumRequestCount != 1 {
		t.Fatalf("download counts = archive %d, checksum %d", state.archiveRequestCount, state.checksumRequestCount)
	}
}

func TestRunUpdateValidatesAndRunsExtractedExecutable(t *testing.T) {
	newContents := []byte("#!/bin/sh\nprintf '%s\\n' 'Stealth v0.2.0'\n")
	archive := testArchive(t, "stealth", newContents, tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "#!/bin/sh\nprintf '%s\\n' 'Stealth v0.1.0'\n")
	app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)
	app.runner = execCommandRunner{}

	if code := app.run([]string{"update"}); code != 0 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	output, err := exec.Command(target, "version").Output()
	if err != nil {
		t.Fatalf("updated executable did not start: %v", err)
	}
	if string(output) != "Stealth v0.2.0\n" {
		t.Fatalf("updated executable output = %q", output)
	}
}

func TestRunUpdateAlreadyCurrentDoesNotDownload(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "current cli")
	app, output, errorsOutput := newUpdateTestApp(t, state, "v0.2.0", target)

	if code := app.run([]string{"update"}); code != 0 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "Stealth is already up to date") || !strings.Contains(output.String(), "v0.2.0") {
		t.Fatalf("output = %q", output.String())
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.archiveRequestCount != 0 || state.checksumRequestCount != 0 {
		t.Fatalf("already-current update downloaded assets: archive %d, checksum %d", state.archiveRequestCount, state.checksumRequestCount)
	}
}

func TestRunUpdateDoesNotDowngrade(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("old cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "newer cli")
	app, output, errorsOutput := newUpdateTestApp(t, state, "v0.3.0", target)

	if code := app.run([]string{"update"}); code != 0 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "newer cli" || !strings.Contains(output.String(), "No update performed") {
		t.Fatalf("output = %q, binary = %q", output.String(), contents)
	}
}

func TestRunUpdateDevelopmentBuildIsHandled(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "development cli")
	app, output, errorsOutput := newUpdateTestApp(t, state, "dev", target)

	if code := app.run([]string{"update", "--check"}); code != 1 {
		t.Fatalf("development check exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "Current version   dev") || !strings.Contains(output.String(), "Update available.") {
		t.Fatalf("output = %q", output.String())
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.archiveRequestCount != 0 {
		t.Fatal("--check downloaded an archive")
	}
}

func TestRunUpdateCheckExitBehavior(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, output, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)

	if code := app.run([]string{"update", "--check"}); code != 1 {
		t.Fatalf("check exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "Current version   v0.1.0") || !strings.Contains(output.String(), "Latest version    v0.2.0") || !strings.Contains(output.String(), "Update available.") {
		t.Fatalf("output = %q", output.String())
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "old cli" {
		t.Fatalf("--check changed installed binary to %q", contents)
	}
}

func TestRunUpdateRejectsChecksumFailuresAndPreservesBinary(t *testing.T) {
	tests := []struct {
		name      string
		checksums string
	}{
		{name: "mismatch", checksums: strings.Repeat("0", 64) + "  stealth_Linux_x86_64.tar.gz\n"},
		{name: "malformed", checksums: "not-a-sha  stealth_Linux_x86_64.tar.gz\n"},
		{name: "missing", checksums: "abc  another.tar.gz\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
			state := newUpdateTestServer(t, archive, test.checksums)
			target := writeTestExecutable(t, t.TempDir(), "old cli")
			app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)

			if code := app.run([]string{"update"}); code != 1 {
				t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
			}
			contents, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != "old cli" {
				t.Fatalf("failed update changed binary to %q", contents)
			}
			if !strings.Contains(errorsOutput.String(), "was not modified") {
				t.Fatalf("failure output = %q", errorsOutput.String())
			}
		})
	}
}

func TestRunUpdateRejectsUnsafeArchives(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		typeFlag byte
	}{
		{name: "traversal", path: "../stealth", typeFlag: tar.TypeReg},
		{name: "absolute", path: "/tmp/stealth", typeFlag: tar.TypeReg},
		{name: "symlink", path: "stealth", typeFlag: tar.TypeSymlink},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := testArchive(t, test.path, []byte("new cli"), test.typeFlag)
			state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
			target := writeTestExecutable(t, t.TempDir(), "old cli")
			app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)

			if code := app.run([]string{"update"}); code != 1 {
				t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
			}
			contents, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != "old cli" {
				t.Fatalf("unsafe archive changed binary to %q", contents)
			}
		})
	}
}

func TestRunUpdateReplacementFailurePreservesBinary(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)
	app.renameFile = func(string, string) error { return errors.New("read-only installation") }

	if code := app.run([]string{"update"}); code != 1 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "old cli" {
		t.Fatalf("replacement failure changed binary to %q", contents)
	}
	if !strings.Contains(errorsOutput.String(), "left unchanged") {
		t.Fatalf("failure output = %q", errorsOutput.String())
	}
}

func TestRunUpdateValidationFailurePreservesBinary(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("not a runnable cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)
	app.runner = updateTestRunner{version: "v9.9.9"}

	if code := app.run([]string{"update"}); code != 1 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "old cli" {
		t.Fatalf("validation failure changed binary to %q", contents)
	}
}

func TestRunUpdateCancellationLeavesInstallationUnchanged(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, _, _ := newUpdateTestApp(t, state, "v0.1.0", target)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	release, err := app.latestStableRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := app.performUpdate(ctx, release, "stealth_Linux_x86_64.tar.gz"); err == nil {
		t.Fatal("canceled update succeeded")
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "old cli" {
		t.Fatalf("canceled update changed binary to %q", contents)
	}
}

func TestRunUpdateRejectsMissingReleaseAssets(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	state.release.Assets = []githubReleaseAsset{{Name: "checksums.txt"}}
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)

	if code := app.run([]string{"update"}); code != 1 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	if !strings.Contains(errorsOutput.String(), "does not contain stealth_Linux_x86_64.tar.gz") {
		t.Fatalf("missing asset output = %q", errorsOutput.String())
	}
}

func TestRunUpdateRejectsNonHTTPSEndpoints(t *testing.T) {
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.releaseAPIBase = "http://github.example.test/repos/Stealth-deplover/stealth"
	if _, err := app.latestStableRelease(context.Background()); err == nil {
		t.Fatal("HTTP GitHub API endpoint was accepted")
	}
	if _, err := appendReleasePath("http://github.example.test/releases/download", "v0.2.0", "checksums.txt"); err == nil {
		t.Fatal("HTTP release asset endpoint was accepted")
	}
}

func TestRunUpdatePlainOutputHonorsNoColorAndDumbTerminal(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, output, errorsOutput := newUpdateTestApp(t, state, "v0.2.0", target)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")

	if code := app.run([]string{"update"}); code != 0 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	if strings.Contains(output.String(), "\x1b[") || strings.Contains(errorsOutput.String(), "\x1b[") {
		t.Fatalf("plain output contains ANSI escape sequence: %q %q", output.String(), errorsOutput.String())
	}
}

func TestExtractUpdateBinaryRejectsUnexpectedSecondFile(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"stealth", "other"} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(name)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if temporaryDir, _, err := extractUpdateBinary(archive.Bytes()); err == nil {
		_ = os.RemoveAll(temporaryDir)
		t.Fatal("archive with an unexpected second file was accepted")
	}
}

func TestUpdateUserAgentIncludesVersion(t *testing.T) {
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.currentVersion = func() string { return "v0.1.0" }
	if got := app.updateUserAgent(); got != "Stealth/v0.1.0 (self-update)" {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestRunUpdateReportsDownloadFailure(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	state.archiveStatus = http.StatusServiceUnavailable
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)

	if code := app.run([]string{"update"}); code != 1 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	if !strings.Contains(errorsOutput.String(), "HTTP 503") {
		t.Fatalf("download failure output = %q", errorsOutput.String())
	}
}

func TestRunUpdateReleaseMetadataRejectsPrerelease(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	state.release.Prerelease = true
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.httpClient = state.server.Client()
	app.releaseAPIBase = state.server.URL
	app.currentVersion = func() string { return "v0.1.0" }

	if _, err := app.latestStableRelease(context.Background()); err == nil {
		t.Fatal("prerelease was accepted as stable")
	}
}

func TestDisplayCurrentVersionHandlesMalformedBuildVersion(t *testing.T) {
	for _, version := range []string{"", "dev", "feature-build", "v1.2.3-rc.1"} {
		if got := displayCurrentVersion(version); got != "dev" {
			t.Fatalf("displayCurrentVersion(%q) = %q, want dev", version, got)
		}
	}
	if got := displayCurrentVersion("v1.2.3"); got != "v1.2.3" {
		t.Fatalf("displayCurrentVersion(v1.2.3) = %q", got)
	}
}

func TestCompareReleaseVersions(t *testing.T) {
	for _, test := range []struct {
		current string
		latest  string
		want    int
	}{
		{current: "v0.1.0", latest: "v0.2.0", want: -1},
		{current: "v0.2.0", latest: "v0.2.0", want: 0},
		{current: "v0.3.0", latest: "v0.2.0", want: 1},
	} {
		got, comparable := compareReleaseVersions(test.current, test.latest)
		if !comparable || got != test.want {
			t.Fatalf("compareReleaseVersions(%q, %q) = %d, %v; want %d, true", test.current, test.latest, got, comparable, test.want)
		}
	}
	if _, comparable := compareReleaseVersions("dev", "v0.2.0"); comparable {
		t.Fatal("development version was treated as comparable")
	}
}

func TestAppendReleasePathRejectsPathInjection(t *testing.T) {
	for _, component := range []string{"../other", "/absolute", ""} {
		if _, err := appendReleasePath("https://github.example.test/releases/download", component); err == nil {
			t.Fatalf("path component %q was accepted", component)
		}
	}
	if got, err := appendReleasePath("https://github.example.test/releases/download", "v0.2.0", "checksums.txt"); err != nil || !strings.HasSuffix(got, "/v0.2.0/checksums.txt") {
		t.Fatalf("appendReleasePath = %q, %v", got, err)
	}
}

func TestRunUpdateInvalidArguments(t *testing.T) {
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	if code := app.run([]string{"update", "unexpected"}); code != 2 {
		t.Fatalf("invalid argument exit code = %d", code)
	}
}

func TestRunUpdateDoesNotUseArbitraryReleaseAssetURLs(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	for index := range state.release.Assets {
		if state.release.Assets[index].Name == "stealth_Linux_x86_64.tar.gz" {
			state.release.Assets[index].BrowserDownloadURL = "https://attacker.example.test/replacement.tar.gz"
		}
	}
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)
	app.releaseDownloadBase = state.server.URL + "/releases/download"

	if code := app.run([]string{"update"}); code != 0 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
}

func TestRunUpdateMissingChecksumsStatusPreservesBinary(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, "")
	state.checksumsStatus = http.StatusNotFound
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)

	if code := app.run([]string{"update"}); code != 1 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	if !strings.Contains(errorsOutput.String(), "HTTP 404") {
		t.Fatalf("missing checksums output = %q", errorsOutput.String())
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "old cli" {
		t.Fatalf("missing checksums changed binary to %q", contents)
	}
}

func TestRunUpdateRateLimitIsActionable(t *testing.T) {
	state := newUpdateTestServer(t, nil, "")
	state.server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-RateLimit-Remaining", "0")
		writer.WriteHeader(http.StatusForbidden)
	})
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.httpClient = state.server.Client()
	app.releaseAPIBase = state.server.URL
	app.currentVersion = func() string { return "v0.1.0" }
	if _, err := app.latestStableRelease(context.Background()); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("rate-limit error = %v", err)
	}
}

func TestRunUpdateArchiveExtractionRejectsEmptyBinary(t *testing.T) {
	archive := testArchive(t, "stealth", nil, tar.TypeReg)
	if temporaryDir, _, err := extractUpdateBinary(archive); err == nil {
		_ = os.RemoveAll(temporaryDir)
		t.Fatal("empty release binary was accepted")
	}
}

func TestRunUpdateReplacementUsesAtomicRenameHook(t *testing.T) {
	archive := testArchive(t, "stealth", []byte("new cli"), tar.TypeReg)
	state := newUpdateTestServer(t, archive, testChecksums(archive, "stealth_Linux_x86_64.tar.gz"))
	target := writeTestExecutable(t, t.TempDir(), "old cli")
	app, _, errorsOutput := newUpdateTestApp(t, state, "v0.1.0", target)
	called := false
	app.renameFile = func(source, destination string) error {
		called = true
		if source == destination || filepath.Dir(source) != filepath.Dir(destination) {
			return fmt.Errorf("replacement was not staged beside target")
		}
		return os.Rename(source, destination)
	}
	if code := app.run([]string{"update"}); code != 0 {
		t.Fatalf("update exit code = %d, stderr = %s", code, errorsOutput.String())
	}
	if !called {
		t.Fatal("atomic rename hook was not called")
	}
}
